package console

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mirror-studio/internal/db"
	"mirror-studio/internal/quota"
	"mirror-studio/internal/siteconfig"
)

// isoFormat reproduces python's datetime.isoformat() for a UTC-aware value:
// the microsecond field is omitted when it is zero and always six digits
// otherwise.
func isoFormat(value time.Time) string {
	value = value.UTC()
	base := value.Format("2006-01-02T15:04:05")
	micro := value.Nanosecond() / 1000
	if micro == 0 {
		return base + "+00:00"
	}
	return fmt.Sprintf("%s.%06d+00:00", base, micro)
}

// dateOnly normalises a DATE column: the sqlite driver reports declared DATE
// columns as time.Time, which database/sql renders as an RFC3339 string.
func dateOnly(value string) string {
	if parsed, ok := db.ParseTime(value); ok {
		return parsed.Format("2006-01-02")
	}
	return value
}

func isoOf(raw string) any {
	if parsed, ok := db.ParseTime(raw); ok {
		return isoFormat(parsed)
	}
	return nil
}

func isoOfNull(raw sql.NullString) any {
	if !raw.Valid {
		return nil
	}
	return isoOf(raw.String)
}

func nullString(raw sql.NullString) any {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	return raw.String
}

// decodeJSONValue passes a stored JSON document through as a nested JSON value,
// which is what python does with the JSONField dict.
func decodeJSONValue(raw sql.NullString) any {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw.String))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil
	}
	return decoded
}

func (s *Service) serializeUser(user userRow) map[string]any {
	return map[string]any{
		"id":            user.ID,
		"username":      user.Username,
		"email":         user.Email.String,
		"mobile":        user.Mobile.String,
		"role":          user.Role,
		"status":        user.Status,
		"is_staff":      user.IsStaff,
		"date_joined":   isoOf(user.DateJoined),
		"last_login":    isoOfNull(user.LastLogin),
		"last_login_ip": nullString(user.LastLoginIP),
		"quota":         s.quotaJSON(user.Quota),
	}
}

func (s *Service) serializeUserDetail(user userRow) map[string]any {
	payload := s.serializeUser(user)
	if user.Profile == nil {
		payload["profile"] = nil
		return payload
	}
	payload["profile"] = map[string]any{
		"nickname":        user.Profile.Nickname,
		"avatar":          user.Profile.Avatar,
		"register_ip":     nullString(user.Profile.RegisterIP),
		"register_source": user.Profile.RegisterSource,
		"remark":          user.Profile.Remark,
	}
	return payload
}

func (s *Service) quotaJSON(account *quota.Account) any {
	if account == nil {
		return nil
	}
	var lastReset any
	if account.LastResetDate != "" {
		lastReset = account.LastResetDate
	}
	return map[string]any{
		"daily_quota":           account.DailyQuota,
		"available_quota":       account.AvailableQuota,
		"frozen_quota":          account.FrozenQuota,
		"total_generated_count": account.TotalGeneratedCount,
		"last_reset_date":       lastReset,
	}
}

func serializeTask(task taskRow) map[string]any {
	return map[string]any{
		"id":                     task.ID,
		"task_no":                task.TaskNo,
		"user_id":                task.UserID,
		"username":               task.Username.String,
		"status":                 task.Status,
		"prompt":                 task.Prompt,
		"style_code":             task.StyleCode,
		"size_code":              task.SizeCode,
		"quality_code":           task.QualityCode,
		"channel_name":           task.ChannelName.String,
		"quota_cost":             task.QuotaCost,
		"error_code":             task.ErrorCode,
		"error_message":          task.ErrorMessage,
		"provider_request_id":    task.ProviderRequestID,
		"provider_response_json": decodeJSONValue(task.ProviderResponse),
		"created_at":             isoOf(task.CreatedAt),
		"finished_at":            isoOfNull(task.FinishedAt),
	}
}

func serializeFailureLog(task taskRow) map[string]any {
	payload := decodeJSONValue(task.ProviderResponse)
	logged := payload
	if document, ok := payload.(map[string]any); ok {
		if failure := document["failure"]; truthy(failure) {
			logged = failure
		}
	}
	created := isoOf(task.CreatedAt)
	if finished := isoOfNull(task.FinishedAt); finished != nil {
		created = finished
	}
	return map[string]any{
		"id":                  task.ID,
		"event_type":          "generation_failed",
		"source":              channelSource(task),
		"success":             false,
		"remark":              task.ErrorMessage,
		"task_no":             task.TaskNo,
		"error_code":          task.ErrorCode,
		"error_message":       task.ErrorMessage,
		"provider_request_id": task.ProviderRequestID,
		"payload_json":        logged,
		"created_at":          created,
	}
}

func channelSource(task taskRow) string {
	if task.ChannelCode.String != "" {
		return task.ChannelCode.String
	}
	return "generator"
}

func serializeConfig(row siteconfig.Row) map[string]any {
	return map[string]any{
		"id":          row.ID,
		"key":         row.Key,
		"value":       row.Masked(),
		"is_secret":   row.IsSecret,
		"description": row.Description,
		"category":    row.Category,
		"updated_at":  isoOf(row.UpdatedAt),
	}
}

func serializeChannel(channel channelRow) map[string]any {
	return map[string]any{
		"id":              channel.ID,
		"name":            channel.Name,
		"channel_code":    channel.ChannelCode,
		"channel_type":    channel.ChannelType,
		"provider_name":   channel.ProviderName,
		"base_url":        channel.BaseURL,
		"api_path":        channel.APIPath,
		"edit_api_path":   channel.EditAPIPath,
		"model_name":      channel.ModelName,
		"timeout_seconds": channel.TimeoutSeconds,
		"max_retries":     channel.MaxRetries,
		"is_active":       channel.IsActive,
		"priority":        channel.Priority,
		"created_at":      isoOf(channel.CreatedAt),
		"updated_at":      isoOf(channel.UpdatedAt),
	}
}
