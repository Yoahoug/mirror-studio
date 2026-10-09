package web

import (
	"fmt"
	"io/fs"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// safeHTML marks a value the reference template engine already escaped.
type safeHTML string

func (s safeHTML) templateHTML() string { return string(s) }

// htmlRenderer is any value that renders its own markup, which the engine must
// not escape a second time.
type htmlRenderer interface{ templateHTML() string }

// fieldLookup lets a context value answer dotted lookups itself, which is how
// form objects expose their bound fields.
type fieldLookup interface {
	lookupField(name string) (any, bool)
}

type scope struct {
	values map[string]any
	parent *scope
}

func (s *scope) lookup(name string) (any, bool) {
	for current := s; current != nil; current = current.parent {
		if value, ok := current.values[name]; ok {
			return value, true
		}
	}
	return nil, false
}

func (s *scope) child(values map[string]any) *scope {
	return &scope{values: values, parent: s}
}

type renderState struct {
	scope     *scope
	overrides map[string][]node
	engine    *templateSet
	location  *time.Location
	// onCSRF is called the first time {% csrf_token %} is rendered. Django only
	// emits the csrftoken cookie for responses whose template actually asked for
	// the token (or whose view called ensure_csrf_cookie), so the caller uses
	// this to decide whether to set it.
	onCSRF func()
}

type node interface {
	render(state *renderState, out *strings.Builder) error
}

type Template struct {
	name   string
	nodes  []node
	blocks map[string][]node
	parent string
}

type templateSet struct {
	fsys  fs.FS
	mu    sync.RWMutex
	cache map[string]*Template
}

func newTemplateSet(fsys fs.FS) *templateSet {
	return &templateSet{fsys: fsys, cache: map[string]*Template{}}
}

// normaliseNewlines mirrors the reference loader, whose text-mode read applies
// universal newline translation before the template is parsed.
func normaliseNewlines(source string) string {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	return strings.ReplaceAll(source, "\r", "\n")
}

func (t *templateSet) load(name string) (*Template, error) {
	t.mu.RLock()
	cached, ok := t.cache[name]
	t.mu.RUnlock()
	if ok {
		return cached, nil
	}
	source, err := fs.ReadFile(t.fsys, name)
	if err != nil {
		return nil, fmt.Errorf("template %q: %w", name, err)
	}
	parsed, err := parseTemplate(name, normaliseNewlines(string(source)))
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.cache[name] = parsed
	t.mu.Unlock()
	return parsed, nil
}

func (t *templateSet) render(name string, location *time.Location, values map[string]any, onCSRF func()) (string, error) {
	template, err := t.load(name)
	if err != nil {
		return "", err
	}
	state := &renderState{scope: &scope{values: values}, engine: t, location: location, onCSRF: onCSRF}
	out := &strings.Builder{}
	if err := t.renderTemplate(template, state, out, nil); err != nil {
		return "", err
	}
	return out.String(), nil
}

func (t *templateSet) renderTemplate(template *Template, state *renderState, out *strings.Builder, overrides map[string][]node) error {
	if template.parent == "" {
		return renderNodes(template.nodes, state, out, overrides)
	}
	parent, err := t.load(template.parent)
	if err != nil {
		return err
	}
	merged := map[string][]node{}
	for name, body := range parent.blocks {
		merged[name] = body
	}
	for name, body := range overrides {
		merged[name] = body
	}
	for name, body := range template.blocks {
		merged[name] = body
	}
	return t.renderTemplate(parent, state, out, merged)
}

func renderNodes(nodes []node, state *renderState, out *strings.Builder, overrides map[string][]node) error {
	saved := state.overrides
	if overrides != nil {
		state.overrides = overrides
	}
	defer func() { state.overrides = saved }()
	for _, item := range nodes {
		if err := item.render(state, out); err != nil {
			return err
		}
	}
	return nil
}

// ── nodes ──

type textNode string

func (n textNode) render(_ *renderState, out *strings.Builder) error {
	out.WriteString(string(n))
	return nil
}

type varNode struct{ expr *filterExpression }

func (n varNode) render(state *renderState, out *strings.Builder) error {
	value, _ := n.expr.resolve(state)
	out.WriteString(state.renderValue(value, n.expr.autoEscape))
	return nil
}

type blockNode struct {
	name string
	body []node
}

func (n *blockNode) render(state *renderState, out *strings.Builder) error {
	if body, ok := state.overrides[n.name]; ok && body != nil {
		remaining := map[string][]node{}
		for name, item := range state.overrides {
			if name != n.name {
				remaining[name] = item
			}
		}
		return renderNodes(body, state, out, remaining)
	}
	return renderNodes(n.body, state, out, nil)
}

type includeNode struct{ name string }

func (n includeNode) render(state *renderState, out *strings.Builder) error {
	included, err := state.engine.load(n.name)
	if err != nil {
		return err
	}
	return state.engine.renderTemplate(included, state, out, state.overrides)
}

type csrfNode struct{}

func (csrfNode) render(state *renderState, out *strings.Builder) error {
	if state.onCSRF != nil {
		state.onCSRF()
	}
	token, _ := state.scope.lookup("csrf_token")
	value, _ := token.(string)
	out.WriteString(`<input type="hidden" name="csrfmiddlewaretoken" value="` + escapeHTML(value) + `">`)
	return nil
}

type ifBranch struct {
	condition *conditionExpression
	body      []node
}

type ifNode struct {
	branches []ifBranch
	fallback []node
}

func (n *ifNode) render(state *renderState, out *strings.Builder) error {
	for _, branch := range n.branches {
		if branch.condition.evaluate(state) {
			return renderNodes(branch.body, state, out, nil)
		}
	}
	return renderNodes(n.fallback, state, out, nil)
}

type forNode struct {
	names []string
	seq   *filterExpression
	body  []node
	empty []node
}

func (n *forNode) render(state *renderState, out *strings.Builder) error {
	value, _ := n.seq.resolve(state)
	items := iterate(value)
	if len(items) == 0 {
		return renderNodes(n.empty, state, out, nil)
	}
	for index, item := range items {
		bindings := map[string]any{}
		if len(n.names) == 1 {
			bindings[n.names[0]] = item.value
		} else {
			for position, name := range n.names {
				var field any
				if position < len(item.values) {
					field = item.values[position]
				}
				bindings[name] = field
			}
		}
		bindings["forloop"] = map[string]any{
			"counter":     index + 1,
			"counter0":    index,
			"revcounter":  len(items) - index,
			"revcounter0": len(items) - index - 1,
			"first":       index == 0,
			"last":        index == len(items)-1,
		}
		child := &renderState{
			scope:     state.scope.child(bindings),
			overrides: state.overrides,
			engine:    state.engine,
			location:  state.location,
			onCSRF:    state.onCSRF,
		}
		if err := renderNodes(n.body, child, out, nil); err != nil {
			return err
		}
	}
	return nil
}

type iterItem struct {
	value  any
	values []any
}

func iterate(value any) []iterItem {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []any:
		items := make([]iterItem, 0, len(typed))
		for _, entry := range typed {
			items = append(items, iterItem{value: entry, values: tupleOf(entry)})
		}
		return items
	case []map[string]any:
		items := make([]iterItem, 0, len(typed))
		for _, entry := range typed {
			items = append(items, iterItem{value: entry, values: tupleOf(entry)})
		}
		return items
	case map[string]any:
		items := make([]iterItem, 0, len(typed))
		for key, entry := range typed {
			items = append(items, iterItem{value: key, values: tupleOf(entry)})
		}
		return items
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Slice, reflect.Array:
		items := make([]iterItem, 0, reflected.Len())
		for index := 0; index < reflected.Len(); index++ {
			entry := reflected.Index(index).Interface()
			items = append(items, iterItem{value: entry, values: tupleOf(entry)})
		}
		return items
	}
	return nil
}

func tupleOf(value any) []any {
	if pair, ok := value.([]any); ok {
		return pair
	}
	return []any{value}
}

// ── lookups and value conversion ──

func resolvePath(root any, parts []string) (any, bool) {
	current := root
	for _, part := range parts {
		next, ok := lookupPart(current, part)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func lookupPart(value any, name string) (any, bool) {
	if value == nil {
		return nil, false
	}
	if lookup, ok := value.(fieldLookup); ok {
		if found, ok := lookup.lookupField(name); ok {
			return found, true
		}
	}
	if mapping, ok := value.(map[string]any); ok {
		if found, ok := mapping[name]; ok {
			return found, true
		}
		return nil, false
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Pointer:
		if reflected.IsNil() {
			return nil, false
		}
		return lookupPart(reflected.Elem().Interface(), name)
	case reflect.Struct:
		if field := reflected.FieldByNameFunc(func(candidate string) bool {
			return strings.EqualFold(candidate, name)
		}); field.IsValid() && field.CanInterface() {
			return field.Interface(), true
		}
		if method := reflected.MethodByName(name); method.IsValid() {
			if results := method.Call(nil); len(results) == 1 {
				return results[0].Interface(), true
			}
		}
		if method := reflected.MethodByName(exportedName(name)); method.IsValid() {
			if results := method.Call(nil); len(results) == 1 {
				return results[0].Interface(), true
			}
		}
		return nil, false
	case reflect.Map:
		key := reflect.ValueOf(name)
		if key.Type().AssignableTo(reflected.Type().Key()) {
			if found := reflected.MapIndex(key); found.IsValid() {
				return found.Interface(), true
			}
		}
		return nil, false
	case reflect.Slice, reflect.Array:
		index, err := strconv.Atoi(name)
		if err != nil || index < 0 || index >= reflected.Len() {
			return nil, false
		}
		return reflected.Index(index).Interface(), true
	}
	return nil, false
}

func exportedName(name string) string {
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	case []any:
		return len(typed) != 0
	case []map[string]any:
		return len(typed) != 0
	case map[string]any:
		return len(typed) != 0
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		if reflected.IsNil() {
			return false
		}
		if reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Map {
			return reflected.Len() != 0
		}
		return true
	}
	return true
}

func (state *renderState) renderValue(value any, autoEscape bool) string {
	if value == nil {
		return "None"
	}
	if rendered, ok := value.(htmlRenderer); ok {
		return rendered.templateHTML()
	}
	text := stringify(value)
	if autoEscape {
		return escapeHTML(text)
	}
	return text
}

func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return "None"
	case string:
		return typed
	case safeHTML:
		return string(typed)
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float64:
		return formatFloat(typed)
	case float32:
		return formatFloat(float64(typed))
	case time.Time:
		return typed.Format("2006-01-02 15:04:05.999999")
	case fmt.Stringer:
		return typed.String()
	}
	return fmt.Sprintf("%v", value)
}

func formatFloat(value float64) string {
	text := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}
	return text
}

// escapeHTML mirrors django.utils.html.escape: the ampersand is replaced first,
// so entities introduced by later steps are never re-escaped.
func escapeHTML(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	value = strings.ReplaceAll(value, "'", "&#x27;")
	return value
}
