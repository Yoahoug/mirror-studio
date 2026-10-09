package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mirror-studio/internal/db"
)

// SessionCookieName is the session cookie; SessionCookieMaxAge mirrors Django's
// default SESSION_COOKIE_AGE.
const (
	SessionCookieName   = "sessionid"
	SessionCookieMaxAge = 1209600
	CSRFCookieName      = "csrftoken"
	CSRFCookieMaxAge    = 31449600
	csrfSecretLength    = 32
	csrfTokenLength     = csrfSecretLength * 2
	csrfAllowedChars    = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// Session is a server-side session record persisted in app_session.
type Session struct {
	Key      string
	values   map[string]any
	IsNew    bool
	modified bool
	dirty    bool
	// hasKey mirrors the reference's _session_key: true while the session is
	// backed by a key, including a key the client presented whose row still
	// exists. It drops to false when the row is gone, expired, or the session
	// was flushed — which is what makes the session "empty".
	hasKey bool
}

// Get returns a string value from the session.
func (s *Session) Get(key string) string {
	if s == nil {
		return ""
	}
	value, ok := s.values[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return text
}

// Set stores a string value.
func (s *Session) Set(key, value string) {
	if s.values == nil {
		s.values = map[string]any{}
	}
	s.values[key] = value
	s.modified = true
}

// GetInt returns an integer session value.
func (s *Session) GetInt(key string) int64 {
	if s == nil {
		return 0
	}
	value, ok := s.values[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	}
	return 0
}

// SetInt stores an integer value.
func (s *Session) SetInt(key string, value int64) {
	if s.values == nil {
		s.values = map[string]any{}
	}
	s.values[key] = value
	s.modified = true
}

// Delete removes a key from the session.
func (s *Session) Delete(key string) {
	if s == nil || s.values == nil {
		return
	}
	if _, ok := s.values[key]; ok {
		delete(s.values, key)
		s.modified = true
	}
}

// Empty reports whether the session has neither a key nor data. It matches the
// reference's is_empty(): a request that merely carries a stale-but-present
// session cookie is NOT empty, so its cookie is left alone.
func (s *Session) Empty() bool { return s == nil || (!s.hasKey && len(s.values) == 0) }

// Flush empties the session and marks it for logging out.
func (s *Session) Flush() {
	s.values = map[string]any{}
	s.hasKey = false
	s.modified = true
	s.dirty = true
}

// CycleKey moves the session to a fresh key, which is what login does to
// prevent session fixation.
func (s *Session) CycleKey() {
	s.Key = newSessionKey()
	s.IsNew = true
	s.hasKey = true
	s.modified = true
}

func newSessionKey() string {
	buffer := make([]byte, csrfSecretLength)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return randomStringFrom(buffer)
}

func randomStringFrom(buffer []byte) string {
	builder := &strings.Builder{}
	for _, value := range buffer {
		builder.WriteByte(csrfAllowedChars[int(value)%len(csrfAllowedChars)])
	}
	return builder.String()
}

func randomString(length int) string {
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return randomStringFrom(buffer)
}

// SessionStore loads and saves sessions.
type SessionStore struct {
	DB     *db.DB
	Secure bool
	TTL    time.Duration
}

// NewSessionStore builds a store with Django's two-week session lifetime.
func NewSessionStore(handle *db.DB, secure bool) *SessionStore {
	return &SessionStore{DB: handle, Secure: secure, TTL: SessionCookieMaxAge * time.Second}
}

// Load returns the session referenced by the request cookie, or a fresh one.
func (s *SessionStore) Load(r *http.Request) *Session {
	session := &Session{values: map[string]any{}}
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		session.IsNew = true
		session.Key = newSessionKey()
		return session
	}
	session.Key = cookie.Value
	// The cookie names a session, so the reference's _session_key is set until a
	// lookup proves the row is gone or expired.
	session.hasKey = true

	var payload string
	var expires string
	row := s.DB.QueryRow(`SELECT data, expires_at FROM app_session WHERE session_key = ?`, session.Key)
	if err := row.Scan(&payload, &expires); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			session.IsNew = true
			session.Key = newSessionKey()
			session.hasKey = false
			return session
		}
		session.IsNew = true
		session.hasKey = false
		return session
	}
	if expiry, ok := db.ParseTime(expires); ok && expiry.Before(time.Now().UTC()) {
		// An expired row is dropped here; the reference's lookup sets
		// _session_key to None, so the session reads as empty and the client's
		// cookie is cleared on the way out.
		_, _ = s.DB.Exec(`DELETE FROM app_session WHERE session_key = ?`, session.Key)
		session.IsNew = true
		session.Key = newSessionKey()
		session.hasKey = false
		return session
	}
	values := map[string]any{}
	if strings.TrimSpace(payload) != "" {
		_ = json.Unmarshal([]byte(payload), &values)
	}
	session.values = values
	return session
}

// Save writes the session back when it changed.
func (s *SessionStore) Save(session *Session) error {
	if session == nil || !session.modified {
		return nil
	}
	if session.dirty {
		_, err := s.DB.Exec(`DELETE FROM app_session WHERE session_key = ?`, session.Key)
		return err
	}
	payload, err := json.Marshal(session.values)
	if err != nil {
		return err
	}
	expires := db.FormatTime(time.Now().Add(s.TTL))
	_, err = s.DB.Exec(
		`INSERT INTO app_session (session_key, data, expires_at) VALUES (?, ?, ?)
		 ON CONFLICT(session_key) DO UPDATE SET data = excluded.data, expires_at = excluded.expires_at`,
		session.Key, string(payload), expires,
	)
	session.modified = false
	session.hasKey = true
	return nil
}

// PurgeExpired removes sessions past their expiry. It runs from the background
// loop so the table cannot grow without bound.
func (s *SessionStore) PurgeExpired() (int64, error) {
	result, err := s.DB.Exec(`DELETE FROM app_session WHERE expires_at <= ?`, db.FormatTime(time.Now()))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ── CSRF ──

// CSRFToken is the per-request CSRF material: a 32-character secret and the
// 64-character masked cookie value Django exposes to templates and the SPA.
type CSRFToken struct {
	Secret string
	Masked string
	Fresh  bool
}

// NewCSRFToken creates fresh material.
func NewCSRFToken() *CSRFToken {
	secret := randomString(csrfSecretLength)
	return &CSRFToken{Secret: secret, Masked: maskCSRFSecret(secret), Fresh: true}
}

// tokenFromCookie converts an existing cookie value into usable material.
func tokenFromCookie(value string) *CSRFToken {
	if len(value) != csrfTokenLength || !isAllowedCSRF(value) {
		return nil
	}
	return &CSRFToken{Secret: unmaskCSRFToken(value), Masked: value}
}

func maskCSRFSecret(secret string) string {
	mask := randomString(csrfSecretLength)
	builder := &strings.Builder{}
	for index := 0; index < csrfSecretLength; index++ {
		x := strings.IndexByte(csrfAllowedChars, secret[index])
		y := strings.IndexByte(csrfAllowedChars, mask[index])
		builder.WriteByte(csrfAllowedChars[(x+y)%len(csrfAllowedChars)])
	}
	return mask + builder.String()
}

func unmaskCSRFToken(token string) string {
	mask, cipher := token[:csrfSecretLength], token[csrfSecretLength:]
	builder := &strings.Builder{}
	for index := 0; index < csrfSecretLength; index++ {
		x := strings.IndexByte(csrfAllowedChars, mask[index])
		y := strings.IndexByte(csrfAllowedChars, cipher[index])
		builder.WriteByte(csrfAllowedChars[((x-y)%len(csrfAllowedChars)+len(csrfAllowedChars))%len(csrfAllowedChars)])
	}
	return builder.String()
}

func isAllowedCSRF(value string) bool {
	for index := 0; index < len(value); index++ {
		if strings.IndexByte(csrfAllowedChars, value[index]) < 0 {
			return false
		}
	}
	return true
}

// VerifyCSRF checks the request token against the cookie secret the same way
// Django does: the header may carry either the masked value or the raw secret.
func VerifyCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil {
		return false
	}
	material := tokenFromCookie(cookie.Value)
	if material == nil {
		return false
	}
	requestToken := r.Header.Get("X-CSRFToken")
	if requestToken == "" {
		requestToken = r.FormValue("csrfmiddlewaretoken")
	}
	if requestToken == "" {
		return false
	}
	if len(requestToken) == csrfTokenLength && isAllowedCSRF(requestToken) {
		requestToken = unmaskCSRFToken(requestToken)
	} else if len(requestToken) != csrfSecretLength || !isAllowedCSRF(requestToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(requestToken), []byte(material.Secret)) == 1
}

// VerifyCSRFOrigin ports Django's Origin/Referer check (csrf.py
// _origin_verified + _check_referer): an Origin header must equal
// scheme://host of the request or an entry of the trusted list; a secure
// request without Origin must present a same-host Referer. isSecure reports
// whether the request arrived over https (socket or trusted proxy header),
// and host is the request's Host header.
func VerifyCSRFOrigin(r *http.Request, trustedOrigins []string, isSecure bool) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		if host := r.Host; host != "" {
			scheme := "http"
			if isSecure {
				scheme = "https"
			}
			if origin == scheme+"://"+host {
				return true
			}
		}
		for _, trusted := range trustedOrigins {
			if origin == strings.TrimRight(strings.TrimSpace(trusted), "/") {
				return true
			}
		}
		return false
	}
	if !isSecure {
		return true
	}
	// No Origin on an https request: Django requires a Referer whose host
	// matches the request host.
	referer := strings.TrimSpace(r.Header.Get("Referer"))
	if referer == "" {
		return false
	}
	parsed, err := url.Parse(referer)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

// ── helpers shared with form handling ──

// ErrRequestTooBig reports a body larger than DATA_UPLOAD_MAX_MEMORY_SIZE.
// The reference raises RequestDataTooBig, which the generator endpoint turns
// into a 400100 response and every other endpoint lets fall through as HTTP 400.
var ErrRequestTooBig = errors.New("request body exceeded the maximum size")

// ParsePayload mirrors the reference payload parser: a JSON object for JSON
// requests, otherwise the parsed form body.
func ParsePayload(r *http.Request) (map[string]any, error) {
	if state := From(r); state.TooBig {
		return nil, ErrRequestTooBig
	}
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		body := From(r).Body
		if body == nil {
			var err error
			body, err = io.ReadAll(r.Body)
			if err != nil {
				return nil, errors.New("请求 JSON 格式错误")
			}
		}
		if len(strings.TrimSpace(string(body))) == 0 {
			return map[string]any{}, nil
		}
		decoded := map[string]any{}
		if err := json.Unmarshal(body, &decoded); err != nil {
			return nil, errors.New("请求 JSON 格式错误")
		}
		return decoded, nil
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil && r.PostForm == nil {
		return nil, errors.New("请求参数格式错误")
	}
	result := map[string]any{}
	for key, values := range r.PostForm {
		if len(values) > 0 {
			result[key] = values[0]
		}
	}
	return result, nil
}

// ClientIPFromRequest resolves the caller address, honouring
// X-Forwarded-For only when the deployment declares a trusted proxy.
func ClientIPFromRequest(r *http.Request, trustForwarded bool) string {
	if trustForwarded {
		if forwarded := r.Header.Get("X-Forwarded-For"); strings.TrimSpace(forwarded) != "" {
			first, _, _ := strings.Cut(forwarded, ",")
			if trimmed := strings.TrimSpace(first); trimmed != "" {
				return trimmed
			}
		}
	}
	host := r.RemoteAddr
	if strings.HasPrefix(host, "[") {
		if end := strings.Index(host, "]"); end > 0 {
			return host[1:end]
		}
	}
	if colon := strings.LastIndex(host, ":"); colon > 0 {
		return host[:colon]
	}
	return host
}

// RedirectTo builds a relative redirect target.
func RedirectTo(w http.ResponseWriter, r *http.Request, target string) {
	http.Redirect(w, r, target, http.StatusFound)
}

// QueryEscape exposes url escaping for handlers that build download links.
func QueryEscape(value string) string { return url.QueryEscape(value) }

// PathEscapePercent encodes a path segment the way python's urllib.parse.quote
// does with the default safe set, which keeps ":" and "=" escaped.
func PathEscapePercent(value string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-~/"
	builder := &strings.Builder{}
	for _, char := range []byte(value) {
		if strings.IndexByte(unreserved, char) >= 0 {
			builder.WriteByte(char)
			continue
		}
		builder.WriteString("%" + strings.ToUpper(string([]byte{hexDigit(char >> 4), hexDigit(char & 0x0f)})))
	}
	return builder.String()
}

func hexDigit(value byte) byte {
	if value < 10 {
		return '0' + value
	}
	return 'a' + value - 10
}
