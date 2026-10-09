package risk

import (
	"fmt"
	"strings"
)

// pyQuote renders a Go string exactly like Python's
// json.dumps(value, ensure_ascii=True) renders it, because risk_riskevent rows
// are compared byte for byte against the reference database.
func pyQuote(value string) string {
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
			case char < 0x20, char == 0x7f:
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
	return builder.String()
}

func utf16Surrogates(char rune) (uint16, uint16) {
	value := char - 0x10000
	return uint16(0xd800 + (value >> 10)), uint16(0xdc00 + (value & 0x3ff))
}
