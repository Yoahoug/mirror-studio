package generator

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"mirror-studio/internal/db"
	"mirror-studio/internal/httpapi"
)

// The chat proxy lives in the generator package because the LLM channels are
// generator rows (`generator_imagechannel` with channel_type=llm) and the keys
// are decrypted here. The reference serves it from apps/core, which owns no rows
// of its own in the rewrite.

// Register chat routes. Both endpoints are login-required API views.
func (h *Handlers) registerChat(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/chat/models", h.chatModels)
	mux.HandleFunc("POST /api/chat", h.chat)
}

// llmChannels mirrors ChatModelListAPIView's queryset: active LLM channels that
// actually carry a key, ordered by (priority, id).
func (s *Service) llmChannels(ctx context.Context, channelCode string) ([]*Channel, error) {
	query := `SELECT ` + channelColumns + ` FROM generator_imagechannel
		WHERE channel_type = ? AND is_active = ? AND api_key_encrypted <> ''`
	args := []any{ChannelTypeLLM, db.Bool(true)}
	if channelCode != "" {
		query += ` AND channel_code = ?`
		args = append(args, channelCode)
	}
	query += ` ORDER BY priority, id`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := []*Channel{}
	for rows.Next() {
		channel, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return channels, nil
}

// chatModelView is _serialize_chat_model. `configured` reports whether a key is
// actually present, which for an encrypted column means it decrypts.
func (s *Service) chatModelView(channel *Channel) map[string]any {
	configured := false
	if apiKey, err := channel.APIKey(s.cipher); err == nil && apiKey != "" {
		configured = true
	}
	return map[string]any{
		"id":            channel.ID,
		"name":          channel.Name,
		"channel_code":  channel.ChannelCode,
		"provider_name": channel.ProviderName,
		"model_name":    channel.ModelName,
		"configured":    configured,
	}
}

func (h *Handlers) chatModels(w http.ResponseWriter, r *http.Request) {
	if h.loginRequired(w, r) {
		return
	}
	channels, err := h.service.llmChannels(r.Context(), "")
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	items := make([]any, 0, len(channels))
	for _, channel := range channels {
		items = append(items, h.service.chatModelView(channel))
	}
	httpapi.OK(w, items)
}

func (h *Handlers) chat(w http.ResponseWriter, r *http.Request) {
	if h.loginRequired(w, r) {
		return
	}
	payload, err := httpapi.ParsePayload(r)
	if err != nil {
		h.writeError(w, r, payloadErrorFromGo(err))
		return
	}
	prompt := strings.TrimSpace(stringValue(payload["prompt"]))
	if prompt == "" {
		httpapi.BadRequest(w, "请输入对话内容")
		return
	}

	channelCode := strings.TrimSpace(stringValue(payload["channel_code"]))
	if channelCode == "" {
		channelCode = strings.TrimSpace(stringValue(payload["model"]))
	}
	channels, err := h.service.llmChannels(r.Context(), channelCode)
	if err != nil {
		h.writeInternalError(w, r, err)
		return
	}
	if len(channels) == 0 {
		httpapi.Error(w, http.StatusServiceUnavailable, httpapi.CodeNoProvider, "未配置可用对话模型")
		return
	}
	channel := channels[0]

	apiKey, err := channel.APIKey(h.service.cipher)
	if err != nil || apiKey == "" {
		httpapi.Error(w, http.StatusServiceUnavailable, httpapi.CodeNoProvider, "对话模型缺少 API Key")
		return
	}

	systemPrompt := stringValue(payload["system_prompt"])
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = "你是绘镜的创作助手，回答应简洁、专业、友好。"
	}
	messages := []ChatMessage{{Role: "system", Content: systemPrompt}}
	history := payload["history"]
	if list, ok := history.([]any); ok {
		if len(list) > 12 {
			list = list[len(list)-12:]
		}
		for _, item := range list {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			content := chatMessageText(entry)
			if content == "" {
				continue
			}
			role := "user"
			if stringValue(entry["role"]) == "model" {
				role = "assistant"
			}
			messages = append(messages, ChatMessage{Role: role, Content: content})
		}
	}
	messages = append(messages, ChatMessage{Role: "user", Content: prompt})

	temperature := floatValue(payload["temperature"], 0.7)
	reply, err := h.service.ChatCompletion(r.Context(), channel, messages, temperature)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, httpapi.CodeUpstreamFailure, "对话 LLM 响应失败")
		return
	}
	if reply == "" {
		httpapi.Error(w, http.StatusBadGateway, httpapi.CodeUpstreamFailure, "对话 LLM 未返回内容")
		return
	}
	httpapi.OK(w, map[string]any{"reply": reply, "model": h.service.chatModelView(channel)})
}

// chatMessageText is _message_text: prefer `content`, fall back to joining the
// `parts[].text` entries.
func chatMessageText(item map[string]any) string {
	if content := strings.TrimSpace(stringValue(item["content"])); content != "" {
		return content
	}
	parts, ok := item["parts"].([]any)
	if !ok {
		return ""
	}
	texts := []string{}
	for _, part := range parts {
		entry, ok := part.(map[string]any)
		if !ok {
			continue
		}
		texts = append(texts, stringValue(entry["text"]))
	}
	return strings.TrimSpace(strings.Join(texts, "\n"))
}

// floatValue is _payload_float: a value that is not a number falls back to the
// default instead of failing the request.
func floatValue(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			return parsed
		}
	}
	return fallback
}
