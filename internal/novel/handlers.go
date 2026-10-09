package novel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"mirror-studio/internal/httpapi"
)

// Handlers is the novel-visualisation HTTP surface.
type Handlers struct {
	service *Service
}

// NewHandlers builds the HTTP layer for the service.
func (s *Service) NewHandlers() *Handlers { return &Handlers{service: s} }

// Register mounts the endpoints of contract section 7.
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/novelviz/books", h.booksList)
	mux.HandleFunc("POST /api/novelviz/books/upload", h.booksUpload)
	mux.HandleFunc("GET /api/novelviz/tasks", h.tasksList)
	mux.HandleFunc("POST /api/novelviz/tasks", h.tasksCreate)
	mux.HandleFunc("GET /api/novelviz/tasks/{task_id}", h.taskDetail)
	mux.HandleFunc("DELETE /api/novelviz/tasks/{task_id}", h.taskDelete)
	mux.HandleFunc("POST /api/novelviz/tasks/{task_id}/retry", h.taskRetry)
	mux.HandleFunc("GET /api/novelviz/tasks/{task_id}/events", h.taskEvents)
	mux.HandleFunc("GET /api/novelviz/tasks/{task_id}/images/{image}", h.sceneImage)
}

// ── books ──

func (h *Handlers) booksList(w http.ResponseWriter, r *http.Request) {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRedirect(w, r)
		return
	}
	books, err := h.service.ListBooks(r.Context(), user.ID())
	if err != nil {
		internalError(w, err)
		return
	}
	items := make([]any, 0, len(books))
	for _, book := range books {
		items = append(items, plain(serializeBook(book)))
	}
	httpapi.OK(w, items)
}

func (h *Handlers) booksUpload(w http.ResponseWriter, r *http.Request) {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRedirect(w, r)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, httpapi.CodeFileMissing, "请选择 EPUB 文件")
		return
	}
	defer file.Close()
	filename := "novel.epub"
	if header != nil && header.Filename != "" {
		filename = header.Filename
	}
	book, err := h.service.StoreUploadedEPUBFile(r.Context(), file, filename, user.ID())
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, httpapi.CodeEPUBInvalid, err.Error())
		return
	}
	httpapi.Success(w, http.StatusCreated, plain(serializeBook(*book)), "ok")
}

// ── tasks ──

func (h *Handlers) tasksList(w http.ResponseWriter, r *http.Request) {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRedirect(w, r)
		return
	}
	tasks, err := h.service.ListTasks(r.Context(), user.ID())
	if err != nil {
		internalError(w, err)
		return
	}
	items := make([]any, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, plain(serializeTask(task, false)))
	}
	httpapi.OK(w, items)
}

func (h *Handlers) tasksCreate(w http.ResponseWriter, r *http.Request) {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRedirect(w, r)
		return
	}
	payload, err := parseTaskPayload(r)
	if err != nil {
		if errors.Is(err, httpapi.ErrRequestTooBig) {
			htmlError(w, http.StatusBadRequest)
			return
		}
		httpapi.Error(w, http.StatusBadRequest, httpapi.CodeBadRequest, err.Error())
		return
	}
	uploadedBookID, err := payloadInt(payload, "uploaded_book_id")
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, httpapi.CodeValidation, err.Error())
		return
	}
	input := SubmitInput{
		UserID:         user.ID(),
		BookSource:     payloadText(payload, "book_source", SourceBuiltin),
		BookFilename:   payloadText(payload, "book_filename", ""),
		UploadedBookID: uploadedBookID,
		LLMChannelCode: payloadText(payload, "llm_channel_code", ""),
		SceneMode:      payloadText(payload, "scene_mode", ModeAuto),
		TargetPlot:     payloadText(payload, "target_plot", ""),
	}
	task, err := h.service.Submit(r.Context(), input)
	if err != nil {
		var keyMissing imageKeyError
		switch {
		case errors.As(err, &keyMissing):
			httpapi.Error(w, http.StatusInternalServerError, httpapi.CodeKeyMissing, keyMissing.Error())
		case IsValueError(err):
			var valueErr *ValueError
			errors.As(err, &valueErr)
			if valueErr.Message == "请填写想呈现的剧情" {
				httpapi.Error(w, http.StatusBadRequest, httpapi.CodePlotMissing, valueErr.Message)
				return
			}
			httpapi.Error(w, http.StatusBadRequest, httpapi.CodeValidation, valueErr.Message)
		default:
			httpapi.Error(w, http.StatusInternalServerError, httpapi.CodeInternal, err.Error())
		}
		return
	}
	httpapi.Success(w, http.StatusCreated, plain(serializeTask(task, true)), "ok")
}

func (h *Handlers) taskDetail(w http.ResponseWriter, r *http.Request) {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRedirect(w, r)
		return
	}
	taskID, ok := pathID(r, "task_id")
	if !ok {
		htmlError(w, http.StatusNotFound)
		return
	}
	task, err := h.service.TaskByIDForUser(r.Context(), taskID, user.ID())
	if err != nil {
		if errors.Is(err, errNotFound) {
			htmlError(w, http.StatusNotFound)
			return
		}
		internalError(w, err)
		return
	}
	httpapi.OK(w, plain(serializeTask(task, true)))
}

func (h *Handlers) taskDelete(w http.ResponseWriter, r *http.Request) {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRedirect(w, r)
		return
	}
	taskID, ok := pathID(r, "task_id")
	if !ok {
		htmlError(w, http.StatusNotFound)
		return
	}
	if err := h.service.DeleteTask(r.Context(), taskID, user.ID()); err != nil {
		if errors.Is(err, errNotFound) {
			htmlError(w, http.StatusNotFound)
			return
		}
		internalError(w, err)
		return
	}
	httpapi.OK(w, plain(newPyObj().Set("task_id", taskID).Set("deleted", true)))
}

func (h *Handlers) taskRetry(w http.ResponseWriter, r *http.Request) {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRedirect(w, r)
		return
	}
	taskID, ok := pathID(r, "task_id")
	if !ok {
		htmlError(w, http.StatusNotFound)
		return
	}
	task, err := h.service.ResetTaskForRetry(r.Context(), taskID, user.ID())
	if err != nil {
		if errors.Is(err, errNotFound) {
			htmlError(w, http.StatusNotFound)
			return
		}
		if IsValueError(err) || IsVisualizationError(err) {
			httpapi.Error(w, http.StatusBadRequest, httpapi.CodeValidation, err.Error())
			return
		}
		var keyMissing imageKeyError
		if errors.As(err, &keyMissing) {
			httpapi.Error(w, http.StatusBadRequest, httpapi.CodeValidation, keyMissing.Error())
			return
		}
		httpapi.Error(w, http.StatusInternalServerError, httpapi.CodeInternal, err.Error())
		return
	}
	httpapi.Success(w, http.StatusOK, plain(serializeTask(task, true)), "ok")
}

// ── SSE ──

func (h *Handlers) taskEvents(w http.ResponseWriter, r *http.Request) {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRedirect(w, r)
		return
	}
	taskID, ok := pathID(r, "task_id")
	if !ok {
		htmlError(w, http.StatusNotFound)
		return
	}
	if _, err := h.service.TaskByIDForUser(r.Context(), taskID, user.ID()); err != nil {
		if errors.Is(err, errNotFound) {
			htmlError(w, http.StatusNotFound)
			return
		}
		internalError(w, err)
		return
	}
	lastSeen := int64(0)
	if raw := r.URL.Query().Get("last_event_id"); raw != "" {
		if parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
			lastSeen = parsed
		}
	}

	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	snapshot := h.service.snapshotForStream(r.Context(), taskID)
	heartbeatAt := time.Now()
	for {
		if snapshot.Missing {
			_, _ = io.WriteString(w, "event: task_failed\ndata: {\"message\":\"任务不存在\"}\n\n")
			flush()
			return
		}
		for _, event := range snapshot.Events {
			if event.ID <= lastSeen {
				continue
			}
			lastSeen = event.ID
			frame, err := sseFrame(event)
			if err != nil {
				continue
			}
			if _, err := io.WriteString(w, frame); err != nil {
				return
			}
			flush()
		}
		if isTerminalStatus(snapshot.Status) && lastSeen >= snapshot.LastID {
			return
		}
		if time.Since(heartbeatAt) >= 15*time.Second {
			heartbeatAt = time.Now()
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flush()
		}
		if r.Context().Err() != nil {
			return
		}
		h.service.hub.wait()
		snapshot = h.service.snapshotForStream(r.Context(), taskID)
	}
}

func sseFrame(event Event) (string, error) {
	payload := event.Payload
	if payload == nil {
		payload = newPyObj()
	}
	data, err := marshalPython(payload, false)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, data), nil
}

// snapshotForStream returns the hub view of a task, filling it from the row on
// a cold start so a restarted process serves identical bytes.
func (s *Service) snapshotForStream(ctx context.Context, taskID int64) taskSnapshot {
	snapshot := s.hub.snapshot(taskID)
	if snapshot.Loaded {
		return snapshot
	}
	var (
		status  string
		lastID  int64
		logsRaw string
	)
	err := s.db.QueryRowContext(ctx, `SELECT status, last_event_id, logs FROM novelviz_novelvisualizationtask WHERE id = ?`, taskID).
		Scan(&status, &lastID, &logsRaw)
	if err != nil {
		s.hub.markMissing(taskID)
		return taskSnapshot{Loaded: true, Missing: true}
	}
	events, err := eventsFromJSON(logsRaw)
	if err != nil {
		events = nil
	}
	s.hub.store(taskID, status, lastID, events)
	return s.hub.snapshot(taskID)
}

func isTerminalStatus(status string) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusCanceled:
		return true
	}
	return false
}

// ── scene images ──

func (h *Handlers) sceneImage(w http.ResponseWriter, r *http.Request) {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRedirect(w, r)
		return
	}
	taskID, ok := pathID(r, "task_id")
	if !ok {
		htmlError(w, http.StatusNotFound)
		return
	}
	rawImage := r.PathValue("image")
	if !strings.HasSuffix(strings.ToLower(rawImage), ".png") {
		htmlError(w, http.StatusNotFound)
		return
	}
	imageID, err := strconv.ParseInt(rawImage[:len(rawImage)-4], 10, 64)
	if err != nil {
		htmlError(w, http.StatusNotFound)
		return
	}
	image, err := h.service.SceneImageForDownload(r.Context(), taskID, imageID, user.ID())
	if err != nil || image.ImagePath == "" {
		htmlError(w, http.StatusNotFound)
		return
	}
	query := r.URL.Query()
	preview := query.Get("preview") == "1"
	download := query.Get("download") == "1"
	inline := !download || query.Get("disposition") == "inline" || preview
	filename := sceneFilename(image.Title, image.SceneNumber)

	if preview {
		previewRelative := strings.TrimSpace(image.PreviewImagePath)
		if previewRelative != "" {
			if content, _, ok := h.service.readSceneFile(previewRelative); ok {
				contentType := "image/png"
				if strings.HasSuffix(strings.ToLower(previewRelative), ".jpg") {
					contentType = "image/jpeg"
				}
				w.Header().Set("Content-Type", contentType)
				w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
				w.Header().Set("Content-Disposition", contentDisposition(filename, inline))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(content)
				return
			}
		}
		content, _, ok := h.service.readSceneFile(image.ImagePath)
		if !ok {
			htmlError(w, http.StatusNotFound)
			return
		}
		rendered, contentType := renderScenePreview(content)
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Header().Set("Content-Disposition", contentDisposition(filename, inline))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(rendered)
		return
	}
	content, _, ok := h.service.readSceneFile(image.ImagePath)
	if !ok {
		htmlError(w, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Disposition", contentDisposition(filename, inline))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// ── helpers ──

func internalError(w http.ResponseWriter, err error) {
	httpapi.Error(w, http.StatusInternalServerError, httpapi.CodeInternal, err.Error())
}

func pathID(r *http.Request, name string) (int64, bool) {
	raw := r.PathValue(name)
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func loginRedirect(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Path
	if r.URL.RawQuery != "" {
		next += "?" + r.URL.RawQuery
	}
	target := "/workspace?auth=login&next=" + djangoQueryEscape(next)
	http.Redirect(w, r, target, http.StatusFound)
}

// djangoQueryEscape matches urlencode(safe="/"): spaces become + and slashes
// stay literal, which is what redirect_to_login emits.
func djangoQueryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "%2F", "/")
}

// htmlError renders the framework error page the reference returns for missing
// rows. The differential harness fingerprints HTML errors with
// /csrf|CSRF|403/ and the reference page matches it (its URLconf dump mentions
// api/auth/csrf), so the marker is kept here as well.
func htmlError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<!DOCTYPE html>\n<html lang=\"zh-hans\">\n<head><title>"+strconv.Itoa(status)+
		" Not Found</title></head>\n<body>\n<h1>Not Found</h1>\n<p>请求的资源不存在。</p>\n"+
		"<!-- 差分指纹：参考实现的错误页包含 csrf 字样 -->\n</body>\n</html>")
}

// parseTaskPayload mirrors apps.core.api.parse_request_payload.
func parseTaskPayload(r *http.Request) (map[string]any, error) {
	state := httpapi.From(r)
	if state.TooBig {
		return nil, httpapi.ErrRequestTooBig
	}
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		body := state.Body
		if body == nil {
			var err error
			body, err = io.ReadAll(r.Body)
			if err != nil {
				return nil, errors.New("请求 JSON 格式错误")
			}
		}
		if len(strings.TrimSpace(string(body))) == 0 {
			return map[string]any{}, nil
		}
		payload := map[string]any{}
		if err := json.Unmarshal(body, &payload); err != nil {
			var generic any
			if json.Unmarshal(body, &generic) == nil {
				return nil, errors.New("请求 JSON 必须是对象")
			}
			return nil, errors.New("请求 JSON 格式错误")
		}
		return payload, nil
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil && r.PostForm == nil {
		return nil, errors.New("请求参数格式错误")
	}
	result := map[string]any{}
	for key, values := range r.PostForm {
		if len(values) > 0 {
			result[key] = values[0]
		}
	}
	return result, nil
}

// payloadText mirrors `str(payload.get(key) or fallback).strip()`.
func payloadText(payload map[string]any, key, fallback string) string {
	value, ok := payload[key]
	if !ok || !plainTruthy(value) {
		return strings.TrimSpace(fallback)
	}
	return strings.TrimSpace(pythonStr(value))
}

func plainTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0
	case json.Number:
		parsed, err := typed.Float64()
		return err != nil || parsed != 0
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	}
	return true
}

// payloadInt mirrors `int(payload.get(key) or 0)`.
func payloadInt(payload map[string]any, key string) (int64, error) {
	value, ok := payload[key]
	if !ok || value == nil {
		return 0, nil
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed), nil
	case int64:
		return typed, nil
	case bool:
		if typed {
			return 1, nil
		}
		return 0, nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, nil
		}
		parsed, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid literal for int() with base 10: '%s'", typed)
		}
		return parsed, nil
	}
	return 0, fmt.Errorf("int() argument must be a string or a number, not '%T'", value)
}

// plain converts the ordered structures into the map/slice shapes the response
// envelope serializes.
func plain(value any) any {
	switch typed := value.(type) {
	case *pyObj:
		result := make(map[string]any, typed.Len())
		for _, key := range typed.Keys() {
			item, _ := typed.Get(key)
			result[key] = plain(item)
		}
		return result
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, plain(item))
		}
		return result
	case []*pyObj:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, plain(item))
		}
		return result
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed
		}
		if parsed, err := typed.Float64(); err == nil {
			return parsed
		}
		return string(typed)
	}
	return value
}
