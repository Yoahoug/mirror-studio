package audit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"mirror-studio/internal/db"
)

func newTestService(t *testing.T) (*Service, *db.DB) {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "audit.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewService(handle, nil), handle
}

func TestLogCallbackEventStoresDjangoJSONText(t *testing.T) {
	service, handle := newTestService(t)
	ctx := context.Background()

	payload := map[string]any{
		"task_no":             "G20260101000000ABCDEF12",
		"error_code":          "400503",
		"error_message":       "缺少 OpenAI 兼容渠道 API Key",
		"provider_request_id": "",
		"raw_response": map[string]any{
			"exception_type": "ValueError",
			"attempts_used":  1,
		},
	}
	if err := service.LogCallbackEvent(ctx, "generation_failed", "generator", payload, false, "失败"); err != nil {
		t.Fatalf("log callback event: %v", err)
	}

	var (
		eventType string
		source    string
		stored    string
		success   int
		remark    string
	)
	if err := handle.QueryRow(
		`SELECT event_type, source, payload_json, success, remark FROM audit_callbackeventlog`,
	).Scan(&eventType, &source, &stored, &success, &remark); err != nil {
		t.Fatalf("read callback event: %v", err)
	}
	if eventType != "generation_failed" || source != "generator" {
		t.Fatalf("event = (%s, %s)", eventType, source)
	}
	if success != 0 || remark != "失败" {
		t.Fatalf("success/remark = (%d, %q), want (0, 失败)", success, remark)
	}
	want := `{"task_no": "G20260101000000ABCDEF12", "error_code": "400503", "error_message": "\u7f3a\u5c11 OpenAI \u517c\u5bb9\u6e20\u9053 API Key", "provider_request_id": "", "raw_response": {"exception_type": "ValueError", "attempts_used": 1}}`
	if stored != want {
		t.Fatalf("payload_json =\n%s\nwant\n%s", stored, want)
	}

	if err := service.LogCallbackEvent(ctx, "password_change", "users", nil, true, ""); err != nil {
		t.Fatalf("nil payload: %v", err)
	}
	if err := handle.QueryRow(
		`SELECT payload_json FROM audit_callbackeventlog WHERE event_type = 'password_change'`,
	).Scan(&stored); err != nil {
		t.Fatalf("read nil payload row: %v", err)
	}
	if stored != "{}" {
		t.Fatalf("nil payload stored as %s, want {}", stored)
	}
}

func TestRecentFailuresShape(t *testing.T) {
	service, handle := newTestService(t)
	ctx := context.Background()

	older := map[string]any{
		"task_no":             "G20260101000000ABCDEF12",
		"error_code":          "400503",
		"error_message":       "缺少 OpenAI 兼容渠道 API Key",
		"provider_request_id": "req-1",
		"raw_response": map[string]any{
			"exception_type": "ValueError",
			"attempts_used":  1,
		},
	}
	newer := map[string]any{
		"task_no":             "G20260101000001ABCDEF13",
		"error_code":          "400500",
		"error_message":       "生成失败",
		"provider_request_id": "",
	}
	if err := service.LogCallbackEvent(ctx, "generation_failed", "generator", older, false, "缺少 OpenAI 兼容渠道 API Key"); err != nil {
		t.Fatalf("first event: %v", err)
	}
	if err := service.LogCallbackEvent(ctx, "generation_failed", "generator", newer, false, "生成失败"); err != nil {
		t.Fatalf("second event: %v", err)
	}
	if err := service.LogCallbackEvent(ctx, "password_change", "users", map[string]any{"username": "diff-user"}, true, ""); err != nil {
		t.Fatalf("success event: %v", err)
	}
	setCreatedAt(t, handle, "G20260101000000ABCDEF12", time.Date(2026, 1, 1, 10, 0, 0, 123400000, time.UTC))
	setCreatedAt(t, handle, "G20260101000001ABCDEF13", time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC))

	items, err := service.RecentFailures(ctx, 10)
	if err != nil {
		t.Fatalf("recent failures: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("rows = %d, want the two failures only", len(items))
	}

	newest := items[0]
	if newest["success"] != false || newest["event_type"] != "generation_failed" {
		t.Fatalf("newest row = %v", newest)
	}
	if newest["task_no"] != "G20260101000001ABCDEF13" || newest["error_message"] != "生成失败" {
		t.Fatalf("newest payload fields = %v", newest)
	}
	if newest["provider_request_id"] != "" || newest["error_code"] != "400500" {
		t.Fatalf("newest payload fields = %v", newest)
	}
	if newest["created_at"] != "2026-01-01T11:00:00+00:00" {
		t.Fatalf("created_at = %v, want 2026-01-01T11:00:00+00:00", newest["created_at"])
	}
	if _, ok := newest["payload_json"].(map[string]any); !ok {
		t.Fatalf("payload_json = %T, want the payload object", newest["payload_json"])
	}

	oldest := items[1]
	if oldest["created_at"] != "2026-01-01T10:00:00.123400+00:00" {
		t.Fatalf("created_at = %v, want microsecond precision", oldest["created_at"])
	}
	raw, ok := oldest["payload_json"].(map[string]any)
	if !ok {
		t.Fatalf("payload_json = %T, want the raw_response object", oldest["payload_json"])
	}
	if raw["exception_type"] != "ValueError" || raw["attempts_used"] != float64(1) {
		t.Fatalf("payload_json = %v, want raw_response preferred over the payload", raw)
	}

	limited, err := service.RecentFailures(ctx, 1)
	if err != nil {
		t.Fatalf("limited recent failures: %v", err)
	}
	if len(limited) != 1 || limited[0]["task_no"] != "G20260101000001ABCDEF13" {
		t.Fatalf("limit ignored: %v", limited)
	}
	empty, err := service.RecentFailures(ctx, 0)
	if err != nil {
		t.Fatalf("zero limit: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("zero limit returned %d rows", len(empty))
	}
}

func TestLogCallbackEventSwallowsWriteFailure(t *testing.T) {
	handle, err := db.Open(filepath.Join(t.TempDir(), "audit.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	service := NewService(handle, nil)
	handle.Close()

	if err := service.LogCallbackEvent(context.Background(), "password_change", "users", map[string]any{"username": "diff-user"}, true, ""); err != nil {
		t.Fatalf("write failure must be swallowed, got %v", err)
	}
}

func setCreatedAt(t *testing.T, handle *db.DB, taskNo string, createdAt time.Time) {
	t.Helper()
	if _, err := handle.Exec(
		`UPDATE audit_callbackeventlog SET created_at = ? WHERE event_type = 'generation_failed' AND payload_json LIKE ?`,
		db.FormatTime(createdAt), "%"+taskNo+"%",
	); err != nil {
		t.Fatalf("set created_at: %v", err)
	}
}
