package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"math"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"

	"mirror-studio/internal/config"
)

// Email-verification failures. The HTTP layer maps the first three onto
// 500/500001, 429/400429 and 502/500002 respectively.
type (
	// ConfigError is an ImproperlyConfigured equivalent.
	ConfigError struct{ Message string }
	// RateLimitError is a PermissionError equivalent.
	RateLimitError struct{ Message string }
	// SendError is an EmailVerificationSendError equivalent.
	SendError struct{ Message string }
)

func (e *ConfigError) Error() string    { return e.Message }
func (e *RateLimitError) Error() string { return e.Message }
func (e *SendError) Error() string      { return e.Message }

// Errors raised while validating a code.
var (
	ErrEmailVerificationDisabled = &ConfigError{Message: "当前未启用邮箱验证码注册"}
	ErrEmailCodeExpired          = fmt.Errorf("验证码不存在或已过期")
	ErrEmailCodeInvalid          = fmt.Errorf("邮箱验证码错误")
)

// EmailCodeResult is the payload of a successful send.
type EmailCodeResult struct {
	ExpiresIn int
	ResendIn  int
}

type emailCodeEntry struct {
	hash      string
	expiresAt time.Time
}

type dailyEntry struct {
	count     int
	expiresAt time.Time
}

// emailCodeStore is the in-process replacement for the
// django.core.cache LocMemCache the reference kept codes and send counters in.
// Single process is also why the review's P1-5 note is satisfied: one shared
// store instead of one per worker.
type emailCodeStore struct {
	mu        sync.Mutex
	codes     map[string]emailCodeEntry
	cooldowns map[string]time.Time
	daily     map[string]dailyEntry
}

func newEmailCodeStore() *emailCodeStore {
	return &emailCodeStore{
		codes:     map[string]emailCodeEntry{},
		cooldowns: map[string]time.Time{},
		daily:     map[string]dailyEntry{},
	}
}

func (s *emailCodeStore) storeCode(email, codeHash string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(time.Now())
	s.codes[email] = emailCodeEntry{hash: codeHash, expiresAt: time.Now().Add(ttl)}
}

func (s *emailCodeStore) takeCode(email string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.codes[email]
	if !ok {
		return "", false
	}
	delete(s.codes, email)
	if time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.hash, true
}

func (s *emailCodeStore) peekCode(email string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.codes[email]
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.hash, true
}

func (s *emailCodeStore) cooldownRemaining(email string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.cooldowns[email]
	if !ok {
		return 0
	}
	return time.Until(until)
}

func (s *emailCodeStore) setCooldown(email string, window time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cooldowns[email] = time.Now().Add(window)
}

func (s *emailCodeStore) dailyCount(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.daily[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return 0
	}
	return entry.count
}

func (s *emailCodeStore) incrementDaily(key string, window time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(time.Now())
	entry := s.daily[key]
	if time.Now().After(entry.expiresAt) {
		entry = dailyEntry{expiresAt: time.Now().Add(window)}
	}
	entry.count++
	s.daily[key] = entry
}

// sweepLocked drops expired entries so the maps stay bounded.
func (s *emailCodeStore) sweepLocked(now time.Time) {
	for key, entry := range s.codes {
		if now.After(entry.expiresAt) {
			delete(s.codes, key)
		}
	}
	for key, until := range s.cooldowns {
		if now.After(until) {
			delete(s.cooldowns, key)
		}
	}
	for key, entry := range s.daily {
		if now.After(entry.expiresAt) {
			delete(s.daily, key)
		}
	}
}

// ── configuration ──

// EmailVerificationEnabled resolves email.verification_enabled (DB → env →
// default).
func (s *Service) EmailVerificationEnabled() bool {
	return s.Site.Bool("email.verification_enabled", s.Cfg.EmailVerification)
}

// EmailCodeLength resolves email.verification_code_length.
func (s *Service) EmailCodeLength() int {
	return s.Site.Int("email.verification_code_length", s.Cfg.EmailCodeLength)
}

// EmailCodeTTLSeconds resolves email.verification_ttl_seconds.
func (s *Service) EmailCodeTTLSeconds() int {
	return s.Site.Int("email.verification_ttl_seconds", int(s.Cfg.EmailCodeTTL/time.Second))
}

// EmailResendSeconds resolves email.verification_resend_seconds.
func (s *Service) EmailResendSeconds() int {
	return s.Site.Int("email.verification_resend_seconds", int(s.Cfg.EmailResendInterval/time.Second))
}

// EmailDailyLimitPerEmail resolves email.daily_limit_per_email.
func (s *Service) EmailDailyLimitPerEmail() int {
	return s.Site.Int("email.daily_limit_per_email", s.Cfg.EmailDailyPerEmail)
}

// EmailDailyLimitPerIP resolves email.daily_limit_per_ip.
func (s *Service) EmailDailyLimitPerIP() int {
	return s.Site.Int("email.daily_limit_per_ip", s.Cfg.EmailDailyPerIP)
}

// ── issue / validate ──

// IssueRegisterEmailCode generates, stores and sends a registration code. The
// order of side effects matches the reference: limits are checked first, the
// code is stored, the send counters are marked and only then is mail attempted,
// so a failed send still arms the resend cooldown.
func (s *Service) IssueRegisterEmailCode(ctx context.Context, email, clientIP string) (EmailCodeResult, error) {
	if !s.EmailVerificationEnabled() {
		return EmailCodeResult{}, ErrEmailVerificationDisabled
	}
	email = normalizeEmail(email)
	resendSeconds := s.EmailResendSeconds()
	if s.emailCodes.cooldownRemaining(email) > 0 {
		return EmailCodeResult{}, &RateLimitError{Message: fmt.Sprintf("验证码发送过于频繁，请 %d 秒后再试", resendSeconds)}
	}
	dateKey := s.localDate().Format("2006-01-02")
	if s.emailCodes.dailyCount(dailyEmailKey(email, dateKey)) >= s.EmailDailyLimitPerEmail() {
		return EmailCodeResult{}, &RateLimitError{Message: "该邮箱今日获取验证码次数已达上限，请明天再试"}
	}
	if clientIP != "" && s.emailCodes.dailyCount(dailyIPKey(clientIP, dateKey)) >= s.EmailDailyLimitPerIP() {
		return EmailCodeResult{}, &RateLimitError{Message: "当前 IP 今日获取验证码次数已达上限，请明天再试"}
	}

	code := randomDigits(s.EmailCodeLength())
	ttlSeconds := s.EmailCodeTTLSeconds()
	expireMinutes := int(math.Ceil(float64(ttlSeconds) / 60))
	if expireMinutes < 1 {
		expireMinutes = 1
	}
	s.emailCodes.storeCode(email, hashEmailCode(code), time.Duration(ttlSeconds)*time.Second)
	s.emailCodes.setCooldown(email, time.Duration(resendSeconds)*time.Second)
	window := s.secondsUntilTomorrow()
	s.emailCodes.incrementDaily(dailyEmailKey(email, dateKey), window)
	if clientIP != "" {
		s.emailCodes.incrementDaily(dailyIPKey(clientIP, dateKey), window)
	}

	if err := s.sendRegisterVerificationEmail(email, code, expireMinutes); err != nil {
		return EmailCodeResult{}, err
	}
	s.logAudit(ctx, "register_email_code_sent", "smtp", map[string]any{"email": email})
	return EmailCodeResult{ExpiresIn: ttlSeconds, ResendIn: resendSeconds}, nil
}

// ValidateRegisterEmailCode checks a code without consuming it. It is a no-op
// when email verification is switched off.
func (s *Service) ValidateRegisterEmailCode(email, code string) error {
	if !s.EmailVerificationEnabled() {
		return nil
	}
	stored, ok := s.emailCodes.peekCode(normalizeEmail(email))
	if !ok {
		return ErrEmailCodeExpired
	}
	if !hmac.Equal([]byte(stored), []byte(hashEmailCode(strings.TrimSpace(code)))) {
		return ErrEmailCodeInvalid
	}
	return nil
}

// ConsumeRegisterEmailCode validates and then deletes a code.
func (s *Service) ConsumeRegisterEmailCode(email, code string) error {
	if !s.EmailVerificationEnabled() {
		return nil
	}
	normalized := normalizeEmail(email)
	stored, ok := s.emailCodes.takeCode(normalized)
	if !ok {
		return ErrEmailCodeExpired
	}
	if !hmac.Equal([]byte(stored), []byte(hashEmailCode(strings.TrimSpace(code)))) {
		return ErrEmailCodeInvalid
	}
	return nil
}

func hashEmailCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func dailyEmailKey(email, dateKey string) string { return "email:" + email + ":" + dateKey }
func dailyIPKey(ip, dateKey string) string       { return "ip:" + ip + ":" + dateKey }

func (s *Service) localDate() time.Time {
	return time.Now().In(s.timeLocation())
}

func (s *Service) timeLocation() *time.Location {
	if s.location != nil {
		return s.location
	}
	return time.Local
}

// secondsUntilTomorrow mirrors the reference's cache TTL for the daily send
// counters: the whole remaining local day, floored at 60 seconds.
func (s *Service) secondsUntilTomorrow() time.Duration {
	location := s.timeLocation()
	now := time.Now().In(location)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).AddDate(0, 0, 1)
	seconds := int(math.Ceil(midnight.Sub(now).Seconds()))
	if seconds < 60 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}

func randomDigits(length int) string {
	if length <= 0 {
		length = 6
	}
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	out := make([]byte, length)
	for index, value := range buffer {
		out[index] = '0' + value%10
	}
	return string(out)
}

// ── SMTP ──

// sendRegisterVerificationEmail is the SMTP path the reference uses
// (_send_register_verification_email). Missing settings raise ConfigError, any
// transport failure raises SendError.
func (s *Service) sendRegisterVerificationEmail(email, code string, expireMinutes int) error {
	missing := []string{}
	if strings.TrimSpace(s.Cfg.EmailHost) == "" {
		missing = append(missing, "EMAIL_HOST")
	}
	if strings.TrimSpace(s.Cfg.EmailUser) == "" {
		missing = append(missing, "EMAIL_HOST_USER")
	}
	if s.Cfg.EmailPassword == "" {
		missing = append(missing, "EMAIL_HOST_PASSWORD")
	}
	if strings.TrimSpace(s.Cfg.EmailFrom) == "" {
		missing = append(missing, "DEFAULT_FROM_EMAIL")
	}
	if len(missing) > 0 {
		return &ConfigError{Message: "邮件发送配置不完整：缺少 " + strings.Join(missing, ", ")}
	}

	subject := fmt.Sprintf("%s 注册验证码", s.Cfg.SiteName)
	textBody := fmt.Sprintf("你的验证码是：%s\n\n%d 分钟内有效。\n如果这不是你的操作，请忽略本邮件。", code, expireMinutes)
	htmlBody := fmt.Sprintf("<p>你的验证码是：<strong style='font-size:20px;'>%s</strong></p><p>%d 分钟内有效。</p><p>如果这不是你的操作，请忽略本邮件。</p>",
		html.EscapeString(code), expireMinutes)
	if err := sendSMTP(s.Cfg, email, subject, textBody, htmlBody); err != nil {
		if s.Log != nil {
			s.Log.Warn("email verification send failed", "error", err)
		}
		return &SendError{Message: "邮箱验证码发送失败，请检查 SMTP 配置"}
	}
	return nil
}

func sendSMTP(cfg *config.Config, to, subject, textBody, htmlBody string) error {
	timeout := cfg.EmailTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	address := net.JoinHostPort(cfg.EmailHost, strconv.Itoa(cfg.EmailPort))
	dialer := &net.Dialer{Timeout: timeout}

	var conn net.Conn
	var err error
	if cfg.EmailUseSSL {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: cfg.EmailHost})
	} else {
		conn, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))

	client, err := smtp.NewClient(conn, cfg.EmailHost)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer client.Close()

	if !cfg.EmailUseSSL && cfg.EmailUseTLS {
		if err := client.StartTLS(&tls.Config{ServerName: cfg.EmailHost}); err != nil {
			return err
		}
	}
	if cfg.EmailUser != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.EmailUser, cfg.EmailPassword, cfg.EmailHost)); err != nil {
			return err
		}
	}

	from := envelopeAddress(cfg.EmailFrom)
	if from == "" {
		from = cfg.EmailUser
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(buildMessage(cfg.EmailFrom, to, subject, textBody, htmlBody)); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// envelopeAddress extracts "addr" from "Name <addr>".
func envelopeAddress(value string) string {
	if start := strings.LastIndex(value, "<"); start >= 0 {
		if end := strings.Index(value[start:], ">"); end > 0 {
			return strings.TrimSpace(value[start+1 : start+end])
		}
	}
	return strings.TrimSpace(value)
}

func buildMessage(from, to, subject, textBody, htmlBody string) []byte {
	boundary := fmt.Sprintf("===============%s==", strings.ToLower(randomHexString(12)))
	var message strings.Builder
	fmt.Fprintf(&message, "From: %s\r\n", from)
	fmt.Fprintf(&message, "To: %s\r\n", to)
	fmt.Fprintf(&message, "Subject: %s\r\n", encodeHeader(subject))
	fmt.Fprintf(&message, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	message.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&message, "Content-Type: multipart/alternative; boundary=\"%s\"\r\n", boundary)
	message.WriteString("\r\n")
	fmt.Fprintf(&message, "--%s\r\n", boundary)
	message.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	message.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	message.WriteString(wrapBase64(textBody))
	message.WriteString("\r\n")
	fmt.Fprintf(&message, "--%s\r\n", boundary)
	message.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n")
	message.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	message.WriteString(wrapBase64(htmlBody))
	message.WriteString("\r\n")
	fmt.Fprintf(&message, "--%s--\r\n", boundary)
	return []byte(message.String())
}

func encodeHeader(value string) string {
	if isASCII(value) {
		return value
	}
	return mime.BEncoding.Encode("utf-8", value)
}

func isASCII(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] > 0x7f {
			return false
		}
	}
	return true
}

func wrapBase64(value string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(value))
	var out strings.Builder
	for len(encoded) > 76 {
		out.WriteString(encoded[:76])
		out.WriteString("\r\n")
		encoded = encoded[76:]
	}
	out.WriteString(encoded)
	return out.String()
}

func randomHexString(length int) string {
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buffer)
}

func (s *Service) logAudit(ctx context.Context, eventType, source string, payload map[string]any) {
	if s.Audit == nil {
		return
	}
	if err := s.Audit.LogCallbackEvent(ctx, eventType, source, payload, true, ""); err != nil && s.Log != nil {
		s.Log.Warn("audit log write failed", "event_type", eventType, "error", err)
	}
}
