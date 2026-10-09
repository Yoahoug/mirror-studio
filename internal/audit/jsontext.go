package audit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// callbackPayloadKeyOrder reproduces the insertion order the reference builds
// its callback payloads in. Go maps have no order, so the marshaler imposes
// this one and falls back to sorted keys for anything else; audit_callbackeventlog
// rows are compared byte for byte against the reference database.
var callbackPayloadKeyOrder = []string{
	"task_no",
	"error_code",
	"error_message",
	"provider_request_id",
	"raw_response",
	"exception_type",
	"attempts_used",
}

// marshalPayload renders payload like Django's JSONField stores it:
// json.dumps(value, ensure_ascii=True) with the default ", " / ": " separators.
func marshalPayload(payload map[string]any) string {
	builder := &strings.Builder{}
	writeJSONValue(builder, payload)
	return builder.String()
}

func writeJSONValue(builder *strings.Builder, value any) {
	switch typed := value.(type) {
	case nil:
		builder.WriteString("null")
	case bool:
		if typed {
			builder.WriteString("true")
		} else {
			builder.WriteString("false")
		}
	case string:
		writeJSONString(builder, typed)
	case int:
		builder.WriteString(strconv.Itoa(typed))
	case int8:
		builder.WriteString(strconv.FormatInt(int64(typed), 10))
	case int16:
		builder.WriteString(strconv.FormatInt(int64(typed), 10))
	case int32:
		builder.WriteString(strconv.FormatInt(int64(typed), 10))
	case int64:
		builder.WriteString(strconv.FormatInt(typed, 10))
	case uint:
		builder.WriteString(strconv.FormatUint(uint64(typed), 10))
	case uint8:
		builder.WriteString(strconv.FormatUint(uint64(typed), 10))
	case uint16:
		builder.WriteString(strconv.FormatUint(uint64(typed), 10))
	case uint32:
		builder.WriteString(strconv.FormatUint(uint64(typed), 10))
	case uint64:
		builder.WriteString(strconv.FormatUint(typed, 10))
	case float32:
		writeJSONFloat(builder, float64(typed))
	case float64:
		writeJSONFloat(builder, typed)
	case []any:
		builder.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				builder.WriteString(", ")
			}
			writeJSONValue(builder, item)
		}
		builder.WriteByte(']')
	case []string:
		builder.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				builder.WriteString(", ")
			}
			writeJSONString(builder, item)
		}
		builder.WriteByte(']')
	case map[string]any:
		keys := orderedPayloadKeys(typed)
		builder.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				builder.WriteString(", ")
			}
			writeJSONString(builder, key)
			builder.WriteString(": ")
			writeJSONValue(builder, typed[key])
		}
		builder.WriteByte('}')
	default:
		writeJSONString(builder, fmt.Sprintf("%v", typed))
	}
}

func orderedPayloadKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	seen := make(map[string]bool, len(callbackPayloadKeyOrder))
	for _, key := range callbackPayloadKeyOrder {
		if _, ok := payload[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	rest := make([]string, 0, len(payload))
	for key := range payload {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func writeJSONFloat(builder *strings.Builder, value float64) {
	rendered := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(rendered, ".eE") {
		rendered += ".0"
	}
	builder.WriteString(rendered)
}

func writeJSONString(builder *strings.Builder, value string) {
	builder.WriteByte('"')
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
			switch {
			case char < 0x20, char == 0x7f:
				fmt.Fprintf(builder, `\u%04x`, char)
			case char < 0x7f:
				builder.WriteRune(char)
			case char <= 0xffff:
				fmt.Fprintf(builder, `\u%04x`, char)
			default:
				high, low := utf16.EncodeRune(char)
				fmt.Fprintf(builder, `\u%04x\u%04x`, high, low)
			}
		}
	}
	builder.WriteByte('"')
}
