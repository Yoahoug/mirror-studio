// Package httpapi holds the transport layer: the response envelope, the
// middleware chain, sessions, CSRF and the router. Handlers never build SQL and
// stores never build HTTP responses.
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Business codes. These are part of the public contract and must not change.
const (
	CodeOK              = 0
	CodeBadRequest      = 400001
	CodeConflict        = 400002
	CodeStateInvalid    = 400003
	CodeValidation      = 400100
	CodeFileMissing     = 400101
	CodeEPUBInvalid     = 400102
	CodePlotMissing     = 400103
	CodePermission      = 400403
	CodeNotDownloadable = 400404
	CodeRateLimited     = 400429
	CodeInternal        = 400500
	CodeKeyMissing      = 400501
	// 400502 is only raised by the chat proxy, which answers 502 for an upstream
	// failure or an empty reply.
	CodeUpstreamFailure = 400502
	CodeNoProvider      = 400503

	CodeConsoleUnauthorized = 401000
	CodeConsoleForbidden    = 403000
	CodeConsoleInactive     = 403001
	CodeConsoleNotFound     = 404000
	CodeConsoleRateLimited  = 429000

	CodeEmailNotConfigured = 500001
	CodeEmailSendFailed    = 500002
)

// Envelope is the response body every JSON endpoint returns.
type Envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// Success writes `{"code":0,"message":msg,"data":data}` with the given status.
func Success(w http.ResponseWriter, status int, data any, message string) {
	writeJSON(w, status, Envelope{Code: CodeOK, Message: message, Data: data})
}

// OK writes a 200 success envelope with the standard "ok" message.
func OK(w http.ResponseWriter, data any) { Success(w, http.StatusOK, data, "ok") }

// Error writes a failure envelope. `data` is always null, matching the
// reference implementation.
func Error(w http.ResponseWriter, status, code int, message string) {
	writeJSON(w, status, Envelope{Code: code, Message: message, Data: nil})
}

// BadRequest is the most common failure: HTTP 400 with code 400001.
func BadRequest(w http.ResponseWriter, message string) {
	Error(w, http.StatusBadRequest, CodeBadRequest, message)
}

// Forbidden writes an HTTP 403 envelope.
func Forbidden(w http.ResponseWriter, code int, message string) {
	Error(w, http.StatusForbidden, code, message)
}

// NotFound writes an HTTP 404 envelope.
func NotFound(w http.ResponseWriter, code int, message string) {
	Error(w, http.StatusNotFound, code, message)
}

// Conflict writes an HTTP 400 envelope with the conflict business code.
func Conflict(w http.ResponseWriter, message string) {
	Error(w, http.StatusBadRequest, CodeConflict, message)
}

func writeJSON(w http.ResponseWriter, status int, payload Envelope) {
	body, err := marshalEnvelope(payload)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":500001,"message":"响应序列化失败","data":null}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func marshalEnvelope(payload Envelope) ([]byte, error) {
	buffer := &strings.Builder{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, err
	}
	// json.Encoder appends a newline; the reference writes no trailing newline.
	return []byte(strings.TrimRight(buffer.String(), "\n")), nil
}

// FieldError is one entry of a Django form error list.
type FieldError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// FormErrors keeps field order, which the SPA relies on when it picks the first
// field of the parsed payload. The first field key wins in the UI, so the order
// in which fields are added here matters and must mirror the form declaration
// order of the reference implementation.
type FormErrors struct {
	order  []string
	fields map[string][]FieldError
}

// NewFormErrors creates an empty error set.
func NewFormErrors() *FormErrors {
	return &FormErrors{fields: map[string][]FieldError{}}
}

// Add appends an error under a field name. An empty name maps to `__all__`,
// which is where Django puts non-field errors.
func (f *FormErrors) Add(field, message, code string) {
	if field == "" {
		field = "__all__"
	}
	if _, seen := f.fields[field]; !seen {
		f.order = append(f.order, field)
	}
	f.fields[field] = append(f.fields[field], FieldError{Message: message, Code: code})
}

// Empty reports whether any error was recorded.
func (f *FormErrors) Empty() bool { return len(f.order) == 0 }

// First returns the first message in declaration order, falling back to the
// generic copy the reference uses when a form produced errors without messages.
func (f *FormErrors) First() string {
	for _, field := range f.order {
		for _, item := range f.fields[field] {
			return item.Message
		}
	}
	return "请求参数有误"
}

// AsJSON renders the errors exactly like django.forms.Form.errors.as_json():
// a JSON object serialised with ensure_ascii=True and ", " / ": " separators,
// which the SPA parses out of the `message` field.
func (f *FormErrors) AsJSON() string {
	parts := make([]string, 0, len(f.order))
	for _, field := range f.order {
		items := f.fields[field]
		entries := make([]string, 0, len(items))
		for _, item := range items {
			entries = append(entries, fmt.Sprintf(
				`{"message": "%s", "code": "%s"}`,
				escapeASCIIJSON(item.Message),
				escapeASCIIJSON(item.Code),
			))
		}
		parts = append(parts, fmt.Sprintf(`"%s": [%s]`, escapeASCIIJSON(field), strings.Join(entries, ", ")))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// escapeASCIIJSON reproduces python's json.dumps(ensure_ascii=True) string
// escaping: non-ASCII runes become \uXXXX escapes, and the characters Python
// escapes by default are escaped here too.
func escapeASCIIJSON(value string) string {
	builder := &strings.Builder{}
	for _, char := range value {
		switch char {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		case '\b':
			builder.WriteString(`\b`)
		case '\f':
			builder.WriteString(`\f`)
		default:
			if char < 0x20 {
				builder.WriteString(fmt.Sprintf(`\u%04x`, char))
				continue
			}
			if char < 0x7f {
				builder.WriteRune(char)
				continue
			}
			if char > 0xffff {
				// Python emits a surrogate pair for astral characters.
				high, low := utf16SurrogatePair(char)
				builder.WriteString(fmt.Sprintf(`\u%04x\u%04x`, high, low))
				continue
			}
			builder.WriteString(fmt.Sprintf(`\u%04x`, char))
		}
	}
	return builder.String()
}

func utf16SurrogatePair(char rune) (uint16, uint16) {
	value := char - 0x10000
	return uint16(0xd800 + (value >> 10)), uint16(0xdc00 + (value & 0x3ff))
}
