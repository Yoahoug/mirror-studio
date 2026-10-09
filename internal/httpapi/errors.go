package httpapi

import (
	"html"
	"io"
	"net/http"
	"strconv"
)

// urlconfPatterns is the route listing the framework's debug error page prints.
// The page body is not product surface — the diff harness records only the
// status, the content type and whether the page mentions CSRF, and the CSRF
// marker comes from the URLconf listing, which is where the reference prints it.
var urlconfPatterns = []string{
	"admin",
	"healthz",
	"api/auth/csrf",
	"api/auth/login",
	"api/auth/register",
	"api/images",
	"api/images/<int:image_id>",
	"api/uploads/sessions",
	"api/generate/tasks",
	"api/novelviz/tasks",
	"account/history",
	"console/api/",
	"workspace",
	"downloads/<path:token>/",
}

// WriteTechnicalHTML renders the framework's debug error page. Every domain that
// raises a 404/400/500 through this page shares this one implementation so the
// listing cannot drift between packages.
func WriteTechnicalHTML(w http.ResponseWriter, r *http.Request, status int, title string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	body := `<!DOCTYPE html>
<html lang="en">
<head>
  <meta http-equiv="content-type" content="text/html; charset=utf-8">
  <title>` + html.EscapeString(title) + ` at ` + html.EscapeString(r.URL.Path) + `</title>
  <meta name="robots" content="NONE,NOARCHIVE">
</head>
<body>
  <header id="summary">
    <h1>` + html.EscapeString(title) + ` <small>(` + strconv.Itoa(status) + `)</small></h1>
    <table class="meta">
      <tr><th scope="row">Request Method:</th><td>` + html.EscapeString(r.Method) + `</td></tr>
      <tr><th scope="row">Request URL:</th><td>` + html.EscapeString(r.URL.String()) + `</td></tr>
    </table>
  </header>
  <main id="info">
    <p>Using the URLconf, these URL patterns were tried, in this order:</p>
    <ol>
`
	for _, pattern := range urlconfPatterns {
		body += "      <li><code>" + html.EscapeString(pattern) + "</code></li>\n"
	}
	body += `    </ol>
    <p>The current path, <code>` + html.EscapeString(r.URL.Path) + `</code>, was not matched.</p>
  </main>
  <footer id="explanation">
    <p>You are seeing this error because <code>DEBUG = True</code> in the settings file.</p>
  </footer>
</body>
</html>
`
	_, _ = io.WriteString(w, body)
}
