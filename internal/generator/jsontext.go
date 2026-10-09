package generator

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// orderedObject keeps JSON object keys in document order. Django stores JSON
// columns with json.dumps, which preserves insertion order, and the differential
// harness compares generator_generatetask.provider_response_json byte for byte,
// so a Go map (sorted keys) would not do.
type orderedObject struct {
	keys   []string
	values map[string]any
}

func newOrderedObject() *orderedObject {
	return &orderedObject{values: map[string]any{}}
}

func (o *orderedObject) set(key string, value any) {
	if _, seen := o.values[key]; !seen {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// decodeOrderedJSON parses JSON while preserving object key order and number
// literals.
func decodeOrderedJSON(raw string) (any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	value, err := decodeOrderedValue(decoder)
	if err != nil {
		return nil, err
	}
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}
	return value, nil
}

func decodeOrderedValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		object := newOrderedObject()
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, _ := keyToken.(string)
			value, err := decodeOrderedValue(decoder)
			if err != nil {
				return nil, err
			}
			object.set(key, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		items := []any{}
		for decoder.More() {
			value, err := decodeOrderedValue(decoder)
			if err != nil {
				return nil, err
			}
			items = append(items, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return items, nil
	}
	return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
}

func ensureEOF(decoder *json.Decoder) error {
	if decoder.More() {
		return fmt.Errorf("unexpected trailing JSON content")
	}
	return nil
}

// encodeDjangoJSON renders the value the way Django's JSONField writes a column:
// json.dumps defaults, i.e. ensure_ascii=True and ", " / ": " separators.
func encodeDjangoJSON(value any) string {
	builder := &strings.Builder{}
	writeDjangoValue(builder, value)
	return builder.String()
}

func writeDjangoValue(builder *strings.Builder, value any) {
	switch typed := value.(type) {
	case nil:
		builder.WriteString("null")
	case *orderedObject:
		builder.WriteByte('{')
		for index, key := range typed.keys {
			if index > 0 {
				builder.WriteString(", ")
			}
			writeDjangoString(builder, key)
			builder.WriteString(": ")
			writeDjangoValue(builder, typed.values[key])
		}
		builder.WriteByte('}')
	case map[string]any:
		object := newOrderedObject()
		for _, key := range sortedKeys(typed) {
			object.set(key, typed[key])
		}
		writeDjangoValue(builder, object)
	case []any:
		builder.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				builder.WriteString(", ")
			}
			writeDjangoValue(builder, item)
		}
		builder.WriteByte(']')
	case string:
		writeDjangoString(builder, typed)
	case bool:
		if typed {
			builder.WriteString("true")
		} else {
			builder.WriteString("false")
		}
	case json.Number:
		builder.WriteString(typed.String())
	case int:
		builder.WriteString(strconv.Itoa(typed))
	case int64:
		builder.WriteString(strconv.FormatInt(typed, 10))
	case float64:
		builder.WriteString(strconv.FormatFloat(typed, 'g', -1, 64))
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			builder.WriteString("null")
			return
		}
		builder.Write(encoded)
	}
}

// encodeCompactJSON renders the value with json.dumps(separators=(",", ":"))
// semantics, which is what the reference-image upload token signs.
func encodeCompactJSON(value any) string {
	builder := &strings.Builder{}
	writeCompactValue(builder, value)
	return builder.String()
}

func writeCompactValue(builder *strings.Builder, value any) {
	switch typed := value.(type) {
	case *orderedObject:
		builder.WriteByte('{')
		for index, key := range typed.keys {
			if index > 0 {
				builder.WriteByte(',')
			}
			writeDjangoString(builder, key)
			builder.WriteByte(':')
			writeCompactValue(builder, typed.values[key])
		}
		builder.WriteByte('}')
	case map[string]any:
		object := newOrderedObject()
		for _, key := range sortedKeys(typed) {
			object.set(key, typed[key])
		}
		writeCompactValue(builder, object)
	case []any:
		builder.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				builder.WriteByte(',')
			}
			writeCompactValue(builder, item)
		}
		builder.WriteByte(']')
	case string:
		writeDjangoString(builder, typed)
	default:
		builder.WriteString(encodeDjangoJSON(value))
	}
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// writeDjangoString escapes a string exactly like json.dumps(ensure_ascii=True):
// control characters and every non-ASCII rune become \uXXXX escapes, astral
// code points become a surrogate pair.
func writeDjangoString(builder *strings.Builder, value string) {
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
				fmt.Fprintf(builder, `\u%04x`, char)
			case char < 0x7f:
				builder.WriteRune(char)
			case char <= 0xffff:
				fmt.Fprintf(builder, `\u%04x`, char)
			default:
				high, low := utf16Surrogates(char)
				fmt.Fprintf(builder, `\u%04x\u%04x`, high, low)
			}
		}
	}
	builder.WriteByte('"')
}

// plainJSON converts the ordered document tree into plain Go values so
// collaborators that render their own JSON (the audit log) see maps instead of
// the order-preserving wrapper.
func plainJSON(value any) any {
	switch typed := value.(type) {
	case *orderedObject:
		result := make(map[string]any, len(typed.keys))
		for _, key := range typed.keys {
			result[key] = plainJSON(typed.values[key])
		}
		return result
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, plainJSON(item))
		}
		return result
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return int(integer)
		}
		if number, err := typed.Float64(); err == nil {
			return number
		}
		return typed.String()
	}
	return value
}

func utf16Surrogates(char rune) (uint16, uint16) {
	value := char - 0x10000
	return uint16(0xd800 + (value >> 10)), uint16(0xdc00 + (value & 0x3ff))
}
