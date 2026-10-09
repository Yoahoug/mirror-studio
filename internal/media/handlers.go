package media

import (
	"errors"
	"net/http"
	"strconv"

	"mirror-studio/internal/httpapi"
)

// Handlers is the transport layer of the media domain. It never touches SQL and
// only shapes requests and responses.
type Handlers struct {
	service *Service
}

// NewHandlers builds the media handlers.
func (s *Service) NewHandlers() *Handlers { return &Handlers{service: s} }

// Register wires the media routes. /downloads keeps the reference's
// trailing-slash-only form: the pattern requires the slash, so any other
// spelling is handled by the router.
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/images", h.listImages)
	mux.HandleFunc("GET /api/images/{image_id}", h.imageDetail)
	mux.HandleFunc("POST /api/images/{image_id}/download-url", h.imageDownloadURL)
	mux.HandleFunc("GET /downloads/{token}/", h.signedDownload)
	mux.HandleFunc("POST /api/uploads/sessions", h.createUploadSession)
	mux.HandleFunc("GET /api/uploads/sessions/{upload_id}", h.uploadSessionDetail)
	mux.HandleFunc("DELETE /api/uploads/sessions/{upload_id}", h.cancelUploadSession)
	mux.HandleFunc("PUT /api/uploads/sessions/{upload_id}/chunks/{index}", h.uploadChunk)
	mux.HandleFunc("POST /api/uploads/sessions/{upload_id}/complete", h.completeUploadSession)
}

// listImages serves GET /api/images: a bare array, capped at 100 and not
// paginated (review P1-25).
func (h *Handlers) listImages(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	images, err := h.service.listImages(r.Context(), user.ID(), query.Get("status"), query.Get("keyword"), 100)
	if err != nil {
		h.writeServerError(w, r, err)
		return
	}
	payload := make([]map[string]any, 0, len(images))
	for _, image := range images {
		payload = append(payload, serializeImage(image))
	}
	httpapi.OK(w, payload)
}

// imageDetail serves GET /api/images/{id}; a missing image or one owned by
// another user is an HTML 404.
func (h *Handlers) imageDetail(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	image, ok := h.lookupImage(w, r, user.ID())
	if !ok {
		return
	}
	httpapi.OK(w, serializeImage(image))
}

// imageDownloadURL serves POST /api/images/{id}/download-url.
func (h *Handlers) imageDownloadURL(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	image, ok := h.lookupImage(w, r, user.ID())
	if !ok {
		return
	}
	if image.Status != StatusAvailable {
		httpapi.Error(w, http.StatusNotFound, httpapi.CodeNotDownloadable, "图片已过期或不可下载")
		return
	}
	url, err := h.service.IssueDownloadURL(r.Context(), image, user.ID(), false, false)
	if err != nil {
		h.writeServerError(w, r, err)
		return
	}
	previewURL, err := h.service.IssueDownloadURL(r.Context(), image, user.ID(), true, true)
	if err != nil {
		h.writeServerError(w, r, err)
		return
	}
	httpapi.OK(w, map[string]any{"url": url, "preview_url": previewURL})
}

// signedDownload serves GET /downloads/{token}/ for one signed link.
func (h *Handlers) signedDownload(w http.ResponseWriter, r *http.Request) {
	image, err := h.service.ResolveDownloadToken(r.Context(), r.PathValue("token"), downloadTokenMaxAge)
	if err != nil {
		writeNotFoundHTML(w, r)
		return
	}
	query := r.URL.Query()
	content, contentType, disposition, cacheControl, err := h.service.BuildDownloadResponse(
		r.Context(), image, query.Get("disposition") == "inline", query.Get("preview") == "1")
	if err != nil {
		if errors.Is(err, ErrImageUnavailable) {
			writeNotFoundHTML(w, r)
			return
		}
		h.writeServerError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Cache-Control", cacheControl)
	if query.Get("preview") == "1" {
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// createUploadSession serves POST /api/uploads/sessions.
func (h *Handlers) createUploadSession(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	payload, err := httpapi.ParsePayload(r)
	if err != nil {
		if errors.Is(err, httpapi.ErrRequestTooBig) {
			writeBadRequestHTML(w, r)
			return
		}
		httpapi.BadRequest(w, err.Error())
		return
	}
	data, err := h.service.CreateOrResumeUploadSession(
		r.Context(), user.ID(),
		stringField(payload, "kind"),
		stringField(payload, "filename"),
		stringField(payload, "content_type"),
		payload["total_size"],
		stringField(payload, "client_upload_id"),
	)
	if err != nil {
		h.writeUploadError(w, r, err)
		return
	}
	httpapi.Success(w, http.StatusCreated, data, "ok")
}

// uploadSessionDetail serves GET /api/uploads/sessions/{upload_id}.
func (h *Handlers) uploadSessionDetail(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	data, err := h.service.GetUploadSession(r.Context(), user.ID(), r.PathValue("upload_id"))
	if err != nil {
		h.writeUploadError(w, r, err)
		return
	}
	httpapi.OK(w, data)
}

// cancelUploadSession serves DELETE /api/uploads/sessions/{upload_id}.
func (h *Handlers) cancelUploadSession(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	data, err := h.service.CancelUploadSession(r.Context(), user.ID(), r.PathValue("upload_id"))
	if err != nil {
		h.writeUploadError(w, r, err)
		return
	}
	httpapi.OK(w, data)
}

// uploadChunk serves PUT /api/uploads/sessions/{upload_id}/chunks/{index}.
func (h *Handlers) uploadChunk(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 {
		writeNotFoundHTML(w, r)
		return
	}
	state := httpapi.From(r)
	if state.TooBig {
		writeBadRequestHTML(w, r)
		return
	}
	data, err := h.service.StoreUploadChunk(r.Context(), user.ID(), r.PathValue("upload_id"), index, state.Body)
	if err != nil {
		h.writeUploadError(w, r, err)
		return
	}
	httpapi.OK(w, data)
}

// completeUploadSession serves POST /api/uploads/sessions/{upload_id}/complete.
func (h *Handlers) completeUploadSession(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	data, err := h.service.CompleteUploadSession(r.Context(), user.ID(), r.PathValue("upload_id"))
	if err != nil {
		h.writeUploadError(w, r, err)
		return
	}
	httpapi.OK(w, data)
}

// lookupImage resolves the path id against the caller's library.
func (h *Handlers) lookupImage(w http.ResponseWriter, r *http.Request, userID int64) (*Image, bool) {
	imageID, err := strconv.ParseInt(r.PathValue("image_id"), 10, 64)
	if err != nil {
		writeNotFoundHTML(w, r)
		return nil, false
	}
	image, err := h.service.imageByID(r.Context(), userID, imageID)
	if err != nil {
		h.writeServerError(w, r, err)
		return nil, false
	}
	if image == nil {
		writeNotFoundHTML(w, r)
		return nil, false
	}
	return image, true
}

// requireUser enforces the login requirement the same way the reference mixin
// does: an anonymous caller is redirected with the original path as `next`.
func (h *Handlers) requireUser(w http.ResponseWriter, r *http.Request) (httpapi.Principal, bool) {
	user := httpapi.UserOf(r)
	if user == nil {
		h.redirectToLogin(w, r)
		return nil, false
	}
	return user, true
}

// redirectToLogin answers with the framework's redirect: an empty HTML body and
// the original path, escaped like QueryDict.urlencode(safe="/").
func (h *Handlers) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Location", "/workspace?auth=login&next="+httpapi.PathEscapePercent(fullPath(r)))
	w.WriteHeader(http.StatusFound)
}

func (h *Handlers) writeUploadError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrUploadSessionNotFound) {
		writeNotFoundHTML(w, r)
		return
	}
	if message, ok := invalidMessage(err); ok {
		httpapi.Error(w, http.StatusBadRequest, httpapi.CodeValidation, message)
		return
	}
	h.writeServerError(w, r, err)
}

func (h *Handlers) writeServerError(w http.ResponseWriter, r *http.Request, err error) {
	h.service.logger().Error("media request failed", "path", r.URL.Path, "error", err)
	writeServerErrorHTML(w, r)
}

func fullPath(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return r.URL.Path
	}
	return r.URL.Path + "?" + r.URL.RawQuery
}

func stringField(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

// serializeImage is the item shape of /api/images, /api/images/{id} and the
// image list of a task payload.
func serializeImage(image *Image) map[string]any {
	var expiresAt any
	if !image.ExpiresAt.IsZero() {
		expiresAt = pythonIsoformat(image.ExpiresAt)
	}
	return map[string]any{
		"id":         image.ID,
		"task_no":    image.TaskNo,
		"prompt":     image.Prompt,
		"status":     image.Status,
		"width":      image.Width,
		"height":     image.Height,
		"file_size":  image.FileSize,
		"created_at": pythonIsoformat(image.CreatedAt),
		"expires_at": expiresAt,
	}
}
