package httpapi

import (
	"net/http"
	"sort"
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

func normalizeAllow(values []string) string {
	methods := []string{}
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				methods = append(methods, strings.ToUpper(trimmed))
			}
		}
	}
	methods = append(methods, "HEAD", "OPTIONS")
	sort.Strings(methods)
	unique := make([]string, 0, len(methods))
	for index, method := range methods {
		if index == 0 || methods[index-1] != method {
			unique = append(unique, method)
		}
	}
	return strings.Join(unique, ", ")
}
