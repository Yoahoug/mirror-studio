package novel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// pyObj is an insertion-ordered JSON object. The reference implementation
// stores the event log and the scene plan with Python's json.dumps, so both the
// key order and the ", " / ": " separators are part of the recorded database
// snapshot and of the SSE byte stream; Go maps cannot carry that.
type pyObj struct {
	keys   []string
	values []any
}

func newPyObj() *pyObj { return &pyObj{} }

// Set appends key/value in insertion order, overwriting an existing key in place.
func (o *pyObj) Set(key string, value any) *pyObj {
	for index, existing := range o.keys {
		if existing == key {
			o.values[index] = value
			return o
		}
	}
	o.keys = append(o.keys, key)
	o.values = append(o.values, value)
	return o
}

func (o *pyObj) Len() int { return len(o.keys) }

func (o *pyObj) Keys() []string { return append([]string(nil), o.keys...) }

func (o *pyObj) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	for index, existing := range o.keys {
		if existing == key {
			return o.values[index], true
		}
	}
	return nil, false
}

func (o *pyObj) Has(key string) bool {
	_, ok := o.Get(key)
	return ok
}

// Child returns a nested object field, or nil when absent or of another type.
func (o *pyObj) Child(key string) *pyObj {
	value, ok := o.Get(key)
	if !ok {
		return nil
	}
	child, _ := value.(*pyObj)
	return child
}

// marshalPython renders a value the way json.dumps does: ", " between items,
// ": " after keys, and \uXXXX escapes for non-ASCII text when ascii is set.
func marshalPython(value any, ascii bool) (string, error) {
	builder := &strings.Builder{}
	if err := writePyJSON(builder, value, ascii); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func writePyJSON(builder *strings.Builder, value any, ascii bool) error {
	switch typed := value.(type) {
	case nil:
		builder.WriteString("null")
	case *pyObj:
		builder.WriteByte('{')
		for index, key := range typed.keys {
			if index > 0 {
				builder.WriteString(", ")
			}
			writePyString(builder, key, ascii)
			builder.WriteString(": ")
			if err := writePyJSON(builder, typed.values[index], ascii); err != nil {
				return err
			}
		}
		builder.WriteByte('}')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		builder.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				builder.WriteString(", ")
			}
			writePyString(builder, key, ascii)
			builder.WriteString(": ")
			if err := writePyJSON(builder, typed[key], ascii); err != nil {
				return err
			}
		}
		builder.WriteByte('}')
	case []any:
		builder.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				builder.WriteString(", ")
			}
			if err := writePyJSON(builder, item, ascii); err != nil {
				return err
			}
		}
		builder.WriteByte(']')
	case []*pyObj:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return writePyJSON(builder, items, ascii)
	case string:
		writePyString(builder, typed, ascii)
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
	case json.Number:
		builder.WriteString(string(typed))
	case float64:
		builder.WriteString(pyFloat(typed))
	default:
		return fmt.Errorf("novel: cannot encode %T as JSON", value)
	}
	return nil
}

func writePyString(builder *strings.Builder, value string, ascii bool) {
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
			case ascii && char >= 0x80:
				if char > 0xffff {
					high, low := utf16.EncodeRune(char)
					fmt.Fprintf(builder, `\u%04x\u%04x`, high, low)
					continue
				}
				fmt.Fprintf(builder, `\u%04x`, char)
			default:
				builder.WriteRune(char)
			}
		}
	}
	builder.WriteByte('"')
}

// pyFloat mirrors Python's repr for floats, which is what json.dumps emits.
func pyFloat(value float64) string {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return "NaN"
	}
	text := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}
	return text
}

// decodePyJSON parses JSON preserving object key order, so a payload that made
// a round trip through the database re-serializes to the same bytes.
func decodePyJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodePyValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("novel: trailing JSON content")
	}
	return value, nil
}

func decodePyValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		object := newPyObj()
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("novel: object key is not a string")
			}
			value, err := decodePyValue(decoder)
			if err != nil {
				return nil, err
			}
			object.Set(key, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		items := []any{}
		for decoder.More() {
			value, err := decodePyValue(decoder)
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
	return nil, fmt.Errorf("novel: unexpected JSON delimiter %q", delim)
}
