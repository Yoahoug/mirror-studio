package console

import (
	"net/http"
	"testing"
	"time"
)

// Malformed channel payloads are client errors. A channel_type outside
// image/llm was previously accepted whenever it was falsy (null, "", 0) and
// persisted as the literal "None", which silently removed the channel from
// dispatch; unparsable numbers answered 500 instead of 400.
func TestChannelPayloadValidation(t *testing.T) {
	h := newHarness(t)
	h.seedUser(1, "operator", "admin", "active", true, time.Now().UTC())
	h.login(1)

	base := map[string]any{
		"name": "渠道", "channel_code": "validate-1",
		"base_url": "https://images.example.com/v1", "model_name": "m",
	}
	cases := []struct {
		name    string
		field   string
		value   any
		status  int
		code    int
		message string
	}{
		{"unknown channel_type", "channel_type", "video", http.StatusBadRequest, 400001, "不支持的模型类型"},
		{"null channel_type", "channel_type", nil, http.StatusBadRequest, 400001, "不支持的模型类型"},
		{"empty channel_type", "channel_type", "", http.StatusBadRequest, 400001, "不支持的模型类型"},
		{"unparsable timeout", "timeout_seconds", "abc", http.StatusBadRequest, 400001, "invalid literal for int() with base 10: 'abc'"},
	}
	for _, testCase := range cases {
		body := map[string]any{}
		for key, value := range base {
			body[key] = value
		}
		body[testCase.field] = testCase.value
		recorder := h.request(http.MethodPost, "/console/api/image-channels/", body)
		if recorder.Code != testCase.status {
			t.Fatalf("%s: HTTP %d, want %d", testCase.name, recorder.Code, testCase.status)
		}
		if code, message := h.failure(recorder); code != testCase.code || message != testCase.message {
			t.Fatalf("%s: code %d message %q, want %d %q", testCase.name, code, message, testCase.code, testCase.message)
		}
	}
}

// The batch config route used to persist the string "None" for a null value and
// answer 500 for a malformed item; the single-key route rejects both as 400.
func TestConfigBatchRejectsMalformedItems(t *testing.T) {
	h := newHarness(t)
	h.seedUser(1, "operator", "admin", "active", true, time.Now().UTC())
	h.login(1)

	cases := []struct {
		name    string
		items   []any
		message string
	}{
		{"non-object item", []any{"site.name"}, "配置项必须是对象"},
		{"missing value", []any{map[string]any{"key": "site.name"}}, "配置项缺少 key 或 value"},
		{"null value", []any{map[string]any{"key": "site.name", "value": nil}}, "缺少 value"},
	}
	for _, testCase := range cases {
		recorder := h.request(http.MethodPut, "/console/api/config/batch/", map[string]any{"items": testCase.items})
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s: HTTP %d, want 400", testCase.name, recorder.Code)
		}
		if code, message := h.failure(recorder); code != 400001 || message != testCase.message {
			t.Fatalf("%s: code %d message %q, want 400001 %q", testCase.name, code, message, testCase.message)
		}
	}
}
