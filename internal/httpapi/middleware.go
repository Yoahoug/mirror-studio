package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mirror-studio/internal/config"
)

// Principal is the authenticated user as the transport layer needs to see it.
type Principal interface {
	ID() int64
	Username() string
	IsOperator() bool
	IsActiveStatus() bool
	Email() string
}

// ctxKey is the private type used to store the request context value.
type ctxKey struct{}

// Ctx carries everything a handler needs beyond the raw request.
type Ctx struct {
	RequestID string
	ClientIP  string
	Session   *Session
	CSRF      *CSRFToken
	User      Principal
	Body      []byte
	TooBig    bool
}

// From returns the request context attached by the middleware chain.
func From(r *http.Request) *Ctx {
	value, _ := r.Context().Value(ctxKey{}).(*Ctx)
	if value == nil {
		return &Ctx{}
	}
	return value
}

// UserOf returns the authenticated user, or nil for anonymous requests.
func UserOf(r *http.Request) Principal { return From(r).User }

// SessionOf returns the request session.
func SessionOf(r *http.Request) *Session { return From(r).Session }

// CSRFTokenOf returns the request CSRF material.
func CSRFTokenOf(r *http.Request) *CSRFToken { return From(r).CSRF }

// Options wires the middleware chain to the domain packages that own the
// policies it enforces.
type Options struct {
	Config      *config.Config
	Logger      *slog.Logger
	Sessions    *SessionStore
	ResolveUser func(ctx context.Context, userID int64) (Principal, error)
	IPBlocked   func(ctx context.Context, ip string) (bool, string)
	OnRequestIP func(ctx context.Context, ip string)
	// TrustedOrigins is CSRF_TRUSTED_ORIGINS: exact origins the Origin check
	// accepts in addition to the request's own scheme://host.
	TrustedOrigins []string
}

const maxBodyBytes = 4 * 1024 * 1024 // DATA_UPLOAD_MAX_MEMORY_SIZE default

// bodyCap bounds how much of a request body is buffered in memory. Ordinary
// payloads keep the reference's DATA_UPLOAD_MAX_MEMORY_SIZE (4 MiB), but the
// upload routes must be able to carry what the deployment advertises: a full
// RESUMABLE_UPLOAD_CHUNK_SIZE chunk or an EPUB up to
// NOVELVIZ_MAX_EPUB_UPLOAD_BYTES. With the plain 4 MiB cap the default 8 MiB
// chunk size made every upload above 4 MiB impossible — each full chunk was
// truncated and then rejected for not matching its own chunk size.
func bodyCap(cfg *config.Config) int {
	limit := maxBodyBytes
	if cfg == nil {
		return limit
	}
	if chunk := int(cfg.ResumableUploadChunkSize); chunk > limit {
		limit = chunk
	}
	if epub := int(cfg.NovelvizMaxEpubBytes); epub > limit {
		limit = epub
	}
	return limit
}

// Middleware is the composable filter chain every request passes through.
type Middleware struct {
	options Options

	// RateLimiter counts a key within a sliding window; nil disables limiting.
	RateLimiter func(key string, window time.Duration) int64

	Handlers http.Handler // domain router
	Static   http.Handler // embedded assets
}

// NewMiddleware builds the chain around the given terminal handler.
func NewMiddleware(options Options, root http.Handler) *Middleware {
	return &Middleware{options: options, Handlers: root}
}

// ServeHTTP implements the whole pipeline in the order the reference
// implementation resolves it:
//
//	request id -> security headers -> body capture -> session -> user ->
//	IP block -> console guard -> CSRF -> router
func (m *Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cfg := m.options.Config

	// SECURE_SSL_REDIRECT: outside DEBUG a plain-HTTP request is permanently
	// sent to the https origin, the way SecurityMiddleware does it in the
	// reference's production settings. X-Forwarded-Proto decides whether the
	// hop in front of us already encrypted the connection.
	if cfg != nil && !cfg.Debug && cfg.SecureSSLRedirect && cfg.PublicOrigin != "" && !requestIsSecure(r, cfg.SecureProxySSLHeaderValue) {
		target := cfg.PublicOrigin + r.URL.RequestURI()
		w.Header().Set("Location", target)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusMovedPermanently)
		return
	}

	requestID := r.Header.Get("X-Request-Id")
	if strings.TrimSpace(requestID) == "" {
		requestID = randomHex(8)
	}
	clientIP := ClientIPFromRequest(r, cfg.TrustForwardedFor)

	state := &Ctx{RequestID: requestID, ClientIP: clientIP}
	writer := &responseWriter{
		ResponseWriter: w, status: 200, ctx: state, cfg: cfg,
		sessions: m.options.Sessions, logger: m.options.Logger,
	}

	if cfg.TrustForwardedFor && m.options.OnRequestIP != nil && clientIP != "" {
		m.options.OnRequestIP(r.Context(), clientIP)
	}

	// Body capture: the payload parser and CSRF both need the raw bytes, and
	// Django's request.body is readable more than once.
	if r.Body != nil {
		limit := bodyCap(cfg)
		limited := io.LimitReader(r.Body, int64(limit)+1)
		body, err := io.ReadAll(limited)
		if err != nil {
			m.HTMLStatus(writer, r, http.StatusBadRequest)
			return
		}
		if len(body) > limit {
			state.TooBig = true
			body = body[:limit]
		}
		state.Body = body
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	// A multipart body is parsed from the captured bytes so handlers can read
	// form values and files without re-reading the socket.
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, state))

	state.Session = m.options.Sessions.Load(r)
	if _, err := r.Cookie(SessionCookieName); err == nil {
		writer.hadSessionCookie = true
	}

	// CSRF material comes from the cookie, or is minted fresh. Django only sets
	// the cookie on responses that ask for it (ensure_csrf_cookie) or on any
	// template render that uses {% csrf_token %}.
	if cookie, err := r.Cookie(CSRFCookieName); err == nil {
		if material := tokenFromCookie(cookie.Value); material != nil {
			state.CSRF = material
		}
	}
	if state.CSRF == nil {
		// Mint a token for this request only. Django's CsrfViewMiddleware keeps
		// it in request.META without emitting a cookie; the cookie appears only
		// when the token is actually used (a {% csrf_token %} render, or a view
		// decorated with ensure_csrf_cookie).
		state.CSRF = NewCSRFToken()
	}

	// Resolve the authenticated user from the session.
	if userID := state.Session.GetInt("user_id"); userID > 0 && m.options.ResolveUser != nil {
		user, err := m.options.ResolveUser(r.Context(), userID)
		if err == nil {
			// Django's _auth_user_hash: a session minted under an older password
			// must die when the password changes, so the stored hash is compared
			// on every request. A session that predates the check (no key yet)
			// is treated like a fresh login and re-armed.
			if expected, ok := user.(interface{ SessionAuthHash() string }); ok {
				stored := state.Session.Get("session_auth_hash")
				current := expected.SessionAuthHash()
				switch {
				case stored == "":
					// A session that predates the check is armed like a fresh
					// login; arming marks the session modified, which is what
					// Django's get_user does on the same transition.
					state.Session.Set("session_auth_hash", current)
				case stored != current:
					state.Session.Flush()
					state.User = nil
					user = nil
				}
			}
			if user != nil {
				// Django's ModelBackend.get_user returns None for an account that
				// may not authenticate (is_active = 0), so a deactivated account
				// resolves to anonymous on every request and its existing session
				// stops working — not just new logins.
				if account, ok := user.(interface{ IsActiveFlag() bool }); ok && !account.IsActiveFlag() {
					state.Session.Flush()
					user = nil
				}
			}
			if user != nil {
				state.User = user
			}
		}
	}

	path := r.URL.Path

	// IP block applies to every path, including /healthz and unmatched URLs.
	if m.options.IPBlocked != nil {
		if blocked, message := m.options.IPBlocked(r.Context(), clientIP); blocked {
			m.htmlError(writer, message)
			return
		}
	}

	// Console guard: only /console/api/ is inspected; the note about ordering is
	// in the reference middleware and matters because the guard's error
	// responses intentionally carry no Cache-Control headers.
	if strings.HasPrefix(path, "/console/api/") {
		if handled := m.consoleGuard(writer, r, state); handled {
			return
		}
		writer.consoleHeaders = true
	}

	// CSRF check for unsafe methods, after the guards so a blocked caller never
	// reaches it. The token check runs first, then the Origin/Referer check
	// Django layers on top (csrf.py rejects a bad Origin before the token is
	// even looked at; keeping both produces the same accept/reject set).
	if !isSafeMethod(r.Method) && !pathHasPrefix(path, "/static/") {
		isSecure := requestIsSecure(r, cfg.SecureProxySSLHeaderValue)
		if !VerifyCSRF(r) || !VerifyCSRFOrigin(r, m.options.TrustedOrigins, isSecure) {
			m.csrfFailure(writer, r)
			return
		}
	}

	if strings.HasPrefix(path, "/static/") && m.Static != nil {
		// Static files are served by the asset layer, which in the reference sits
		// outside the security middleware: these responses carry only the content
		// type and disposition, no nosniff/frame-options and no request id.
		writer.staticOnly = true
		m.Static.ServeHTTP(writer, r)
		return
	}

	m.Handlers.ServeHTTP(writer, r)
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

// requestIsSecure reports whether the request already arrived over https, as
// far as the deployment can tell: either the socket is TLS or the trusted
// proxy header says so.
func requestIsSecure(r *http.Request, proxyHeaderValue string) bool {
	if r.TLS != nil {
		return true
	}
	if proxyHeaderValue == "" {
		proxyHeaderValue = "https"
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), proxyHeaderValue)
}

func pathHasPrefix(path, prefix string) bool { return strings.HasPrefix(path, prefix) }

func randomHex(bytesCount int) string {
	buffer := make([]byte, bytesCount)
	if _, err := rand.Read(buffer); err != nil {
		return strings.Repeat("0", bytesCount*2)
	}
	return hex.EncodeToString(buffer)
}

// responseWriter applies the cross-cutting response headers and persists the
// session once the handler is done.
type responseWriter struct {
	http.ResponseWriter
	status           int
	wroteHeader      bool
	ctx              *Ctx
	cfg              *config.Config
	setCSRFCookie    bool
	clearSession     bool
	hadSessionCookie bool
	rotateSession    bool
	consoleHeaders   bool
	sessions         *SessionStore
	logger           *slog.Logger
	staticOnly       bool
}

func (w *responseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.applyHeaders()
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(payload []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(payload)
}

// Flush forwards to the underlying writer so server-sent events stream.
func (w *responseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Unwrap lets http.ResponseController reach the real writer.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseWriter) applyHeaders() {
	header := w.Header()
	if w.staticOnly {
		// The reference's static handler sits outside SecurityMiddleware in DEV
		// and the differential corpus pins those bare headers; production serves
		// static through WhiteNoise behind SecurityMiddleware, which does add
		// them, so the rewrite reproduces that split.
		if w.cfg != nil && !w.cfg.Debug {
			header.Set("X-Content-Type-Options", "nosniff")
			header.Set("Referrer-Policy", "same-origin")
			header.Set("Cross-Origin-Opener-Policy", "same-origin")
		}
		return
	}
	if w.ctx != nil {
		header.Set("X-Request-Id", w.ctx.RequestID)
	}
	if w.consoleHeaders {
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		header.Set("Pragma", "no-cache")
		header.Set("X-Robots-Tag", "noindex, nofollow")
	} else {
		if header.Get("X-Content-Type-Options") == "" {
			header.Set("X-Content-Type-Options", "nosniff")
		}
		if header.Get("X-Frame-Options") == "" {
			header.Set("X-Frame-Options", "DENY")
		}
	}
	if w.cfg != nil && !w.cfg.Debug {
		header.Set("Referrer-Policy", "same-origin")
		header.Set("Cross-Origin-Opener-Policy", "same-origin")
		header.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}
	// Session persistence follows django.contrib.sessions.middleware: the cookie
	// and the row appear only when the session was modified during the request,
	// and a session that became empty clears a cookie the client already had.
	if w.clearSession {
		w.clearSessionCookie()
	} else if w.ctx != nil && w.ctx.Session != nil {
		session := w.ctx.Session
		modified := session.modified
		if modified && w.sessions != nil {
			if err := w.sessions.Save(session); err != nil && w.logger != nil {
				w.logger.Error("session 保存失败", "request_id", w.ctx.RequestID, "error", err)
			}
		}
		switch {
		case session.Empty():
			// Either the row was gone or expired, or the handler flushed the
			// session: the reference drops the client's cookie in both cases.
			if w.hadSessionCookie {
				w.clearSessionCookie()
			}
		case modified:
			w.writeSessionCookie(session.Key, SessionCookieMaxAge)
		}
	}
	if w.setCSRFCookie && w.ctx != nil && w.ctx.CSRF != nil {
		w.writeCSRFCookie(w.ctx.CSRF.Masked)
	}
}

func (w *responseWriter) writeSessionCookie(value string, maxAge int) {
	cookie := &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   w.cfg != nil && !w.cfg.Debug,
	}
	http.SetCookie(w.ResponseWriter, cookie)
}

func (w *responseWriter) clearSessionCookie() {
	cookie := &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		SameSite: http.SameSiteLaxMode,
		Secure:   w.cfg != nil && !w.cfg.Debug,
	}
	http.SetCookie(w.ResponseWriter, cookie)
}

func (w *responseWriter) writeCSRFCookie(value string) {
	cookie := &http.Cookie{
		Name:     CSRFCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   CSRFCookieMaxAge,
		SameSite: http.SameSiteLaxMode,
		Secure:   w.cfg != nil && !w.cfg.Debug,
	}
	http.SetCookie(w.ResponseWriter, cookie)
}

// responseWriterFor walks the Unwrap chain to the middleware's own writer.
// Handlers sit behind NewRouter, whose mismatchWriter (and any http.ResponseController
// wrapper) would otherwise hide it from a direct type assertion.
func responseWriterFor(w http.ResponseWriter) *responseWriter {
	for w != nil {
		if writer, ok := w.(*responseWriter); ok {
			return writer
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil
		}
		w = unwrapper.Unwrap()
	}
	return nil
}

// SetCSRFCookie asks the response to (re)issue the CSRF cookie, which is what
// GET /api/auth/csrf, POST /api/auth/login and the SSR templates do.
func SetCSRFCookie(w http.ResponseWriter) {
	if writer := responseWriterFor(w); writer != nil {
		writer.setCSRFCookie = true
	}
}

// RotateCSRFToken replaces the request's CSRF material with a fresh token and
// marks the response to carry the new cookie, which is what Django's
// rotate_token does inside login(). The old cookie value stops verifying
// immediately, so the next mutating request must use the new token.
func RotateCSRFToken(w http.ResponseWriter, r *http.Request) {
	writer := responseWriterFor(w)
	if writer == nil || writer.ctx == nil {
		return
	}
	writer.ctx.CSRF = NewCSRFToken()
	writer.setCSRFCookie = true
	// A form that rendered before login carries the old token; Django accepts
	// that a login response must be fully reloaded, and so do we.
	_ = r
}

// ClearSession drops the session cookie and record, which is what logout does.
func ClearSession(w http.ResponseWriter, session *Session) {
	if writer := responseWriterFor(w); writer != nil {
		writer.clearSession = true
	}
	if session != nil {
		session.Flush()
	}
}

// SessionModified marks the session for persistence. Handlers that write to the
// session through a wrapper that is not the middleware's own writer (there are
// none today, but the router chain makes it possible) can use this.
func SessionModified(w http.ResponseWriter) {
	if writer := responseWriterFor(w); writer != nil && writer.ctx != nil && writer.ctx.Session != nil {
		writer.ctx.Session.modified = true
	}
}

// htmlError renders a bare text/html error body. Django raises PermissionDenied
// with this message and the body must not contain "403", "csrf" or "CSRF"
// because the differential harness treats those substrings as a CSRF rejection.
func (m *Middleware) htmlError(w http.ResponseWriter, message string) {
	body := escapeHTML(message)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = io.WriteString(w, body)
}

// HTMLStatus writes an empty HTML response with the given status, used when a
// request cannot even be read.
func (m *Middleware) HTMLStatus(w http.ResponseWriter, r *http.Request, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
}

// csrfFailure renders the framework's CSRF rejection page. Only the status and
// the presence of the CSRF marker are part of the contract.
func (m *Middleware) csrfFailure(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = io.WriteString(w, `<!DOCTYPE html>
<html lang="zh-hans">
<head><title>403 Forbidden</title></head>
<body>
<h1>403 Forbidden</h1>
<p>CSRF verification failed. Request aborted.</p>
</body>
</html>`)
	if m.options.Logger != nil {
		m.options.Logger.Warn("csrf verification failed",
			"request_id", From(r).RequestID, "method", r.Method, "path", r.URL.Path)
	}
}

func escapeHTML(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;")
	return replacer.Replace(value)
}

// consoleGuard enforces the operator checks and the per-account rate limit for
// /console/api/. It reports whether the request was already answered.
func (m *Middleware) consoleGuard(w http.ResponseWriter, r *http.Request, state *Ctx) bool {
	if state.User == nil {
		Error(w, http.StatusUnauthorized, CodeConsoleUnauthorized, "未登录")
		return true
	}
	if !state.User.IsActiveStatus() {
		Error(w, http.StatusForbidden, CodeConsoleInactive, "当前账号状态不可用")
		return true
	}
	if !state.User.IsOperator() {
		Error(w, http.StatusForbidden, CodeConsoleForbidden, "权限不足")
		return true
	}
	if limit := m.options.Config.ConsoleAPIRateLimit; limit > 0 && m.RateLimiter != nil {
		key := "console_rate:" + strconv.FormatInt(state.User.ID(), 10)
		if m.RateLimiter(key, time.Minute) > int64(limit) {
			Error(w, http.StatusTooManyRequests, CodeConsoleRateLimited, "请求过于频繁，请稍后再试")
			return true
		}
	}
	return false
}
