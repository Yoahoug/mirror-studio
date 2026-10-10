package web

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func renderSource(t *testing.T, files map[string]string, name string, values map[string]any) string {
	t.Helper()
	content := fstest.MapFS{}
	for fileName, source := range files {
		content[fileName] = &fstest.MapFile{Data: []byte(source)}
	}
	set := newTemplateSet(content)
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	body, err := set.render(name, location, values, nil)
	if err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return body
}

func renderInline(t *testing.T, source string, values map[string]any) string {
	t.Helper()
	return renderSource(t, map[string]string{"page.html": source}, "page.html", values)
}

func TestEscapeOrderMatchesDjango(t *testing.T) {
	got := escapeHTML(`&<>"'`)
	want := "&amp;&lt;&gt;&quot;&#x27;"
	if got != want {
		t.Fatalf("escape = %q, want %q", got, want)
	}
	if double := escapeHTML("&amp;"); double != "&amp;amp;" {
		t.Fatalf("already escaped text = %q", double)
	}
}

func TestAutoescapeAndSafeFilter(t *testing.T) {
	values := map[string]any{"value": `<b class="x">'&'</b>`, "markup": safeHTML("<i>raw</i>")}
	if got := renderInline(t, `{{ value }}`, values); got != "&lt;b class=&quot;x&quot;&gt;&#x27;&amp;&#x27;&lt;/b&gt;" {
		t.Errorf("escaped value = %q", got)
	}
	if got := renderInline(t, `{{ value|safe }}`, values); got != `<b class="x">'&'</b>` {
		t.Errorf("safe value = %q", got)
	}
	if got := renderInline(t, `{{ markup }}`, values); got != "<i>raw</i>" {
		t.Errorf("pre-rendered value = %q", got)
	}
}

func TestVariableLookupOrder(t *testing.T) {
	values := map[string]any{
		"mapping": map[string]any{"key": "value", "nested": map[string]any{"deep": 7}},
		"list":    []any{"first", "second"},
		"struct":  struct{ Name string }{Name: "field"},
	}
	if got := renderInline(t, `{{ mapping.key }}|{{ mapping.nested.deep }}|{{ list.1 }}|{{ struct.Name }}`, values); got != "value|7|second|field" {
		t.Errorf("lookup = %q", got)
	}
	if got := renderInline(t, `{{ missing }}|{{ mapping.absent }}`, values); got != "|" {
		t.Errorf("missing values = %q, want empty", got)
	}
}

func TestDefaultFilter(t *testing.T) {
	values := map[string]any{"zero": 0, "text": "x", "empty": ""}
	if got := renderInline(t, `{{ zero|default:"0" }}{{ text|default:"0" }}{{ empty|default:"0" }}{{ absent|default:"0" }}`, values); got != "0x00" {
		t.Errorf("default filter = %q", got)
	}
}

func TestDateFilterUsesConfiguredTimeZone(t *testing.T) {
	moment := time.Date(2026, 11, 8, 12, 54, 53, 291800000, time.UTC)
	if got := renderInline(t, `{{ at|date:"Y-m-d H:i" }}`, map[string]any{"at": moment}); got != "2026-11-08 20:54" {
		t.Errorf("date filter = %q", got)
	}
	if got := renderInline(t, `{{ at|date:"Y-m-d H:i" }}`, map[string]any{"at": nil}); got != "" {
		t.Errorf("date of none = %q", got)
	}
}

func TestIfElifElseAndOperators(t *testing.T) {
	values := map[string]any{"user": map[string]any{"is_authenticated": true}, "count": 2}
	source := `{% if not user.is_authenticated %}anon{% elif count == 2 and user.is_authenticated %}two{% else %}other{% endif %}`
	if got := renderInline(t, source, values); got != "two" {
		t.Errorf("if/elif = %q", got)
	}
	if got := renderInline(t, `{% if count != 3 or count > 5 %}yes{% else %}no{% endif %}`, values); got != "yes" {
		t.Errorf("or = %q", got)
	}
	if got := renderInline(t, `{% if user.is_authenticated %}a{% endif %}`, values); got != "a" {
		t.Errorf("truthiness = %q", got)
	}
	if got := renderInline(t, `{% if missing %}a{% else %}b{% endif %}`, values); got != "b" {
		t.Errorf("missing is false, got %q", got)
	}
}

func TestForLoopAndForloopCounter(t *testing.T) {
	values := map[string]any{"images": []map[string]any{{"name": "a"}, {"name": "b"}}}
	source := `{% for image in images %}{{ forloop.counter }}:{{ image.name }},{% empty %}none{% endfor %}`
	if got := renderInline(t, source, values); got != "1:a,2:b," {
		t.Errorf("for = %q", got)
	}
	if got := renderInline(t, source, map[string]any{"images": []map[string]any{}}); got != "none" {
		t.Errorf("empty = %q", got)
	}
	if got := renderInline(t, `{% for a, b in pairs %}{{ a }}{{ b }}{% endfor %}`, map[string]any{"pairs": []any{[]any{"x", "y"}}}); got != "xy" {
		t.Errorf("tuple unpacking = %q", got)
	}
}

func TestExtendsBlocksAndInclude(t *testing.T) {
	files := map[string]string{
		"base.html":    "<head>{% block title %}base{% endblock %}</head><body>{% block content %}{% endblock %}</body>",
		"child.html":   `{% extends "base.html" %}{% block title %}child{% endblock %}{% block content %}{% include "partial.html" %}{% endblock %}`,
		"partial.html": "partial:{{ name }}",
	}
	got := renderSource(t, files, "child.html", map[string]any{"name": "x"})
	if got != "<head>child</head><body>partial:x</body>" {
		t.Errorf("extends = %q", got)
	}
}

func TestCsrfTokenTagShape(t *testing.T) {
	got := renderInline(t, `{% csrf_token %}`, map[string]any{"csrf_token": "abc123"})
	want := `<input type="hidden" name="csrfmiddlewaretoken" value="abc123">`
	if got != want {
		t.Errorf("csrf token = %q, want %q", got, want)
	}
}

func TestFormWidgetRenderingMatchesDjango(t *testing.T) {
	accountForm := loginFormSpec()
	accountForm.bind(map[string]string{"username": `a"b`, "password": "", "captcha_code": "ZZ"})
	accountForm.addError("username", "这个字段是必填项。")
	values := map[string]any{"form": accountForm}
	got := renderInline(t, `{{ form.username }}|{{ form.username.errors }}|{{ form.password }}|{{ form.non_field_errors }}`, values)
	parts := strings.Split(got, "|")
	wantInput := `<input type="text" name="username" value="a&quot;b" maxlength="150" class="w-full rounded-xl border border-slate-200 px-3 py-2 text-slate-900" required aria-invalid="true" aria-describedby="id_username_error" id="id_username">`
	if parts[0] != wantInput {
		t.Errorf("widget = %q, want %q", parts[0], wantInput)
	}
	if parts[1] != `<ul class="errorlist" id="id_username_error"><li>这个字段是必填项。</li></ul>` {
		t.Errorf("errors = %q", parts[1])
	}
	wantPassword := `<input type="password" name="password" class="w-full rounded-xl border border-slate-200 px-3 py-2 text-slate-900" required id="id_password">`
	if parts[2] != wantPassword {
		t.Errorf("password widget = %q, want %q", parts[2], wantPassword)
	}
	if parts[3] != "" {
		t.Errorf("non field errors = %q", parts[3])
	}
}

func TestRegisterFormWidgetOrder(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{emailVerification: true})
	accountForm := harness.service.registerFormSpec()
	values := map[string]any{"form": accountForm}
	got := renderInline(t, `{{ form.email_verification_code }}`, values)
	want := `<input type="text" name="email_verification_code" maxlength="8" class="w-full rounded-xl border border-slate-200 px-3 py-2 text-slate-900" inputmode="numeric" required id="id_email_verification_code">`
	if got != want {
		t.Errorf("widget = %q, want %q", got, want)
	}
}

func TestAssetVersionIsStableAndContentDerived(t *testing.T) {
	first := newPageHarness(t, harnessOptions{})
	second := newPageHarness(t, harnessOptions{})
	if first.service.assetVersion != second.service.assetVersion {
		t.Errorf("asset version is not stable: %q vs %q", first.service.assetVersion, second.service.assetVersion)
	}
	if len(first.service.assetVersion) == 0 || strings.ContainsAny(first.service.assetVersion, "+/=") {
		t.Errorf("asset version %q is not a plain digest", first.service.assetVersion)
	}
}

func TestCaptchaDataURIShape(t *testing.T) {
	uri := renderCaptchaDataURI("AB23")
	if !strings.HasPrefix(uri, captchaSVGPrefix) {
		t.Errorf("captcha uri = %.60q", uri)
	}
	if uri != renderCaptchaDataURI("AB23") {
		t.Errorf("captcha rendering is not deterministic for the same code")
	}
	if uri == renderCaptchaDataURI("AB24") {
		t.Errorf("different codes must render differently")
	}
	if len(uri) < 200 {
		t.Errorf("captcha uri looks truncated: %d bytes", len(uri))
	}
}

func TestPythonJSONDumpsEscapesLikeTheReference(t *testing.T) {
	got := pythonJSONDumps([][2]string{{"version", "v1.2"}, {"notes", "a\nb\"c\\d"}})
	want := `{"version": "v1.2", "notes": "a\nb\"c\\d"}`
	if got != want {
		t.Errorf("json = %q, want %q", got, want)
	}
	if escaped := pythonJSONDumps([][2]string{{"title", "绘镜 <b>&</b>"}}); escaped != `{"title": "绘镜 \u003cb\u003e\u0026\u003c/b\u003e"}` {
		// json.dumps leaves <, > and & alone, but this value is embedded in a
		// <script type="application/json"> element through |safe, so the encoder
		// escapes them the way Django's json_script filter does: a note holding
		// "</script>" must not be able to close that element.
		t.Errorf("json = %q", escaped)
	}
}

func TestQuoteLikeDjangoKeepsSlashes(t *testing.T) {
	got := quoteLikeDjango("/account/history?foo=1&bar=%E4%B8%AD", "/")
	want := "/account/history%3Ffoo%3D1%26bar%3D%25E4%25B8%25AD"
	if got != want {
		t.Errorf("quote = %q, want %q", got, want)
	}
}
