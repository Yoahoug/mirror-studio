package web

import (
	"regexp"
	"strconv"
	"strings"
)

// styledClass is the class StyledFormMixin applies to every widget.
const styledClass = "w-full rounded-xl border border-slate-200 px-3 py-2 text-slate-900"

type formField struct {
	name      string
	inputType string
	maxLength int
	required  bool
	attrs     [][2]string
}

type form struct {
	fields   []formField
	data     map[string]string
	bound    bool
	errors   map[string][]string
	nonField []string
}

func newForm(fields []formField) *form {
	return &form{fields: fields, errors: map[string][]string{}}
}

func (f *form) field(name string) (formField, bool) {
	for _, candidate := range f.fields {
		if candidate.name == name {
			return candidate, true
		}
	}
	return formField{}, false
}

func (f *form) bind(data map[string]string) {
	f.bound = true
	f.data = map[string]string{}
	for _, field := range f.fields {
		if value, ok := data[field.name]; ok {
			f.data[field.name] = value
		}
	}
}

func (f *form) value(name string) string {
	if f.data == nil {
		return ""
	}
	return f.data[name]
}

func (f *form) addError(name, message string) {
	f.errors[name] = append(f.errors[name], message)
}

func (f *form) addNonFieldError(message string) {
	f.nonField = append(f.nonField, message)
}

func (f *form) errorsFor(name string) []string { return f.errors[name] }

func (f *form) valid() bool { return len(f.nonField) == 0 && !f.hasFieldErrors() }

func (f *form) hasFieldErrors() bool {
	for _, messages := range f.errors {
		if len(messages) > 0 {
			return true
		}
	}
	return false
}

// lookupField exposes bound fields to the template engine, the way the
// reference exposes form[name].
func (f *form) lookupField(name string) (any, bool) {
	if name == "non_field_errors" {
		return safeHTML(renderErrorList("errorlist nonfield", "", f.nonField)), true
	}
	field, ok := f.field(name)
	if !ok {
		return nil, false
	}
	return &boundField{form: f, field: field}, true
}

func (b *boundField) lookupField(name string) (any, bool) {
	switch name {
	case "errors":
		return safeHTML(renderErrorList("errorlist", b.id()+"_error", b.form.errorsFor(b.field.name))), true
	case "value":
		return b.form.value(b.field.name), true
	case "name":
		return b.field.name, true
	}
	return nil, false
}

type boundField struct {
	form  *form
	field formField
}

func (b *boundField) templateHTML() string { return b.html() }

func (b *boundField) id() string { return "id_" + b.field.name }

func (b *boundField) html() string {
	out := &strings.Builder{}
	out.WriteString(`<input type="` + b.field.inputType + `" name="` + b.field.name + `"`)
	if value := b.form.value(b.field.name); value != "" {
		out.WriteString(` value="` + escapeHTML(value) + `"`)
	}
	if b.field.maxLength > 0 {
		out.WriteString(` maxlength="` + strconv.Itoa(b.field.maxLength) + `"`)
	}
	out.WriteString(` class="` + styledClass + `"`)
	for _, attr := range b.field.attrs {
		out.WriteString(" " + attr[0] + `="` + escapeHTML(attr[1]) + `"`)
	}
	if b.field.required {
		out.WriteString(" required")
	}
	if len(b.form.errorsFor(b.field.name)) > 0 {
		out.WriteString(` aria-invalid="true"`)
		out.WriteString(` aria-describedby="` + b.id() + `_error"`)
	}
	out.WriteString(` id="` + b.id() + `">`)
	return out.String()
}

func renderErrorList(class, id string, messages []string) string {
	if len(messages) == 0 {
		return ""
	}
	out := &strings.Builder{}
	out.WriteString(`<ul class="` + class + `"`)
	if id != "" {
		out.WriteString(` id="` + id + `"`)
	}
	out.WriteString(">")
	for _, message := range messages {
		out.WriteString("<li>" + escapeHTML(message) + "</li>")
	}
	out.WriteString("</ul>")
	return out.String()
}

// ── field validation, mirroring the reference messages ──

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func (f *form) validateRequired() {
	for _, field := range f.fields {
		if !field.required {
			continue
		}
		if strings.TrimSpace(f.value(field.name)) == "" {
			f.addError(field.name, "这个字段是必填项。")
		}
	}
}

func (f *form) validateMaxLength() {
	for _, field := range f.fields {
		if field.maxLength <= 0 {
			continue
		}
		if runes := []rune(f.value(field.name)); len(runes) > field.maxLength {
			f.addError(field.name, "请确保这个值最多包含 "+strconv.Itoa(field.maxLength)+" 个字符（当前长度 "+strconv.Itoa(len(runes))+" 个字符）。")
		}
	}
}

func (f *form) validateEmail(name string) {
	value := strings.TrimSpace(f.value(name))
	if value == "" {
		return
	}
	if !emailPattern.MatchString(value) {
		f.addError(name, "请输入一个有效的电子邮件地址。")
	}
}

func (f *form) hasError(name string) bool { return len(f.errors[name]) > 0 }
