package httpapi

import (
	"net/http"
	"strings"
)

// NewRouter wraps a ServeMux so method mismatches answer the way the reference
// framework does: HTTP 405 with an HTML content type and an Allow header that
// also lists HEAD and OPTIONS.
//
// The wrapper never buffers the body: streaming endpoints must keep flushing.
func NewRouter(mux *http.ServeMux) http.Handler {
	return &router{inner: mux}
}

type router struct{ inner *http.ServeMux }

func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt.inner.ServeHTTP(&mismatchWriter{ResponseWriter: w}, r)
}

type mismatchWriter struct {
	http.ResponseWriter
	mismatch bool
	wrote    bool
}

func (m *mismatchWriter) WriteHeader(status int) {
	if m.wrote {
		return
	}
	m.wrote = true
	if status == http.StatusMethodNotAllowed {
		m.mismatch = true
		allowed := m.ResponseWriter.Header().Values("Allow")
		m.ResponseWriter.Header().Set("Allow", normalizeAllow(allowed))
		m.ResponseWriter.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	m.ResponseWriter.WriteHeader(status)
}

func (m *mismatchWriter) Write(payload []byte) (int, error) {
	if !m.wrote {
		m.WriteHeader(http.StatusOK)
	}
	if m.mismatch {
		// The framework's own "Method Not Allowed" body is replaced by the
		// reference's empty HTML page.
		return len(payload), nil
	}
	return m.ResponseWriter.Write(payload)
}

// Flush forwards to the underlying writer so SSE keeps streaming.
func (m *mismatchWriter) Flush() {
	if flusher, ok := m.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the real writer.
func (m *mismatchWriter) Unwrap() http.ResponseWriter { return m.ResponseWriter }

// djangoMethodNames is http_method_names as the reference declares it. Django's
// View._allowed_methods() walks that table in order and adds HEAD (injected by
// setup()) and OPTIONS (always defined), so the Allow header carries that order —
// the differential harness records the header verbatim.
var djangoMethodNames = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"}

func normalizeAllow(values []string) string {
	seen := map[string]bool{}
	extra := []string{}
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			method := strings.ToUpper(strings.TrimSpace(item))
			if method == "" || seen[method] {
				continue
			}
			seen[method] = true
			known := false
			for _, name := range djangoMethodNames {
				if name == method {
					known = true
					break
				}
			}
			if !known {
				extra = append(extra, method)
			}
		}
	}
	seen["HEAD"] = true
	seen["OPTIONS"] = true
	methods := make([]string, 0, len(seen))
	for _, name := range djangoMethodNames {
		if seen[name] {
			methods = append(methods, name)
		}
	}
	return strings.Join(append(methods, extra...), ", ")
}
