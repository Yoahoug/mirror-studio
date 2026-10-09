package httpapi

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// staticTypes maps the extensions the build produces onto the exact
// Content-Type values the reference static handler sends. Go's mime package
// appends "; charset=utf-8", which the reference does not, so the mapping is
// explicit rather than inferred.
var staticTypes = map[string]string{
	".js":    "text/javascript",
	".mjs":   "text/javascript",
	".css":   "text/css",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".webp":  "image/webp",
	".ico":   "image/x-icon",
	".json":  "application/json",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".map":   "application/json",
	".txt":   "text/plain",
	".html":  "text/html",
}

// StaticHandler serves the embedded build output under /static/.
type StaticHandler struct {
	FS   fs.FS
	Root string
}

// NewStaticHandler builds a handler over an embedded filesystem rooted at dir.
func NewStaticHandler(assets fs.FS, dir string) *StaticHandler {
	return &StaticHandler{FS: assets, Root: dir}
}

// ServeHTTP serves one asset. Only GET and HEAD are allowed; unknown paths are
// answered with the framework's 404.
func (s *StaticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.FS == nil {
		notFoundHTML(w)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		notAllowedHTML(w)
		return
	}
	requestPath := strings.TrimPrefix(r.URL.Path, "/static/")
	clean := path.Clean("/" + requestPath)
	if strings.Contains(clean, "..") {
		notFoundHTML(w)
		return
	}
	name := path.Join(s.Root, clean)
	file, err := s.FS.Open(name)
	if err != nil {
		notFoundHTML(w)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		notFoundHTML(w)
		return
	}
	extension := strings.ToLower(path.Ext(name))
	contentType, ok := staticTypes[extension]
	if !ok {
		contentType = mime.TypeByExtension(extension)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `inline; filename="`+path.Base(name)+`"`)
	w.Header().Set("Content-Length", formatInt(info.Size()))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, file)
}

// Django renders its own 404 and 405 pages; only the status and content type
// are part of the contract, so the bodies stay empty.
func notFoundHTML(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
}

func notAllowedHTML(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func formatInt(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buffer := make([]byte, 0, 20)
	for value > 0 {
		buffer = append([]byte{byte('0' + value%10)}, buffer...)
		value /= 10
	}
	if negative {
		return "-" + string(buffer)
	}
	return string(buffer)
}
