package generator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mirror-studio/internal/httpapi"
)

// Handlers owns the generator HTTP surface. Handlers never build SQL and never
// invent business codes; everything they answer is contract §5.
type Handlers struct {
	service *Service
}

// NewHandlers builds the generator HTTP handlers.
func (s *Service) NewHandlers() *Handlers { return &Handlers{service: s} }

// Register mounts every generator route.
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/generate/topics", h.topicList)
	mux.HandleFunc("POST /api/generate/topics", h.topicCreate)
	mux.HandleFunc("POST /api/generate/reference-images", h.referenceUpload)
	mux.HandleFunc("GET /api/generate/tasks", h.taskList)
	mux.HandleFunc("POST /api/generate/tasks", h.taskCreate)
	mux.HandleFunc("GET /api/workspace/history", h.history)
	mux.HandleFunc("GET /api/generate/tasks/{task_no}", h.taskDetail)
	mux.HandleFunc("DELETE /api/generate/tasks/{task_no}", h.taskDelete)
	mux.HandleFunc("POST /api/generate/tasks/{task_no}/cancel", h.taskCancel)
	h.registerChat(mux)
}

// loginRequired reproduces LoginRequiredMixin: an anonymous request is sent to
// the workspace login page with the original path in `next`.
func (h *Handlers) loginRequired(w http.ResponseWriter, r *http.Request) bool {
	if httpapi.UserOf(r) != nil {
		return false
	}
	target := "/workspace?auth=login&next=" + httpapi.PathEscapePercent(r.URL.RequestURI())
	httpapi.RedirectTo(w, r, target)
	return true
}

func (h *Handlers) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch errorKindOf(err) {
	case kindPayload:
		httpapi.BadRequest(w, err.Error())
	case kindPermission:
		httpapi.Forbidden(w, httpapi.CodePermission, err.Error())
	case kindMissing:
		httpapi.Error(w, http.StatusBadRequest, httpapi.CodeFileMissing, err.Error())
	case kindValidation:
		httpapi.Error(w, http.StatusBadRequest, httpapi.CodeValidation, err.Error())
	default:
		h.writeInternalError(w, r, err)
	}
}

// writeInternalError answers the way the reference's unhandled exception does:
// an HTML status page. The differential harness compares only the status code.
func (h *Handlers) writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	h.service.log.Error("生成接口内部错误", "error", err, "path", r.URL.Path)
	httpapi.WriteTechnicalHTML(w, r, http.StatusInternalServerError, "Server Error")
}

// ── topics ──

func (h *Handlers) topicList(w http.ResponseWriter, r *http.Request) {
	if h.loginRequired(w, r) {
		return
	}
	user := httpapi.UserOf(r)
	topics, err := h.service.topicsForUser(r.Context(), user.ID(), 50)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	items := []any{}
	for _, topic := range topics {
		items = append(items, h.service.SerializeTopic(topic))
	}
	httpapi.OK(w, items)
}

func (h *Handlers) topicCreate(w http.ResponseWriter, r *http.Request) {
	if h.loginRequired(w, r) {
		return
	}
	payload, err := httpapi.ParsePayload(r)
	if err != nil {
		h.writeError(w, r, payloadErrorFromGo(err))
		return
	}
	rawTitle := stringValue(payload["title"])
	title := defaultTaskTitle
	if rawTitle != "" {
		title = trimRunes(strings.TrimSpace(rawTitle), 128)
	}
	user := httpapi.UserOf(r)
	topic, err := h.service.insertTopic(r.Context(), user.ID(), title)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	httpapi.OK(w, h.service.SerializeTopic(topic))
}

// ── reference images ──

func (h *Handlers) referenceUpload(w http.ResponseWriter, r *http.Request) {
	if h.loginRequired(w, r) {
		return
	}
	payload, err := httpapi.ParsePayload(r)
	if err != nil {
		h.writeError(w, r, payloadErrorFromGo(err))
		return
	}
	content, mimeType, name, err := h.service.normalizeReferenceImage(
		stringValue(payload["reference_image_data_url"]), stringValue(payload["reference_image_name"]))
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if len(content) == 0 {
		h.writeError(w, r, validationFailure("请先上传参考图"))
		return
	}
	user := httpapi.UserOf(r)
	taskNo := referenceUploadTaskNo(user.ID(), time.Now())
	meta, err := h.service.media.StoreReferenceImage(r.Context(), user.ID(), taskNo, content, mimeType)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	token := createReferenceUploadToken(h.service.cfg.SecretKey, user.ID(), name, meta)
	httpapi.OK(w, map[string]any{
		"reference_upload_token":   token,
		"reference_image_name":     name,
		"reference_image_attached": true,
	})
}

func referenceUploadTaskNo(userID int64, now time.Time) string {
	return referenceUploadTaskNoPrefix + strconv.FormatInt(userID, 10) +
		strconv.FormatInt(now.UnixMilli(), 10)
}

// ── tasks ──

func (h *Handlers) taskList(w http.ResponseWriter, r *http.Request) {
	if h.loginRequired(w, r) {
		return
	}
	user := httpapi.UserOf(r)
	query := r.URL.Query()
	page := parseIntDefault(query.Get("page"), 1)
	if page < 1 {
		page = 1
	}
	pageSize := parseIntDefault(query.Get("page_size"), 24)
	if pageSize < 1 {
		pageSize = 1
	}
	if pageSize > 48 {
		pageSize = 48
	}
	keyword := strings.TrimSpace(query.Get("q"))
	if keyword == "" {
		keyword = strings.TrimSpace(query.Get("keyword"))
	}
	var topicID *int64
	if raw := strings.TrimSpace(query.Get("topic_id")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			topicID = &parsed
		}
	}

	total, err := h.service.countTasks(r.Context(), user.ID(), topicID, keyword)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	totalPages := pageCount(total, pageSize)
	if page > totalPages {
		page = totalPages
	}
	if page < 1 {
		page = 1
	}
	tasks, err := h.service.listTasks(r.Context(), user.ID(), topicID, keyword, pageSize, (page-1)*pageSize)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	if err := h.service.attachImages(r.Context(), tasks); err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	items := make([]any, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, h.service.SerializeTask(
			h.service.reconcileTaskRuntimeState(r.Context(), task), user.ID(), true))
	}
	httpapi.OK(w, map[string]any{
		"items": items,
		"pagination": map[string]any{
			"page":         page,
			"page_size":    pageSize,
			"total":        total,
			"total_pages":  totalPages,
			"has_next":     page < totalPages,
			"has_previous": page > 1,
		},
	})
}

func (h *Handlers) taskCreate(w http.ResponseWriter, r *http.Request) {
	if h.loginRequired(w, r) {
		return
	}
	payload, err := httpapi.ParsePayload(r)
	if err != nil {
		h.writeError(w, r, payloadErrorFromGo(err))
		return
	}
	user := httpapi.UserOf(r)
	input := SubmitInput{
		UserID:                user.ID(),
		ClientIP:              httpapi.From(r).ClientIP,
		Prompt:                stringValue(payload["prompt"]),
		NegativePrompt:        stringValue(payload["negative_prompt"]),
		StyleCode:             stringValue(payload["style_code"]),
		SizeCode:              stringValue(payload["size_code"]),
		QualityCode:           stringValue(payload["quality_code"]),
		GenerationMode:        stringValue(payload["generation_mode"]),
		ReferenceImageDataURL: stringValue(payload["reference_image_data_url"]),
		ReferenceImageName:    stringValue(payload["reference_image_name"]),
		ReferenceUploadToken:  stringValue(payload["reference_upload_token"]),
		ClientRequestID:       stringValue(payload["client_request_id"]),
	}
	if topicID := intValue(payload["topic_id"]); topicID != 0 {
		input.TopicID = &topicID
	}
	task, err := h.service.Submit(r.Context(), input)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	task = h.service.waitForTask(r.Context(), task, payload["wait_seconds"])
	if task == nil {
		httpapi.WriteTechnicalHTML(w, r, http.StatusInternalServerError, "Server Error")
		return
	}
	httpapi.OK(w, h.service.SerializeTask(task, user.ID(), true))
}

func (h *Handlers) taskDetail(w http.ResponseWriter, r *http.Request) {
	if h.loginRequired(w, r) {
		return
	}
	user := httpapi.UserOf(r)
	taskNo := r.PathValue("task_no")
	task, err := h.service.TaskByNo(r.Context(), user.ID(), taskNo)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	if task == nil {
		// get_object_or_404 in the reference: the debug error page, not an envelope.
		httpapi.WriteTechnicalHTML(w, r, http.StatusNotFound, "Page not found")
		return
	}
	task = h.service.reconcileTaskRuntimeState(r.Context(), task)
	if err := h.service.attachImages(r.Context(), []*Task{task}); err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	httpapi.OK(w, h.service.SerializeTask(task, user.ID(), true))
}

func (h *Handlers) taskDelete(w http.ResponseWriter, r *http.Request) {
	if h.loginRequired(w, r) {
		return
	}
	user := httpapi.UserOf(r)
	taskNo := r.PathValue("task_no")
	if err := h.service.DeleteHistory(r.Context(), user.ID(), taskNo); err != nil {
		h.writeError(w, r, err)
		return
	}
	httpapi.OK(w, map[string]any{"task_no": taskNo, "deleted": true})
}

func (h *Handlers) taskCancel(w http.ResponseWriter, r *http.Request) {
	if h.loginRequired(w, r) {
		return
	}
	user := httpapi.UserOf(r)
	taskNo := r.PathValue("task_no")
	task, err := h.service.Cancel(r.Context(), user.ID(), taskNo)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpapi.OK(w, map[string]any{"task_no": task.TaskNo, "status": task.Status})
}

func (h *Handlers) history(w http.ResponseWriter, r *http.Request) {
	if h.loginRequired(w, r) {
		return
	}
	user := httpapi.UserOf(r)
	query := r.URL.Query()
	page := parseIntDefault(query.Get("page"), 1)
	if page < 1 {
		page = 1
	}
	pageSize := parseIntDefault(query.Get("page_size"), 24)
	if pageSize < 1 {
		pageSize = 1
	}
	if pageSize > 48 {
		pageSize = 48
	}
	keyword := strings.TrimSpace(query.Get("q"))
	if keyword == "" {
		keyword = strings.TrimSpace(query.Get("keyword"))
	}
	payload, err := h.service.HistoryPage(r.Context(), user.ID(), keyword, page, pageSize)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	httpapi.OK(w, payload)
}

// ── helpers ──

func payloadErrorFromGo(err error) error {
	if errors.Is(err, httpapi.ErrRequestTooBig) {
		return validationFailure("参考图过大，请压缩后再试")
	}
	return payloadFailure(err.Error())
}

func parseIntDefault(value string, fallback int) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil {
		return fallback
	}
	return parsed
}

// waitForTask ports views.py `_wait_for_task`: poll every 0.5s for at most
// wait_seconds (clamped to [0,10]), pushing progress_percent through the
// milestones the workspace SPA renders.
func (s *Service) waitForTask(ctx context.Context, task *Task, waitSeconds any) *Task {
	seconds := asFloat(waitSeconds)
	if seconds < 0 {
		seconds = 0
	}
	if seconds > 10 {
		seconds = 10
	}
	if seconds <= 0 {
		if fresh, err := s.reloadTask(ctx, task.TaskNo); err == nil && fresh != nil {
			return fresh
		}
		return task
	}

	deadline := time.Now().Add(time.Duration(seconds * float64(time.Second)))
	milestones := []int{35, 52, 68, 82, 90}
	step := 0
	current := task
	for time.Now().Before(deadline) {
		fresh, err := s.reloadTask(ctx, task.TaskNo)
		if err != nil {
			s.log.Warn("等待任务刷新失败", "task_no", task.TaskNo, "error", err)
			break
		}
		if fresh == nil {
			break
		}
		current = fresh
		if isTerminalStatus(current.Status) || len(current.Images) > 0 {
			return current
		}
		if current.Status == StatusQueued || current.Status == StatusRunning {
			index := step
			if index > len(milestones)-1 {
				index = len(milestones) - 1
			}
			if err := s.bumpTaskProgress(ctx, current.ID, milestones[index]); err != nil {
				s.log.Warn("任务进度更新失败", "task_no", current.TaskNo, "error", err)
			}
		}
		step++
		if !s.sleep(ctx, 500*time.Millisecond) {
			break
		}
	}
	if fresh, err := s.reloadTask(ctx, task.TaskNo); err == nil && fresh != nil {
		return fresh
	}
	return current
}

func isTerminalStatus(status string) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusCanceled:
		return true
	}
	return false
}

func (s *Service) reloadTask(ctx context.Context, taskNo string) (*Task, error) {
	task, err := s.taskByNoAny(ctx, taskNo)
	if err != nil || task == nil {
		return task, err
	}
	if err := s.attachImages(ctx, []*Task{task}); err != nil {
		return nil, err
	}
	return task, nil
}

func asFloat(value any) float64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0
		}
		return parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}
