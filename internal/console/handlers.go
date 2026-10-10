package console

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"encoding/json"

	"mirror-studio/internal/httpapi"
	"mirror-studio/internal/siteconfig"
)

// Handlers serves the console routes.
type Handlers struct {
	svc *Service
}

// Register mounts every /console/api/ endpoint on the mux. Method mismatches
// answer 405 with Django's Allow header, and unknown paths answer an HTML 404,
// which is what the URL resolver does for the reference implementation.
func (h *Handlers) Register(mux *http.ServeMux) {
	h.route(mux, "/console/api/dashboard/", []string{http.MethodGet}, h.dashboard)
	h.route(mux, "/console/api/users/", []string{http.MethodGet}, h.userList)
	h.route(mux, "/console/api/users/{pk}/", []string{http.MethodGet}, h.userDetail)
	h.route(mux, "/console/api/users/{pk}/quota/", []string{http.MethodPost}, h.userQuotaUpdate)
	h.route(mux, "/console/api/tasks/", []string{http.MethodGet}, h.taskList)
	h.route(mux, "/console/api/config/", []string{http.MethodGet}, h.configCollection)
	h.route(mux, "/console/api/config/batch/", []string{http.MethodPut}, h.configBatch)
	h.route(mux, "/console/api/config/{key}/", []string{http.MethodGet, http.MethodPut}, h.configDetail)
	h.route(mux, "/console/api/image-channels/", []string{http.MethodGet, http.MethodPost}, h.channelCollection)
	h.route(mux, "/console/api/image-channels/{pk}/", []string{http.MethodGet, http.MethodPut, http.MethodDelete}, h.channelDetail)
	mux.HandleFunc("/console/api/", h.consoleNotFound)
}

func (h *Handlers) route(mux *http.ServeMux, path string, methods []string, handler http.HandlerFunc) {
	mux.HandleFunc(path+"{$}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Allow", allowHeader(methods))
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			return
		}
		if !methodAllowed(methods, r.Method) {
			writeMethodNotAllowed(w, methods)
			return
		}
		handler(w, r)
	})
}

func methodAllowed(methods []string, method string) bool {
	for _, item := range methods {
		if item == method {
			return true
		}
		if item == http.MethodGet && method == http.MethodHead {
			return true
		}
	}
	return false
}

// allowHeader lists the methods in Django's http_method_names order.
func allowHeader(methods []string) string {
	order := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"}
	allowed := map[string]bool{}
	for _, item := range methods {
		allowed[item] = true
	}
	if allowed["GET"] {
		allowed["HEAD"] = true
	}
	allowed["OPTIONS"] = true
	names := []string{}
	for _, name := range order {
		if allowed[name] {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func (h *Handlers) consoleNotFound(w http.ResponseWriter, r *http.Request) {
	writeHTMLStatus(w, http.StatusNotFound, "Not Found")
}

func (h *Handlers) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.svc.logError("console request failed", err)
	writeHTMLStatus(w, http.StatusInternalServerError, "Internal Server Error")
}

func writeMethodNotAllowed(w http.ResponseWriter, methods []string) {
	w.Header().Set("Allow", allowHeader(methods))
	writeHTMLStatus(w, http.StatusMethodNotAllowed, "Method Not Allowed")
}

func writeHTMLStatus(w http.ResponseWriter, status int, title string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<!DOCTYPE html><html lang=\"zh-hans\"><head><title>"+title+"</title></head><body><h1>"+title+"</h1></body></html>")
}

// payload parses the request body the way the reference payload parser does.
func (h *Handlers) payload(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	body, err := httpapi.ParsePayload(r)
	if err != nil {
		if errors.Is(err, httpapi.ErrRequestTooBig) {
			writeHTMLStatus(w, http.StatusBadRequest, "Bad Request")
			return nil, false
		}
		httpapi.BadRequest(w, err.Error())
		return nil, false
	}
	return body, true
}

// pathID reads an integer path segment. Django's <int:> converter does not
// match empty or negative segments, which fall through to a routing 404.
func (h *Handlers) pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value := r.PathValue("pk")
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 0 {
		writeHTMLStatus(w, http.StatusNotFound, "Not Found")
		return 0, false
	}
	return id, true
}

func (h *Handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	today, totals, err := h.svc.counts(ctx)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	latestTasks, err := h.svc.listTasks(ctx, taskFilter{}, 10, 0, "t.created_at DESC")
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	latestUsers, err := h.svc.listUsers(ctx, userFilter{}, 10, 0)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	recentFailures, err := h.svc.listTasks(ctx, taskFilter{Status: "failed"}, 10, 0, "t.finished_at DESC, t.created_at DESC")
	if err != nil {
		h.internalError(w, r, err)
		return
	}

	taskItems := []any{}
	for _, task := range latestTasks {
		taskItems = append(taskItems, serializeTask(task))
	}
	userItems := []any{}
	for _, user := range latestUsers {
		userItems = append(userItems, h.svc.serializeUser(user))
	}
	failureItems := []any{}
	for _, task := range recentFailures {
		failureItems = append(failureItems, serializeFailureLog(task))
	}
	httpapi.OK(w, map[string]any{
		"today":           today,
		"totals":          totals,
		"latest_tasks":    taskItems,
		"latest_users":    userItems,
		"recent_failures": failureItems,
	})
}

func (h *Handlers) userList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()
	filter := userFilter{
		Keyword: strings.TrimSpace(query.Get("keyword")),
		Status:  query.Get("status"),
		Role:    query.Get("role"),
	}
	total, err := h.svc.countUsers(ctx, filter)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	pager := newPagination(query, total)
	users, err := h.svc.listUsers(ctx, filter, pager.PerPage, pager.Offset())
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	items := []any{}
	for _, user := range users {
		if user.Quota == nil {
			account, err := h.svc.ensureQuota(ctx, user.ID)
			if err != nil {
				h.internalError(w, r, err)
				return
			}
			user.Quota = account
		}
		items = append(items, h.svc.serializeUser(user))
	}
	httpapi.OK(w, map[string]any{"items": items, "pagination": pager.JSON()})
}

func (h *Handlers) userDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	exists, err := h.svc.userExists(ctx, id)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if !exists {
		httpapi.NotFound(w, httpapi.CodeConsoleNotFound, "用户不存在")
		return
	}
	if _, err := h.svc.ensureQuota(ctx, id); err != nil {
		h.internalError(w, r, err)
		return
	}
	user, found, err := h.svc.userByID(ctx, id)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if !found {
		httpapi.NotFound(w, httpapi.CodeConsoleNotFound, "用户不存在")
		return
	}
	profile, err := h.svc.loadProfile(ctx, id)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	user.Profile = profile
	httpapi.OK(w, h.svc.serializeUserDetail(user))
}

func (h *Handlers) userQuotaUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	exists, err := h.svc.userExists(ctx, id)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if !exists {
		httpapi.NotFound(w, httpapi.CodeConsoleNotFound, "用户不存在")
		return
	}

	body, ok := h.payload(w, r)
	if !ok {
		return
	}
	dailyRaw, dailyPresent := body["daily_quota"]
	availableRaw, availablePresent := body["available_quota"]
	if (!dailyPresent || dailyRaw == nil) && (!availablePresent || availableRaw == nil) {
		httpapi.BadRequest(w, "至少传入 daily_quota 或 available_quota")
		return
	}

	var dailyQuota, availableQuota *int
	if dailyPresent && dailyRaw != nil {
		parsed, err := pythonInt(dailyRaw)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, httpapi.CodeConflict, err.Error())
			return
		}
		dailyQuota = &parsed
	}
	if availablePresent && availableRaw != nil {
		parsed, err := pythonInt(availableRaw)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, httpapi.CodeConflict, err.Error())
			return
		}
		availableQuota = &parsed
	}

	remark := ""
	if raw, present := body["remark"]; present {
		if text, isString := raw.(string); isString {
			remark = strings.TrimSpace(text)
		}
	}
	username := ""
	if operator := httpapi.UserOf(r); operator != nil {
		username = operator.Username()
	}
	if remark == "" {
		remark = "后台修改免费次数"
	}
	adminRemark := fmt.Sprintf("[管理员:%s] %s", username, remark)

	account, err := h.svc.adminAdjustQuota(ctx, id, dailyQuota, availableQuota, adminRemark)
	if err != nil && quotaValidationErrors[err.Error()] {
		httpapi.Error(w, http.StatusBadRequest, httpapi.CodeConflict, err.Error())
		return
	}
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	var lastReset any
	if account.LastResetDate != "" {
		lastReset = account.LastResetDate
	}
	httpapi.OK(w, map[string]any{
		"user_id":               id,
		"daily_quota":           account.DailyQuota,
		"available_quota":       account.AvailableQuota,
		"frozen_quota":          account.FrozenQuota,
		"total_generated_count": account.TotalGeneratedCount,
		"last_reset_date":       lastReset,
	})
}

func (h *Handlers) taskList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query()
	filter := taskFilter{
		Keyword: strings.TrimSpace(query.Get("keyword")),
		Status:  query.Get("status"),
	}
	total, err := h.svc.countTasks(ctx, filter)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	pager := newPagination(query, total)
	tasks, err := h.svc.listTasks(ctx, filter, pager.PerPage, pager.Offset(), "t.created_at DESC")
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	items := []any{}
	for _, task := range tasks {
		items = append(items, serializeTask(task))
	}
	httpapi.OK(w, map[string]any{"items": items, "pagination": pager.JSON()})
}

func (h *Handlers) configCollection(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.site.List(r.URL.Query().Get("category"))
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	items := []any{}
	for _, row := range rows {
		items = append(items, serializeConfig(row))
	}
	categories := []any{}
	for _, item := range siteconfig.CategoryLabels {
		categories = append(categories, map[string]any{"code": item.Code, "label": item.Label})
	}
	httpapi.OK(w, map[string]any{"items": items, "categories": categories})
}

func (h *Handlers) configDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		h.configRead(w, r)
		return
	}
	h.configWrite(w, r)
}

func (h *Handlers) configRead(w http.ResponseWriter, r *http.Request) {
	row, found, err := h.svc.site.Get(r.PathValue("key"))
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if !found {
		httpapi.NotFound(w, httpapi.CodeConsoleNotFound, "配置项不存在")
		return
	}
	httpapi.OK(w, serializeConfig(row))
}

func (h *Handlers) configWrite(w http.ResponseWriter, r *http.Request) {
	body, ok := h.payload(w, r)
	if !ok {
		return
	}
	value, present := body["value"]
	if !present || value == nil {
		httpapi.BadRequest(w, "缺少 value")
		return
	}
	row, err := h.svc.site.Set(
		r.PathValue("key"),
		pythonStr(value),
		optionalBool(body, "is_secret"),
		optionalString(body, "description"),
		optionalString(body, "category"),
	)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	httpapi.OK(w, map[string]any{"key": row.Key, "updated_at": isoOf(row.UpdatedAt)})
}

func (h *Handlers) configBatch(w http.ResponseWriter, r *http.Request) {
	body, ok := h.payload(w, r)
	if !ok {
		return
	}
	items, isList := body["items"].([]any)
	if !isList || len(items) == 0 {
		httpapi.BadRequest(w, "缺少 items 数组")
		return
	}
	updated := 0
	for _, raw := range items {
		item, isObject := raw.(map[string]any)
		if !isObject {
			httpapi.BadRequest(w, "配置项必须是对象")
			return
		}
		keyValue, hasKey := item["key"]
		value, hasValue := item["value"]
		key, keyIsString := keyValue.(string)
		if !hasKey || !hasValue || !keyIsString {
			httpapi.BadRequest(w, "配置项缺少 key 或 value")
			return
		}
		if value == nil {
			// The single-key route rejects a null value the same way; a nil
			// stringifies to the literal "None" and would be persisted.
			httpapi.BadRequest(w, "缺少 value")
			return
		}
		if _, err := h.svc.site.Set(
			key,
			pythonStr(value),
			optionalBool(item, "is_secret"),
			optionalString(item, "description"),
			optionalString(item, "category"),
		); err != nil {
			h.internalError(w, r, err)
			return
		}
		updated++
	}
	httpapi.OK(w, map[string]any{"updated": updated})
}

func (h *Handlers) channelCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		h.channelList(w, r)
		return
	}
	h.channelCreate(w, r)
}

func (h *Handlers) channelList(w http.ResponseWriter, r *http.Request) {
	channels, err := h.svc.listChannels(r.Context())
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	items := []any{}
	for _, channel := range channels {
		items = append(items, serializeChannel(channel))
	}
	httpapi.OK(w, items)
}

func (h *Handlers) channelCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body, ok := h.payload(w, r)
	if !ok {
		return
	}
	for _, field := range []string{"name", "channel_code", "base_url", "model_name"} {
		if !truthy(body[field]) {
			httpapi.BadRequest(w, fmt.Sprintf("缺少 %s", field))
			return
		}
	}
	if channelType, present := body["channel_type"]; present && !validChannelType(channelType) {
		// The contract rejects any channel_type other than image/llm with 400;
		// a null or empty value used to be persisted as "None"/"" instead, which
		// silently removed the channel from dispatch.
		httpapi.BadRequest(w, "不支持的模型类型")
		return
	}
	code := pythonStr(body["channel_code"])
	exists, err := h.svc.channelCodeExists(ctx, code)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if exists {
		httpapi.Conflict(w, "渠道编码已存在")
		return
	}

	channel := channelRow{
		Name:           pythonStr(body["name"]),
		ChannelCode:    code,
		ChannelType:    "image",
		BaseURL:        pythonStr(body["base_url"]),
		APIPath:        "/images/generations",
		ModelName:      pythonStr(body["model_name"]),
		TimeoutSeconds: 60,
		MaxRetries:     3,
		IsActive:       true,
		Priority:       1,
		ProviderVendor: "openai",
	}
	if value, present := body["channel_type"]; present {
		channel.ChannelType = pythonStr(value)
	}
	if value, present := body["provider_name"]; present {
		channel.ProviderName = pythonStr(value)
	}
	if value, present := body["api_path"]; present {
		channel.APIPath = pythonStr(value)
	}
	if value, present := body["edit_api_path"]; present {
		channel.EditAPIPath = pythonStr(value)
	}
	if value, present := body["timeout_seconds"]; present {
		parsed, err := pythonInt(value)
		if err != nil {
			// A payload whose numbers do not parse is a client error, the same
			// as every other malformed-value branch in this API.
			httpapi.BadRequest(w, err.Error())
			return
		}
		channel.TimeoutSeconds = parsed
	}
	if value, present := body["max_retries"]; present {
		parsed, err := pythonInt(value)
		if err != nil {
			// A payload whose numbers do not parse is a client error, the same
			// as every other malformed-value branch in this API.
			httpapi.BadRequest(w, err.Error())
			return
		}
		channel.MaxRetries = parsed
	}
	if value, present := body["is_active"]; present {
		channel.IsActive = truthy(value)
	}
	if value, present := body["priority"]; present {
		parsed, err := pythonInt(value)
		if err != nil {
			// A payload whose numbers do not parse is a client error, the same
			// as every other malformed-value branch in this API.
			httpapi.BadRequest(w, err.Error())
			return
		}
		channel.Priority = parsed
	}
	if value := body["api_key"]; truthy(value) {
		encrypted, err := h.svc.encryptSecret(pythonStr(value))
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		channel.APIKey = encrypted
	}

	created, err := h.svc.insertChannel(ctx, channel)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	httpapi.Success(w, http.StatusCreated, serializeChannel(created), "ok")
}

func validChannelType(value any) bool {
	text, isString := value.(string)
	if !isString {
		return false
	}
	return text == "image" || text == "llm"
}

func (h *Handlers) channelDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	channel, found, err := h.svc.channelByID(ctx, id)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if !found {
		httpapi.NotFound(w, httpapi.CodeConsoleNotFound, "渠道不存在")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		httpapi.OK(w, serializeChannel(channel))
	case http.MethodPut:
		h.channelUpdate(w, r, channel)
	case http.MethodDelete:
		h.channelDelete(w, r, channel)
	}
}

func (h *Handlers) channelUpdate(w http.ResponseWriter, r *http.Request, channel channelRow) {
	body, ok := h.payload(w, r)
	if !ok {
		return
	}
	if channelType, present := body["channel_type"]; present && !validChannelType(channelType) {
		httpapi.BadRequest(w, "不支持的模型类型")
		return
	}
	for _, field := range []string{"name", "channel_type", "provider_name", "base_url", "api_path", "edit_api_path", "model_name"} {
		value, present := body[field]
		if !present {
			continue
		}
		text := pythonStr(value)
		switch field {
		case "name":
			channel.Name = text
		case "channel_type":
			channel.ChannelType = text
		case "provider_name":
			channel.ProviderName = text
		case "base_url":
			channel.BaseURL = text
		case "api_path":
			channel.APIPath = text
		case "edit_api_path":
			channel.EditAPIPath = text
		case "model_name":
			channel.ModelName = text
		}
	}
	if value, present := body["is_active"]; present {
		channel.IsActive = truthy(value)
	}
	if value, present := body["priority"]; present {
		parsed, err := pythonInt(value)
		if err != nil {
			// A payload whose numbers do not parse is a client error, the same
			// as every other malformed-value branch in this API.
			httpapi.BadRequest(w, err.Error())
			return
		}
		channel.Priority = parsed
	}
	if value, present := body["timeout_seconds"]; present {
		parsed, err := pythonInt(value)
		if err != nil {
			// A payload whose numbers do not parse is a client error, the same
			// as every other malformed-value branch in this API.
			httpapi.BadRequest(w, err.Error())
			return
		}
		channel.TimeoutSeconds = parsed
	}
	if value, present := body["max_retries"]; present {
		parsed, err := pythonInt(value)
		if err != nil {
			// A payload whose numbers do not parse is a client error, the same
			// as every other malformed-value branch in this API.
			httpapi.BadRequest(w, err.Error())
			return
		}
		channel.MaxRetries = parsed
	}
	if value := body["api_key"]; truthy(value) {
		encrypted, err := h.svc.encryptSecret(pythonStr(value))
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		channel.APIKey = encrypted
	}

	updated, err := h.svc.updateChannel(r.Context(), channel)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	httpapi.OK(w, serializeChannel(updated))
}

func (h *Handlers) channelDelete(w http.ResponseWriter, r *http.Request, channel channelRow) {
	ctx := r.Context()
	hasTasks, err := h.svc.channelHasTasks(ctx, channel.ID)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	if hasTasks {
		httpapi.Error(w, http.StatusBadRequest, httpapi.CodeStateInvalid, "该渠道有关联任务，无法删除，请先禁用")
		return
	}
	if err := h.svc.deleteChannel(ctx, channel.ID); err != nil {
		h.internalError(w, r, err)
		return
	}
	httpapi.OK(w, map[string]any{"deleted": true})
}

// pagination mirrors django.core.paginator.Paginator.get_page: the page number
// is clamped to the last page and per_page to 1..100.
type pagination struct {
	Page       int
	PerPage    int
	Total      int
	TotalPages int
}

func newPagination(query url.Values, total int) pagination {
	page := queryInt(query, "page", 1)
	if page < 1 {
		page = 1
	}
	perPage := queryInt(query, "per_page", 20)
	if perPage < 1 {
		perPage = 1
	}
	if perPage > 100 {
		perPage = 100
	}
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	return pagination{Page: page, PerPage: perPage, Total: total, TotalPages: totalPages}
}

func (p pagination) Offset() int { return (p.Page - 1) * p.PerPage }

func (p pagination) JSON() map[string]any {
	return map[string]any{
		"page":         p.Page,
		"per_page":     p.PerPage,
		"total":        p.Total,
		"total_pages":  p.TotalPages,
		"has_next":     p.Page < p.TotalPages,
		"has_previous": p.Page > 1,
	}
}

func queryInt(query url.Values, key string, fallback int) int {
	raw := strings.TrimSpace(query.Get(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			if strings.HasPrefix(raw, "-") {
				return math.MinInt32
			}
			return math.MaxInt32
		}
		return fallback
	}
	if parsed > math.MaxInt32 {
		return math.MaxInt32
	}
	if parsed < math.MinInt32 {
		return math.MinInt32
	}
	return int(parsed)
}

// pythonInt reproduces python's int() conversion, including its error message.
func pythonInt(value any) (int, error) {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed >= math.MaxInt64 || typed < math.MinInt64 {
			return 0, fmt.Errorf("cannot convert %v to integer", typed)
		}
		return int(typed), nil
	case json.Number:
		return pythonInt(typed.String())
	case bool:
		if typed {
			return 1, nil
		}
		return 0, nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, fmt.Errorf("invalid literal for int() with base 10: '%s'", typed)
		}
		return parsed, nil
	case nil:
		return 0, errors.New("int() argument must be a string, a bytes-like object or a real number, not 'NoneType'")
	}
	return 0, fmt.Errorf("int() argument must be a string, a bytes-like object or a real number, not '%s'", pythonTypeName(value))
}

// pythonStr reproduces python's str() for the JSON scalars the console accepts.
func pythonStr(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return "None"
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case float64:
		return formatPythonNumber(typed)
	case json.Number:
		return typed.String()
	}
	return fmt.Sprintf("%v", value)
}

func formatPythonNumber(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return strconv.FormatFloat(value, 'g', -1, 64)
	}
	if value == math.Trunc(value) && math.Abs(value) < 1e16 {
		return strconv.FormatFloat(value, 'f', -1, 64)
	}
	formatted := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(formatted, ".eE") {
		formatted += ".0"
	}
	return formatted
}

func pythonTypeName(value any) string {
	switch value.(type) {
	case nil:
		return "NoneType"
	case bool:
		return "bool"
	case float64, int, int64:
		return "float"
	case string:
		return "str"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	}
	return fmt.Sprintf("%T", value)
}

func optionalBool(body map[string]any, key string) *bool {
	value, present := body[key]
	if !present || value == nil {
		return nil
	}
	result := truthy(value)
	return &result
}

func optionalString(body map[string]any, key string) *string {
	value, present := body[key]
	if !present || value == nil {
		return nil
	}
	result := pythonStr(value)
	return &result
}

// truthy mirrors python truthiness for JSON values.
func truthy(value any) bool {
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
		return typed.String() != "" && typed.String() != "0"
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	}
	return true
}
