package media

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The JSON text columns of mediahub_uploadsession (uploaded_chunks,
// result_json) are compared byte for byte in the snapshot, so they have to be
// rendered exactly like python's json.dumps with its default separators and
// ensure_ascii=True — including the key order of the result payload.
type jsonField struct {
	key   string
	value any
}

// jsonObject is an ordered JSON object.
type jsonObject []jsonField

// MarshalJSON renders the object as JSON, keeping the declared key order.
func (o jsonObject) MarshalJSON() ([]byte, error) {
	builder := &strings.Builder{}
	writeJSONValue(builder, o, false)
	return []byte(builder.String()), nil
}

// AsMap converts the ordered form into the map the HTTP layer returns; JSON
// object key order is not part of any response contract.
func (o jsonObject) AsMap() map[string]any {
	result := make(map[string]any, len(o))
	for _, field := range o {
		result[field.key] = field.value
	}
	return result
}

// pythonJSONDump mirrors json.dumps(value): ", " and ": " separators, ASCII
// escaping, declared key order.
func pythonJSONDump(value any) string {
	builder := &strings.Builder{}
	writeJSONValue(builder, value, false)
	return builder.String()
}

// pythonJSONCompact mirrors json.dumps(value, separators=(",", ":")) and is
// what the signed reference-upload token carries.
func pythonJSONCompact(value any) string {
	builder := &strings.Builder{}
	writeJSONValue(builder, value, true)
	return builder.String()
}

func writeJSONValue(builder *strings.Builder, value any, compact bool) {
	switch typed := value.(type) {
	case nil:
		builder.WriteString("null")
	case jsonObject:
		builder.WriteString("{")
		for index, field := range typed {
			writeJSONSeparator(builder, index, compact)
			builder.WriteString(pythonJSONString(field.key))
			writeJSONColon(builder, compact)
			writeJSONValue(builder, field.value, compact)
		}
		builder.WriteString("}")
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		builder.WriteString("{")
		for index, key := range keys {
			writeJSONSeparator(builder, index, compact)
			builder.WriteString(pythonJSONString(key))
			writeJSONColon(builder, compact)
			writeJSONValue(builder, typed[key], compact)
		}
		builder.WriteString("}")
	case []any:
		builder.WriteString("[")
		for index, item := range typed {
			writeJSONSeparator(builder, index, compact)
			writeJSONValue(builder, item, compact)
		}
		builder.WriteString("]")
	case []int:
		builder.WriteString("[")
		for index, item := range typed {
			writeJSONSeparator(builder, index, compact)
			builder.WriteString(strconv.Itoa(item))
		}
		builder.WriteString("]")
	case []string:
		builder.WriteString("[")
		for index, item := range typed {
			writeJSONSeparator(builder, index, compact)
			builder.WriteString(pythonJSONString(item))
		}
		builder.WriteString("]")
	case string:
		builder.WriteString(pythonJSONString(typed))
	case bool:
		if typed {
			builder.WriteString("true")
		} else {
			builder.WriteString("false")
		}
	case int:
		builder.WriteString(strconv.Itoa(typed))
	case int64:
		builder.WriteString(strconv.FormatInt(typed, 10))
	case float64:
		builder.WriteString(pythonFloat(typed))
	case time.Time:
		builder.WriteString(pythonJSONString(pythonIsoformat(typed)))
	default:
		builder.WriteString(pythonJSONString(fmt.Sprintf("%v", typed)))
	}
}

func writeJSONSeparator(builder *strings.Builder, index int, compact bool) {
	if index == 0 {
		return
	}
	if compact {
		builder.WriteString(",")
		return
	}
	builder.WriteString(", ")
}

func writeJSONColon(builder *strings.Builder, compact bool) {
	if compact {
		builder.WriteString(":")
		return
	}
	builder.WriteString(": ")
}

func pythonFloat(value float64) string {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return "NaN"
	}
	if value == math.Trunc(value) && math.Abs(value) < 1e16 {
		return strconv.FormatFloat(value, 'f', 1, 64)
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// pythonJSONString escapes a string the way json.dumps(ensure_ascii=True) does.
func pythonJSONString(value string) string {
	builder := &strings.Builder{}
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
			case char < 0x20:
				builder.WriteString(fmt.Sprintf(`\u%04x`, char))
			case char < 0x7f:
				builder.WriteRune(char)
			case char > 0xffff:
				high, low := utf16SurrogatePair(char)
				builder.WriteString(fmt.Sprintf(`\u%04x\u%04x`, high, low))
			default:
				builder.WriteString(fmt.Sprintf(`\u%04x`, char))
			}
		}
	}
	builder.WriteByte('"')
	return builder.String()
}

func utf16SurrogatePair(char rune) (uint16, uint16) {
	value := char - 0x10000
	return uint16(0xd800 + (value >> 10)), uint16(0xdc00 + (value & 0x3ff))
}

// pythonIsoformat renders a timestamp the way datetime.isoformat() does: UTC
// with the +00:00 offset, and no fractional part when it is exactly zero.
func pythonIsoformat(value time.Time) string {
	utc := value.UTC()
	if utc.Nanosecond()/1000 == 0 {
		return utc.Format("2006-01-02T15:04:05+00:00")
	}
	return utc.Format("2006-01-02T15:04:05.000000+00:00")
}
