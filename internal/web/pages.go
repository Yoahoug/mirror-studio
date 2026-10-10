package web

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/fs"
	"math/rand"
	"net/http"
	"sort"
	"strings"

	"mirror-studio/internal/httpapi"
)

// The all: prefix keeps the partials whose names start with an underscore.
//
//go:embed all:templates
var templateFiles embed.FS

const captchaSVGPrefix = "data:image/svg+xml;base64,"

// djangoErrorPage is django.views.defaults.ERROR_PAGE_TEMPLATE, which is what
// an unmatched URL or a denied console request renders.
const djangoErrorPage = "\n<!doctype html>\n<html lang=\"en\">\n<head>\n  <title>%s</title>\n</head>\n<body>\n  <h1>%s</h1><p>%s</p>\n</body>\n</html>\n"

// renderNotFound answers with the framework's debug error page, which the
// reference raises for an unmatched path.
func renderNotFound(w http.ResponseWriter, r *http.Request) {
	httpapi.WriteTechnicalHTML(w, r, http.StatusNotFound, "Page not found")
}

func renderForbidden(w http.ResponseWriter) {
	writeHTML(w, http.StatusForbidden, fmt.Sprintf(djangoErrorPage, "403 Forbidden", "403 Forbidden", ""))
}

func renderServerError(w http.ResponseWriter) {
	writeHTML(w, http.StatusInternalServerError, fmt.Sprintf(djangoErrorPage, "Server Error (500)", "Server Error (500)", ""))
}

func writeHTML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// redirectFound writes the framework's empty 302, which carries no body.
func redirectFound(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusFound)
}

// quoteLikeDjango escapes a value the way urllib.parse.quote does with the
// reference's safe set, which keeps "/" literal and percent-encodes the rest.
func quoteLikeDjango(value, safe string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-~"
	out := &strings.Builder{}
	for _, char := range []byte(value) {
		if strings.IndexByte(unreserved, char) >= 0 || strings.IndexByte(safe, char) >= 0 {
			out.WriteByte(char)
			continue
		}
		out.WriteString("%")
		out.WriteString(strings.ToUpper(hex.EncodeToString([]byte{char})))
	}
	return out.String()
}

func loginRequiredRedirect(w http.ResponseWriter, r *http.Request) {
	redirectFound(w, "/workspace?auth=login&next="+quoteLikeDjango(fullPath(r), "/"))
}

func fullPath(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return r.URL.Path
	}
	return r.URL.Path + "?" + r.URL.RawQuery
}

// ── captcha ──

// renderCaptchaDataURI reproduces the reference SVG generator: the drawing is
// derived deterministically from the code, so the same code always renders the
// same image. Codes themselves come from internal/auth's CSPRNG generator.
func renderCaptchaDataURI(code string) string {
	width, height := 168, 56
	seed := int64(0)
	for index, char := range code {
		seed += int64(index+1) * int64(char)
	}
	source := rand.New(rand.NewSource(seed))

	circles := &strings.Builder{}
	for index := 0; index < 7; index++ {
		fmt.Fprintf(circles,
			`<circle cx="%d" cy="%d" r="%d" fill="rgba(%d, %d, 255, 0.32)" />`,
			source.Intn(width-16)+8, source.Intn(height-16)+8, source.Intn(3)+2,
			source.Intn(56)+180, source.Intn(46)+190)
	}
	paths := &strings.Builder{}
	for index := 0; index < 4; index++ {
		startY := source.Intn(height-20) + 10
		controlY := source.Intn(height-8) + 4
		endY := source.Intn(height-20) + 10
		fmt.Fprintf(paths,
			`<path d="M -8 %d Q %.1f %d %d %d" stroke="rgba(%d, %d, %d, 0.42)" stroke-width="%.2f" fill="none" stroke-linecap="round" />`,
			startY, float64(width)/2, controlY, width+8, endY,
			source.Intn(71)+120, source.Intn(71)+135, source.Intn(56)+190, 1.2+source.Float64()*0.9)
	}
	glyphs := &strings.Builder{}
	startX, gap := 24, 33
	for index, char := range code {
		x := startX + index*gap + source.Intn(5) - 2
		y := 36 + source.Intn(10) - 4
		rotate := source.Intn(25) - 12
		fontSize := source.Intn(7) + 25
		fmt.Fprintf(glyphs,
			`<text x="%d" y="%d" transform="rotate(%d %d %d)" fill="#0f172a" fill-opacity="%.2f" font-family="ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace" font-size="%d" font-weight="700" letter-spacing="1.5">%s</text>`,
			x, y, rotate, x, y, 0.84+source.Float64()*0.14, fontSize, escapeHTML(string(char)))
	}

	svg := fmt.Sprintf(`
<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="验证码">
  <defs>
    <linearGradient id="captcha-bg" x1="0%%" y1="0%%" x2="100%%" y2="100%%">
      <stop offset="0%%" stop-color="#f8fbff" />
      <stop offset="100%%" stop-color="#eef4ff" />
    </linearGradient>
    <filter id="soft-noise">
      <feTurbulence type="fractalNoise" baseFrequency="0.9" numOctaves="1" seed="%d" />
      <feColorMatrix type="saturate" values="0" />
      <feComponentTransfer>
        <feFuncA type="table" tableValues="0 0 0.08 0" />
      </feComponentTransfer>
    </filter>
  </defs>
  <rect x="0.75" y="0.75" width="%.1f" height="%.1f" rx="16" fill="url(#captcha-bg)" stroke="rgba(148,163,184,0.28)" />
  <rect x="0" y="0" width="%d" height="%d" rx="16" fill="white" filter="url(#soft-noise)" opacity="0.45" />
  %s
  %s
  %s
</svg>`, width, height, width, height, seed,
		float64(width)-1.5, float64(height)-1.5, width, height,
		strings.TrimSpace(circles.String()), strings.TrimSpace(paths.String()), strings.TrimSpace(glyphs.String()))

	return captchaSVGPrefix + base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(svg)))
}

// ── python-flavoured JSON ──

// pythonJSONDumps renders an ordered object the way json.dumps(..., ensure_ascii=False)
// does: ", " and ": " separators, no escaping beyond what JSON itself needs,
// non-ASCII kept literal. The value is embedded in a <script
// type="application/json"> element via |safe, so the encoder additionally escapes
// the three characters that can close that element (Django's json_script filter
// escapes the same three); release notes come from editable configuration and the
// changelog.
func pythonJSONDumps(pairs [][2]string) string {
	out := &strings.Builder{}
	out.WriteString("{")
	for index, pair := range pairs {
		if index > 0 {
			out.WriteString(", ")
		}
		out.WriteString(pythonJSONString(pair[0]))
		out.WriteString(": ")
		out.WriteString(pythonJSONString(pair[1]))
	}
	out.WriteString("}")
	return out.String()
}

func pythonJSONString(value string) string {
	out := &strings.Builder{}
	out.WriteByte('"')
	for _, char := range value {
		switch char {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '<', '>', '&':
			fmt.Fprintf(out, `\u%04x`, char)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			if char < 0x20 {
				fmt.Fprintf(out, `\u%04x`, char)
				continue
			}
			out.WriteRune(char)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// ── request context ──

func requestUser(user httpapi.Principal) map[string]any {
	if user == nil {
		return map[string]any{"is_authenticated": false, "username": "", "email": ""}
	}
	return map[string]any{
		"is_authenticated": true,
		"username":         user.Username(),
		"email":            user.Email(),
	}
}

func requestQuery(r *http.Request) map[string]any {
	values := map[string]any{}
	for key, entries := range r.URL.Query() {
		if len(entries) > 0 {
			values[key] = entries[0]
		}
	}
	return values
}

func csrfTokenOf(r *http.Request) string {
	if token := httpapi.CSRFTokenOf(r); token != nil {
		return token.Masked
	}
	return ""
}

// assetVersion is a stable identity for the shipped workspace assets. The
// reference derives it from asset file mtimes, which the rewrite explicitly
// drops, so it is a content digest here and never a timestamp.
func assetVersion(source fs.FS, release string) string {
	digest := sha256.New()
	if source != nil {
		names := []string{}
		_ = fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, err error) error {
			if err == nil && !entry.IsDir() {
				names = append(names, name)
			}
			return nil
		})
		sort.Strings(names)
		for _, name := range names {
			if content, err := fs.ReadFile(source, name); err == nil {
				digest.Write([]byte(name))
				digest.Write(content)
			}
		}
	}
	digest.Write([]byte("release:" + release))
	return hex.EncodeToString(digest.Sum(nil))[:16]
}
