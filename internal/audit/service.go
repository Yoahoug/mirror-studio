// Package audit owns the callback event log the console's failure list reads.
// The reference's AdminOperationLog is deliberately not ported (review P1-35:
// it had zero callers).
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"mirror-studio/internal/db"
)

// Service reads and writes audit_callbackeventlog.
type Service struct {
	db     *db.DB
	logger *slog.Logger
}

// NewService wires an audit service onto the shared database.
func NewService(handle *db.DB, log *slog.Logger) *Service {
	return &Service{db: handle, logger: log}
}

// LogCallbackEvent writes one callback event row. A failed write is logged at
// error level and never propagates: the audit trail must not break the flow it
// observes.
func (s *Service) LogCallbackEvent(ctx context.Context, eventType, source string, payload map[string]any, success bool, remark string) error {
	now := db.FormatTime(time.Now())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_callbackeventlog (created_at, updated_at, event_type, source, payload_json, success, remark)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		now, now, eventType, source, marshalPayload(payload), db.Bool(success), remark,
	)
	if err != nil && s.logger != nil {
		s.logger.Error("callback event log write failed", "event_type", eventType, "source", source, "error", err)
	}
	return nil
}

// RecentFailures returns failed callback events, newest first, shaped for the
// console failure list.
func (s *Service) RecentFailures(ctx context.Context, limit int) ([]map[string]any, error) {
	result := []map[string]any{}
	if limit <= 0 {
		return result, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, event_type, source, success, remark, payload_json, created_at
		 FROM audit_callbackeventlog
		 WHERE success = 0
		 ORDER BY created_at DESC
		 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id          int64
			eventType   string
			source      string
			success     int
			remark      string
			payloadText sql.NullString
			createdAt   sql.NullString
		)
		if err := rows.Scan(&id, &eventType, &source, &success, &remark, &payloadText, &createdAt); err != nil {
			return nil, err
		}
		payload := decodePayload(payloadText.String)
		result = append(result, map[string]any{
			"id":                  id,
			"event_type":          eventType,
			"source":              source,
			"success":             success != 0,
			"remark":              remark,
			"task_no":             payloadValue(payload, "task_no"),
			"error_code":          payloadValue(payload, "error_code"),
			"error_message":       payloadValue(payload, "error_message"),
			"provider_request_id": payloadValue(payload, "provider_request_id"),
			"payload_json":        displayPayload(payload),
			"created_at":          isoformat(createdAt.String),
		})
	}
	return result, rows.Err()
}

func decodePayload(text string) any {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return map[string]any{}
	}
	return decoded
}

func payloadValue(payload any, key string) any {
	if object, ok := payload.(map[string]any); ok {
		if value, present := object[key]; present {
			return value
		}
	}
	return ""
}

// displayPayload is the reference's `payload.get("raw_response") or payload`.
func displayPayload(payload any) any {
	object, ok := payload.(map[string]any)
	if !ok {
		return payload
	}
	if raw, present := object["raw_response"]; present && pythonTruthy(raw) {
		return raw
	}
	return object
}

func pythonTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func isoformat(value string) any {
	parsed, ok := db.ParseTime(value)
	if !ok {
		return nil
	}
	parsed = parsed.UTC()
	if parsed.Nanosecond() == 0 {
		return parsed.Format("2006-01-02T15:04:05+00:00")
	}
	return parsed.Format("2006-01-02T15:04:05.000000+00:00")
}
