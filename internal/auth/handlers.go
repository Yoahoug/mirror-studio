package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"mirror-studio/internal/config"
	"mirror-studio/internal/db"
	"mirror-studio/internal/httpapi"
	"mirror-studio/internal/siteconfig"
)

// Field and message text from the frozen contract.
const (
	msgRegisterClosed = "当前站点已关闭注册"
	msgFieldRequired  = "这个字段是必填项。"
	msgMaxLength      = "确保该变量包含不超过 %d 字符 (目前字符数 %d)。"
	msgInvalidEmail   = "输入一个有效的 Email 地址。"
)

// Handlers serves the auth and account JSON API.
type Handlers struct {
	svc      *Service
	site     *siteconfig.Service
	cfg      *config.Config
	log      *slog.Logger
	sessions *httpapi.SessionStore
}

// NewHandlers builds the HTTP layer for the auth service.
func NewHandlers(svc *Service, site *siteconfig.Service, cfg *config.Config, log *slog.Logger) *Handlers {
	return &Handlers{
		svc:      svc,
		site:     site,
		cfg:      cfg,
		log:      log,
		sessions: httpapi.NewSessionStore(svc.DB, !cfg.Debug),
	}
}

// NewHandlers builds the HTTP layer bound to this service, which is how main.go
// wires it.
func (s *Service) NewHandlers(site *siteconfig.Service, cfg *config.Config, log *slog.Logger) *Handlers {
	return NewHandlers(s, site, cfg, log)
}

// Register mounts the JSON routes owned by this package. HTML pages belong to
// internal/web.
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/csrf", h.withSession(h.handleCSRF))
	mux.HandleFunc("GET /api/auth/captcha", h.withSession(h.handleCaptcha))
	mux.HandleFunc("POST /api/auth/login", h.withSession(h.handleLogin))
	mux.HandleFunc("POST /api/auth/logout", h.withSession(h.handleLogout))
	mux.HandleFunc("POST /api/auth/register/check", h.withSession(h.handleRegisterCheck))
	mux.HandleFunc("POST /api/auth/register/email-code", h.withSession(h.handleRegisterEmailCode))
	mux.HandleFunc("POST /api/auth/register", h.withSession(h.handleRegister))
	mux.HandleFunc("POST /api/auth/reset-password", h.withSession(h.handleResetPassword))
	mux.HandleFunc("GET /api/account/summary", h.withSession(h.handleAccountSummary))
	mux.HandleFunc("GET /api/account/settings", h.withSession(h.handleAccountSettingsGet))
	mux.HandleFunc("PATCH /api/account/settings", h.withSession(h.handleAccountSettingsPatch))
	mux.HandleFunc("POST /api/account/password", h.withSession(h.handleAccountPassword))
}

// withSession persists the request session once the handler ran. The shared
// middleware writes the session cookie but never stores the record, so every
// domain that touches session state has to flush it itself.
func (h *Handlers) withSession(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler(w, r)
		session := httpapi.SessionOf(r)
		if session == nil {
			return
		}
		if err := h.sessions.Save(session); err != nil && h.log != nil {
			h.log.Error("session save failed", "error", err)
		}
	}
}

// ── handlers ──

func (h *Handlers) handleCSRF(w http.ResponseWriter, r *http.Request) {
	httpapi.SetCSRFCookie(w)
	token := ""
	if material := httpapi.CSRFTokenOf(r); material != nil {
		token = material.Masked
	}
	httpapi.OK(w, map[string]any{"csrf_token": token})
}

func (h *Handlers) handleCaptcha(w http.ResponseWriter, r *http.Request) {
	httpapi.SetCSRFCookie(w)
	if !h.loginCaptchaEnabled() {
		httpapi.OK(w, map[string]any{"captcha_code": "", "captcha_svg": ""})
		return
	}
	code := GenerateCaptchaCode(CaptchaLength)
	if session := httpapi.SessionOf(r); session != nil {
		session.Set(SessionCaptchaKey, code)
	}
	httpapi.OK(w, map[string]any{"captcha_code": code, "captcha_svg": renderCaptchaDataURI(code)})
}

func (h *Handlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	payload, ok := h.payloadOrFail(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	clientIP := httpapi.From(r).ClientIP
	username := stringValue(payload, "username")
	if err := h.ensureLoginAllowed(ctx, username, clientIP); err != nil {
		httpapi.Error(w, http.StatusTooManyRequests, httpapi.CodeRateLimited, err.Error())
		return
	}
	form, err := h.validateLogin(ctx, payload, httpapi.SessionOf(r))
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !form.errs.Empty() {
		h.recordLoginFailure(ctx, username, clientIP)
		httpapi.BadRequest(w, form.errs.AsJSON())
		return
	}
	user := form.user
	if err := h.svc.Login(ctx, httpapi.SessionOf(r), user); err != nil {
		h.serverError(w, err)
		return
	}
	if err := h.svc.UpdateLogin(ctx, user.IDValue, clientIP); err != nil {
		h.serverError(w, err)
		return
	}
	h.clearLoginFailures(ctx, username, clientIP)
	httpapi.SetCSRFCookie(w)
	httpapi.OK(w, map[string]any{"username": user.Username})
}

func (h *Handlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	httpapi.ClearSession(w, httpapi.SessionOf(r))
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		httpapi.OK(w, map[string]any{})
		return
	}
	redirectFound(w, "/")
}

func (h *Handlers) handleRegisterCheck(w http.ResponseWriter, r *http.Request) {
	if !h.registerEnabled() {
		httpapi.Forbidden(w, httpapi.CodePermission, msgRegisterClosed)
		return
	}
	payload, ok := h.payloadOrFail(w, r)
	if !ok {
		return
	}
	form := h.validateRegisterAvailability(payload)
	if !form.errs.Empty() {
		httpapi.BadRequest(w, form.errs.First())
		return
	}
	ctx := r.Context()
	data := map[string]any{}
	if username := form.cleaned["username"]; username != "" {
		exists, err := h.svc.UsernameExists(ctx, username)
		if err != nil {
			h.serverError(w, err)
			return
		}
		data["username"] = availabilityItem(exists, "账号可用", "账号已存在")
	}
	if email := form.cleaned["email"]; email != "" {
		exists, err := h.svc.EmailExists(ctx, email)
		if err != nil {
			h.serverError(w, err)
			return
		}
		data["email"] = availabilityItem(exists, "邮箱可用", "邮箱已存在")
	}
	httpapi.OK(w, data)
}

func availabilityItem(exists bool, availableMessage, takenMessage string) map[string]any {
	if exists {
		return map[string]any{"available": false, "message": takenMessage}
	}
	return map[string]any{"available": true, "message": availableMessage}
}

func (h *Handlers) handleRegisterEmailCode(w http.ResponseWriter, r *http.Request) {
	if !h.registerEnabled() {
		httpapi.Forbidden(w, httpapi.CodePermission, msgRegisterClosed)
		return
	}
	payload, ok := h.payloadOrFail(w, r)
	if !ok {
		return
	}
	form, err := h.validateRegisterEmailCodeForm(r.Context(), payload)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !form.errs.Empty() {
		httpapi.BadRequest(w, form.errs.First())
		return
	}
	result, err := h.svc.IssueRegisterEmailCode(r.Context(), form.cleaned["email"], httpapi.From(r).ClientIP)
	if err != nil {
		var configErr *ConfigError
		var rateErr *RateLimitError
		var sendErr *SendError
		switch {
		case errors.As(err, &configErr):
			httpapi.Error(w, http.StatusInternalServerError, httpapi.CodeEmailNotConfigured, configErr.Message)
		case errors.As(err, &rateErr):
			httpapi.Error(w, http.StatusTooManyRequests, httpapi.CodeRateLimited, rateErr.Message)
		case errors.As(err, &sendErr):
			httpapi.Error(w, http.StatusBadGateway, httpapi.CodeEmailSendFailed, sendErr.Message)
		default:
			h.serverError(w, err)
		}
		return
	}
	httpapi.Success(w, http.StatusOK, map[string]any{
		"expire_seconds": result.ExpiresIn,
		"resend_seconds": result.ResendIn,
	}, "验证码已发送，请留意邮箱")
}

func (h *Handlers) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !h.registerEnabled() {
		httpapi.Forbidden(w, httpapi.CodePermission, msgRegisterClosed)
		return
	}
	payload, ok := h.payloadOrFail(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	form, err := h.validateRegister(ctx, payload)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !form.errs.Empty() {
		httpapi.BadRequest(w, form.errs.AsJSON())
		return
	}
	if err := h.svc.ConsumeRegisterEmailCode(form.email, form.emailCode); err != nil {
		httpapi.BadRequest(w, err.Error())
		return
	}
	user, err := h.svc.Register(ctx, form.username, form.email, form.password, httpapi.From(r).ClientIP)
	if err != nil {
		switch {
		case errors.Is(err, ErrUsernameTaken):
			errs := httpapi.NewFormErrors()
			errs.Add("username", "账号已存在", "")
			httpapi.BadRequest(w, errs.AsJSON())
		case errors.Is(err, ErrEmailTaken):
			errs := httpapi.NewFormErrors()
			errs.Add("email", "邮箱已存在", "")
			httpapi.BadRequest(w, errs.AsJSON())
		default:
			h.serverError(w, err)
		}
		return
	}
	if err := h.svc.Login(ctx, httpapi.SessionOf(r), user); err != nil {
		h.serverError(w, err)
		return
	}
	httpapi.SetCSRFCookie(w)
	httpapi.OK(w, map[string]any{"username": user.Username})
}

func (h *Handlers) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	payload, ok := h.payloadOrFail(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	form, err := h.validateResetPassword(ctx, payload, httpapi.SessionOf(r))
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !form.errs.Empty() {
		httpapi.BadRequest(w, form.errs.AsJSON())
		return
	}
	if err := h.svc.SetPassword(ctx, form.user.IDValue, form.password); err != nil {
		h.serverError(w, err)
		return
	}
	h.svc.logAudit(ctx, "password_reset", "users", map[string]any{"username": form.user.Username})
	httpapi.OK(w, map[string]any{})
}

func (h *Handlers) handleAccountSummary(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	summary, err := h.svc.Summary(ctx, user)
	if err != nil {
		h.serverError(w, err)
		return
	}
	quota, err := h.svc.QuotaSummary(ctx, user.IDValue)
	if err != nil {
		h.serverError(w, err)
		return
	}
	tasks, err := h.recentTasks(ctx, user.IDValue)
	if err != nil {
		h.serverError(w, err)
		return
	}
	httpapi.OK(w, map[string]any{"user": summary, "quota": quota, "recent_tasks": tasks})
}

func (h *Handlers) handleAccountSettingsGet(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	profile, err := h.svc.ProfileOf(ctx, user.IDValue)
	if err != nil {
		h.serverError(w, err)
		return
	}
	quota, err := h.svc.QuotaSummary(ctx, user.IDValue)
	if err != nil {
		h.serverError(w, err)
		return
	}
	httpapi.OK(w, map[string]any{"user": accountUserPayload(user, profile), "quota": quota})
}

func (h *Handlers) handleAccountSettingsPatch(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	payload, ok := h.payloadOrFail(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	profile, err := h.svc.ProfileOf(ctx, user.IDValue)
	if err != nil {
		h.serverError(w, err)
		return
	}

	nickname := truncateRunes(strings.TrimSpace(stringValue(payload, "nickname")), 64)
	email := normalizeEmail(stringValue(payload, "email"))
	avatar := strings.TrimSpace(stringValue(payload, "avatar"))
	remark := truncateRunes(strings.TrimSpace(stringValue(payload, "remark")), 500)

	if nickname == "" {
		httpapi.BadRequest(w, "昵称不能为空")
		return
	}
	if email != "" {
		var count int
		if err := h.svc.DB.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM users_user WHERE email = ? AND id <> ?`, email, user.IDValue,
		).Scan(&count); err != nil {
			h.serverError(w, err)
			return
		}
		if count > 0 {
			httpapi.BadRequest(w, "邮箱已存在")
			return
		}
	}
	if avatar != "" && !validAvatarURL(avatar) {
		httpapi.BadRequest(w, "头像地址格式不正确")
		return
	}

	now := db.FormatTime(time.Now())
	if _, err := h.svc.DB.ExecContext(ctx,
		`UPDATE users_user SET email = ? WHERE id = ?`, db.NullableString(email), user.IDValue,
	); err != nil {
		h.serverError(w, err)
		return
	}
	if _, err := h.svc.DB.ExecContext(ctx,
		`UPDATE users_userprofile SET nickname = ?, avatar = ?, remark = ?, updated_at = ? WHERE user_id = ?`,
		nickname, avatar, remark, now, user.IDValue,
	); err != nil {
		h.serverError(w, err)
		return
	}

	user.Email = email
	profile.Nickname = nickname
	profile.Avatar = avatar
	profile.Remark = remark
	quota, err := h.svc.QuotaSummary(ctx, user.IDValue)
	if err != nil {
		h.serverError(w, err)
		return
	}
	httpapi.Success(w, http.StatusOK,
		map[string]any{"user": accountUserPayload(user, profile), "quota": quota}, "资料已更新")
}

func (h *Handlers) handleAccountPassword(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	payload, ok := h.payloadOrFail(w, r)
	if !ok {
		return
	}
	currentPassword := stringValue(payload, "current_password")
	newPassword := stringValue(payload, "new_password")
	confirmPassword := stringValue(payload, "confirm_password")

	if currentPassword == "" {
		httpapi.BadRequest(w, "请输入当前密码")
		return
	}
	if ok, _ := VerifyPassword(user.Password, currentPassword); !ok {
		httpapi.BadRequest(w, "当前密码不正确")
		return
	}
	if newPassword == "" {
		httpapi.BadRequest(w, "请输入新密码")
		return
	}
	if newPassword != confirmPassword {
		httpapi.BadRequest(w, "两次密码输入不一致")
		return
	}
	if violations := ValidatePassword(newPassword, user); len(violations) > 0 {
		httpapi.BadRequest(w, violations[0].Message)
		return
	}
	ctx := r.Context()
	if err := h.svc.SetPassword(ctx, user.IDValue, newPassword); err != nil {
		h.serverError(w, err)
		return
	}
	// update_session_auth_hash keeps this session valid across the change.
	if session := httpapi.SessionOf(r); session != nil {
		session.CycleKey()
	}
	h.svc.logAudit(ctx, "password_change", "users", map[string]any{"username": user.Username})
	httpapi.Success(w, http.StatusOK, map[string]any{}, "密码已更新")
}

// ── form validation ──

type formState struct {
	errs    *httpapi.FormErrors
	cleaned map[string]string
}

func newFormState() *formState {
	return &formState{errs: httpapi.NewFormErrors(), cleaned: map[string]string{}}
}

func (f *formState) addError(field, message, code string) { f.errs.Add(field, message, code) }

// charField mirrors django.forms.CharField: strip, required, max_length.
func (f *formState) charField(name, value string, required bool, maxLength int) {
	value = strings.TrimSpace(value)
	if value == "" {
		if required {
			f.addError(name, msgFieldRequired, "required")
			return
		}
		f.cleaned[name] = ""
		return
	}
	if maxLength > 0 && utf8.RuneCountInString(value) > maxLength {
		f.addError(name, fmt.Sprintf(msgMaxLength, maxLength, utf8.RuneCountInString(value)), "max_length")
		return
	}
	f.cleaned[name] = value
}

// emailField mirrors django.forms.EmailField (max_length 254) plus
// EmailValidator.
func (f *formState) emailField(name, value string, required bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		if required {
			f.addError(name, msgFieldRequired, "required")
			return
		}
		f.cleaned[name] = ""
		return
	}
	if len(value) > 254 {
		f.addError(name, fmt.Sprintf(msgMaxLength, 254, utf8.RuneCountInString(value)), "max_length")
		return
	}
	if !validEmail(value) {
		f.addError(name, msgInvalidEmail, "invalid")
		return
	}
	f.cleaned[name] = value
}

func (f *formState) has(name string) bool {
	_, ok := f.cleaned[name]
	return ok
}

type loginForm struct {
	*formState
	user *User
}

func (h *Handlers) validateLogin(ctx context.Context, payload map[string]any, session *httpapi.Session) (*loginForm, error) {
	form := &loginForm{formState: newFormState()}
	form.charField("captcha_code", stringValue(payload, "captcha_code"), true, 8)
	if form.has("captcha_code") && h.loginCaptchaEnabled() {
		if !captchaMatches(session, form.cleaned["captcha_code"]) {
			form.addError("captcha_code", "验证码错误", "")
		}
	}
	form.charField("username", stringValue(payload, "username"), true, 150)
	form.charField("password", stringValue(payload, "password"), true, 0)

	username, hasUsername := form.cleaned["username"]
	password, hasPassword := form.cleaned["password"]
	if !hasUsername || !hasPassword {
		form.addError("", "账号或密码错误", "")
		return form, nil
	}
	user, err := h.svc.Authenticate(ctx, username, password)
	if err != nil {
		if !errors.Is(err, ErrInvalidCredentials) {
			return nil, err
		}
		form.addError("", "账号或密码错误", "")
		return form, nil
	}
	if user.Status != StatusActive {
		form.addError("", "账号状态不可用", "")
		return form, nil
	}
	form.user = user
	return form, nil
}

func captchaMatches(session *httpapi.Session, value string) bool {
	expected := ""
	if session != nil {
		expected = session.Get(SessionCaptchaKey)
	}
	if expected == "" {
		return false
	}
	return strings.ToUpper(strings.TrimSpace(value)) == expected
}

type registerForm struct {
	*formState
	username  string
	email     string
	emailCode string
	password  string
}

func (h *Handlers) validateRegister(ctx context.Context, payload map[string]any) (*registerForm, error) {
	form := &registerForm{formState: newFormState()}
	verification := h.svc.EmailVerificationEnabled()

	form.charField("username", stringValue(payload, "username"), true, 150)
	if form.has("username") {
		exists, err := h.svc.UsernameExists(ctx, form.cleaned["username"])
		if err != nil {
			return nil, err
		}
		if exists {
			form.addError("username", "账号已存在", "")
		}
	}
	form.emailField("email", stringValue(payload, "email"), true)
	if form.has("email") {
		form.cleaned["email"] = normalizeEmail(form.cleaned["email"])
		exists, err := h.svc.EmailExists(ctx, form.cleaned["email"])
		if err != nil {
			return nil, err
		}
		if exists {
			form.addError("email", "邮箱已存在", "")
		}
	}
	form.charField("email_verification_code", stringValue(payload, "email_verification_code"), verification, 8)
	form.charField("password", stringValue(payload, "password"), true, 0)
	form.charField("confirm_password", stringValue(payload, "confirm_password"), true, 0)

	form.username = form.cleaned["username"]
	form.email = form.cleaned["email"]
	form.emailCode = form.cleaned["email_verification_code"]
	form.password = form.cleaned["password"]

	if form.email != "" && form.emailCode != "" {
		if err := h.svc.ValidateRegisterEmailCode(form.email, form.emailCode); err != nil {
			form.addError("", err.Error(), "")
			return form, nil
		}
	}
	if form.password != "" && form.password != form.cleaned["confirm_password"] {
		form.addError("", "两次密码输入不一致", "")
		return form, nil
	}
	if form.password != "" {
		for _, violation := range ValidatePassword(form.password, nil) {
			form.addError("", violation.Message, violation.Code)
		}
	}
	return form, nil
}

type availabilityForm struct {
	*formState
}

func (h *Handlers) validateRegisterAvailability(payload map[string]any) *availabilityForm {
	form := &availabilityForm{formState: newFormState()}
	form.charField("username", stringValue(payload, "username"), false, 150)
	form.emailField("email", stringValue(payload, "email"), false)
	if form.has("email") {
		form.cleaned["email"] = normalizeEmail(form.cleaned["email"])
	}
	if form.cleaned["username"] == "" && form.cleaned["email"] == "" {
		form.addError("", "至少提供账号或邮箱", "")
	}
	return form
}

type emailCodeForm struct {
	*formState
}

func (h *Handlers) validateRegisterEmailCodeForm(ctx context.Context, payload map[string]any) (*emailCodeForm, error) {
	form := &emailCodeForm{formState: newFormState()}
	form.charField("username", stringValue(payload, "username"), true, 150)
	if form.has("username") {
		exists, err := h.svc.UsernameExists(ctx, form.cleaned["username"])
		if err != nil {
			return nil, err
		}
		if exists {
			form.addError("username", "账号已存在", "")
		}
	}
	form.emailField("email", stringValue(payload, "email"), true)
	if form.has("email") {
		form.cleaned["email"] = normalizeEmail(form.cleaned["email"])
		exists, err := h.svc.EmailExists(ctx, form.cleaned["email"])
		if err != nil {
			return nil, err
		}
		if exists {
			form.addError("email", "邮箱已存在", "")
		}
	}
	return form, nil
}

type resetPasswordForm struct {
	*formState
	user     *User
	password string
}

func (h *Handlers) validateResetPassword(ctx context.Context, payload map[string]any, session *httpapi.Session) (*resetPasswordForm, error) {
	form := &resetPasswordForm{formState: newFormState()}
	form.charField("captcha_code", stringValue(payload, "captcha_code"), true, 8)
	if form.has("captcha_code") && h.loginCaptchaEnabled() {
		if !captchaMatches(session, form.cleaned["captcha_code"]) {
			form.addError("captcha_code", "验证码错误", "")
		}
	}
	form.charField("username", stringValue(payload, "username"), true, 150)
	form.emailField("email", stringValue(payload, "email"), false)
	form.charField("new_password", stringValue(payload, "new_password"), true, 0)
	form.charField("confirm_password", stringValue(payload, "confirm_password"), true, 0)

	email := normalizeEmail(form.cleaned["email"])
	if form.has("email") {
		form.cleaned["email"] = email
	}
	form.password = form.cleaned["new_password"]
	if form.password != "" && form.password != form.cleaned["confirm_password"] {
		form.addError("", "两次密码输入不一致", "")
		return form, nil
	}
	if form.password != "" {
		violations := ValidatePassword(form.password, nil)
		// A strength failure raises in the reference, which stops clean() before
		// the username lookup; a passing password falls through to it.
		if len(violations) > 0 {
			for _, violation := range violations {
				form.addError("", violation.Message, violation.Code)
			}
			return form, nil
		}
	}
	user, err := h.svc.ByUsername(ctx, form.cleaned["username"])
	if errors.Is(err, ErrUserNotFound) {
		form.addError("", "用户不存在", "")
		return form, nil
	}
	if err != nil {
		return nil, err
	}
	if email != "" && user.Email != "" && strings.ToLower(user.Email) != email {
		form.addError("", "邮箱不匹配", "")
		return form, nil
	}
	form.user = user
	return form, nil
}

// ── account payloads ──

func accountUserPayload(user *User, profile Profile) map[string]any {
	registerSource := profile.RegisterSource
	if registerSource == "" {
		registerSource = "website"
	}
	var dateJoined any
	if !user.DateJoined.IsZero() {
		dateJoined = pythonISO(user.DateJoined)
	}
	var lastLoginAt any
	if user.LastLoginAt != nil {
		lastLoginAt = pythonISO(*user.LastLoginAt)
	}
	return map[string]any{
		"username":        user.Username,
		"nickname":        firstNonEmpty(profile.Nickname, user.Username),
		"email":           user.Email,
		"avatar":          strings.TrimSpace(profile.Avatar),
		"remark":          profile.Remark,
		"role":            user.DisplayRole(),
		"status":          user.DisplayStatus(),
		"date_joined":     dateJoined,
		"last_login_at":   lastLoginAt,
		"last_login_ip":   user.LastLoginIP,
		"register_source": registerSource,
	}
}

// ── risk bridge ──

func (h *Handlers) ensureLoginAllowed(ctx context.Context, username, ip string) error {
	if h.svc.Risk == nil {
		return nil
	}
	return h.svc.Risk.EnsureLoginAllowed(ctx, username, ip)
}

func (h *Handlers) recordLoginFailure(ctx context.Context, username, ip string) {
	if h.svc.Risk == nil {
		return
	}
	h.svc.Risk.RecordLoginFailure(ctx, username, ip)
}

func (h *Handlers) clearLoginFailures(ctx context.Context, username, ip string) {
	if h.svc.Risk == nil {
		return
	}
	h.svc.Risk.ClearLoginFailures(ctx, username, ip)
}

// ── request plumbing ──

// requireUser mirrors LoginRequiredMixin: anonymous callers get a redirect to
// LOGIN_URL with the full path in `next`.
func (h *Handlers) requireUser(w http.ResponseWriter, r *http.Request) (*User, bool) {
	user, err := h.svc.UserFromRequest(r)
	if err != nil {
		fullPath := r.URL.Path
		if r.URL.RawQuery != "" {
			fullPath += "?" + r.URL.RawQuery
		}
		redirectFound(w, "/workspace?auth=login&next="+httpapi.PathEscapePercent(escapeURIPath(fullPath)))
		return nil, false
	}
	return user, true
}

// UserFromRequest resolves the authenticated account behind a request.
func (s *Service) UserFromRequest(r *http.Request) (*User, error) {
	principal := httpapi.UserOf(r)
	if principal == nil {
		return nil, ErrUserNotFound
	}
	switch typed := principal.(type) {
	case Principal:
		if typed.User != nil {
			return typed.User, nil
		}
	case *Principal:
		if typed.User != nil {
			return typed.User, nil
		}
	}
	return s.ByID(r.Context(), principal.ID())
}

func (h *Handlers) payloadOrFail(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	payload, err := h.parsePayload(r)
	if err == nil {
		return payload, true
	}
	if errors.Is(err, httpapi.ErrRequestTooBig) {
		// The reference lets RequestDataTooBig fall through as an HTTP 400 page.
		populateHTMLStatus(w, http.StatusBadRequest)
		return nil, false
	}
	httpapi.BadRequest(w, err.Error())
	return nil, false
}

func (h *Handlers) parsePayload(r *http.Request) (map[string]any, error) {
	payload, err := httpapi.ParsePayload(r)
	if err != nil {
		return nil, refinePayloadError(r, err)
	}
	if payload == nil {
		if isJSONRequest(r) {
			return nil, errors.New("请求 JSON 必须是对象")
		}
		payload = map[string]any{}
	}
	return payload, nil
}

// refinePayloadError turns "not an object" JSON bodies into the reference's
// message; syntax errors keep the parser's wording.
func refinePayloadError(r *http.Request, err error) error {
	if !isJSONRequest(r) {
		return err
	}
	body := bytes.TrimSpace(rawRequestBody(r))
	if len(body) == 0 {
		return err
	}
	var probe any
	if json.Unmarshal(body, &probe) == nil {
		return errors.New("请求 JSON 必须是对象")
	}
	return err
}

func isJSONRequest(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json")
}

func rawRequestBody(r *http.Request) []byte {
	if state := httpapi.From(r); state.Body != nil {
		return state.Body
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body
}

func (h *Handlers) serverError(w http.ResponseWriter, err error) {
	if h.log != nil {
		h.log.Error("auth request failed", "error", err)
	}
	populateHTMLStatus(w, http.StatusInternalServerError)
}

// redirectFound answers with an empty HTML body, which is what Django's
// HttpResponseRedirect produces; net/http's Redirect would append an <a> tag.
func redirectFound(w http.ResponseWriter, location string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusFound)
}

func populateHTMLStatus(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
}

// escapeURIPath mirrors django.utils.encoding.escape_uri_path.
func escapeURIPath(path string) string {
	const safe = "/;,:@&=+$-_.!~*'()[]%"
	var out strings.Builder
	for _, char := range []byte(path) {
		if strings.IndexByte(safe, char) >= 0 || isURIUnreserved(char) {
			out.WriteByte(char)
			continue
		}
		out.WriteString(fmt.Sprintf("%%%02X", char))
	}
	return out.String()
}

func isURIUnreserved(char byte) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9') || char == '-' || char == '.' || char == '_' || char == '~'
}

// stringValue mirrors python's str(payload.get(name)) for the scalar JSON types
// the API accepts. Containers degrade to "" because the reference would stringify
// their repr, which no client sends.
func stringValue(payload map[string]any, name string) string {
	value, ok := payload[name]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "True"
		}
		return "False"
	case float64:
		return formatJSONNumber(typed)
	}
	return ""
}

func formatJSONNumber(value float64) string {
	if value == float64(int64(value)) && value < 1e15 && value > -1e15 {
		return fmt.Sprintf("%d", int64(value))
	}
	return fmt.Sprintf("%v", value)
}

func (h *Handlers) registerEnabled() bool {
	return h.site.Bool("site.register_enabled", h.cfg.RegisterEnabled)
}

func (h *Handlers) loginCaptchaEnabled() bool {
	return h.site.Bool("site.login_captcha_enabled", h.cfg.LoginCaptchaEnabled)
}

func (h *Handlers) recentTasks(ctx context.Context, userID int64) ([]map[string]any, error) {
	tasks := []map[string]any{}
	if h.svc.Tasks == nil {
		return tasks, nil
	}
	items, err := h.svc.Tasks.RecentTasks(ctx, userID, 3)
	if err != nil {
		return nil, err
	}
	if items != nil {
		tasks = items
	}
	return tasks, nil
}

// ── validators ported from django.core.validators ──

const emailAtomChars = "-!#$%&'*+/=?^_`{}|~"

// validEmail ports EmailValidator: dot-atom or quoted-string user part,
// domain name or address literal domain part, "localhost" allowed.
func validEmail(value string) bool {
	if value == "" || len(value) > 320 || !strings.Contains(value, "@") {
		return false
	}
	at := strings.LastIndex(value, "@")
	userPart, domainPart := value[:at], value[at+1:]
	if !validEmailUser(userPart) {
		return false
	}
	if domainPart == "localhost" {
		return true
	}
	return validEmailDomain(domainPart)
}

func validEmailUser(value string) bool {
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, `"`) {
		if len(value) < 2 || !strings.HasSuffix(value, `"`) {
			return false
		}
		return validQuotedString(value[1 : len(value)-1])
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" {
			return false
		}
		for _, char := range part {
			if !isASCIIAlnum(char) && !strings.ContainsRune(emailAtomChars, char) {
				return false
			}
		}
	}
	return true
}

// validQuotedString ports the quoted-string branch of EmailValidator.user_regex.
func validQuotedString(value string) bool {
	for index := 0; index < len(value); {
		char := value[index]
		if char == '\\' {
			if index+1 >= len(value) {
				return false
			}
			escaped := value[index+1]
			if escaped < 0x01 || escaped > 0x7f || escaped == 0x0a || escaped == 0x0d {
				return false
			}
			index += 2
			continue
		}
		allowed := (char >= 0x01 && char <= 0x08) || char == 0x0b || char == 0x0c ||
			(char >= 0x0e && char <= 0x1f) || char == '!' ||
			(char >= '#' && char <= '[') || (char >= ']' && char <= 0x7f)
		if !allowed {
			return false
		}
		index++
	}
	return true
}

func validEmailDomain(value string) bool {
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		return validIPLiteral(value[1 : len(value)-1])
	}
	return validDomainName(value, false)
}

func validIPLiteral(value string) bool {
	if value == "" {
		return false
	}
	if validIPv4(value) {
		return true
	}
	return validIPv6(value)
}

// validAvatarURL ports URLValidator with schemes ["http","https"].
func validAvatarURL(value string) bool {
	if len(value) == 0 || len(value) > 2048 || strings.ContainsAny(value, "\t\r\n") {
		return false
	}
	index := strings.Index(value, "://")
	if index <= 0 {
		return false
	}
	for _, char := range value[:index] {
		if !isASCIISchemeChar(char) {
			return false
		}
	}
	scheme := strings.ToLower(value[:index])
	if scheme != "http" && scheme != "https" {
		return false
	}
	rest := value[index+3:]
	cut := strings.IndexAny(rest, "/?#")
	authority, path := rest, ""
	if cut >= 0 {
		authority, path = rest[:cut], rest[cut:]
	}
	if containsSpace(authority) || containsSpace(path) {
		return false
	}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		if !validURLAuthUserInfo(authority[:at]) {
			return false
		}
		authority = authority[at+1:]
	}
	if strings.HasPrefix(authority, "[") {
		end := strings.Index(authority, "]")
		if end < 0 {
			return false
		}
		literal := authority[1:end]
		if !validIPv6(literal) {
			return false
		}
		remainder := authority[end+1:]
		if remainder != "" {
			if !strings.HasPrefix(remainder, ":") || !validPort(remainder[1:]) {
				return false
			}
		}
		return len(literal) <= 253
	}
	host := authority
	if colon := strings.Index(host, ":"); colon >= 0 {
		if !validPort(host[colon+1:]) {
			return false
		}
		host = host[:colon]
	}
	if host == "" || len(host) > 253 {
		return false
	}
	return validIPv4(host) || validDomainName(host, true) || strings.EqualFold(host, "localhost")
}

func validURLAuthUserInfo(value string) bool {
	if value == "" {
		return false
	}
	user, password, hasPassword := strings.Cut(value, ":")
	if user == "" {
		return false
	}
	for _, char := range user {
		if char == ':' || char == '@' || char == '/' || containsSpace(string(char)) {
			return false
		}
	}
	if hasPassword {
		for _, char := range password {
			if char == ':' || char == '@' || char == '/' || containsSpace(string(char)) {
				return false
			}
		}
	}
	return true
}

func validPort(value string) bool {
	if len(value) == 0 || len(value) > 5 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

// validDomainName ports DomainNameValidator's IDN hostname_re + domain_re +
// tld_re (a trailing root dot is allowed when the caller permits it).
func validDomainName(value string, allowRootDot bool) bool {
	if value == "" || len(value) > 255 {
		return false
	}
	if allowRootDot && strings.HasSuffix(value, ".") {
		value = strings.TrimSuffix(value, ".")
	}
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return false
	}
	for index, part := range parts {
		switch {
		case index == 0:
			if !validHostnameLabel(part) {
				return false
			}
		case index == len(parts)-1:
			if !validTLDLabel(part) {
				return false
			}
		default:
			if len(part) < 1 || len(part) > 63 || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
				return false
			}
			if !domainLabelChars(part, true) {
				return false
			}
		}
	}
	return true
}

// validHostnameLabel ports hostname_re: 1..63 characters, alphanumeric ends.
func validHostnameLabel(value string) bool {
	if len(value) < 1 || len(value) > 63 {
		return false
	}
	runes := []rune(value)
	if !isHostnameChar(runes[0]) || !isHostnameChar(runes[len(runes)-1]) {
		return false
	}
	return domainLabelChars(value, true)
}

// validTLDLabel ports tld_no_fqdn_re: a letter-only label of 2..63 characters
// without leading/trailing hyphens, or a punycode label. Digits are not allowed
// in the TLD branch, which is what the Django regex encodes.
func validTLDLabel(value string) bool {
	if strings.HasPrefix(strings.ToLower(value), "xn--") {
		rest := value[4:]
		if len(rest) < 1 || len(rest) > 59 {
			return false
		}
		for index := 0; index < len(rest); index++ {
			if !isASCIIAlnum(rune(rest[index])) {
				return false
			}
		}
		return true
	}
	runes := []rune(value)
	if len(runes) < 2 || len(runes) > 63 {
		return false
	}
	if runes[0] == '-' || runes[len(runes)-1] == '-' {
		return false
	}
	for _, char := range runes {
		if !isASCIILetter(char) && !isDomainLetter(char) && char != '-' {
			return false
		}
	}
	return true
}

func domainLabelChars(value string, allowHyphen bool) bool {
	for _, char := range value {
		if isASCIIAlnum(char) || isDomainLetter(char) {
			continue
		}
		if allowHyphen && char == '-' {
			continue
		}
		return false
	}
	return true
}

// isHostnameChar reports the hostname_re alphabet: ASCII alphanumerics and the
// unicode range the Django regex accepts as letters.
func isHostnameChar(char rune) bool { return isASCIIAlnum(char) || isDomainLetter(char) }

func isDomainLetter(char rune) bool { return char >= 0xa1 && char <= 0xffff }

func isASCIIAlnum(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
}

func isASCIILetter(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
}

func isASCIISchemeChar(char rune) bool {
	return isASCIIAlnum(char) || char == '.' || char == '+' || char == '-'
}

func validIPv4(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return false
	}
	for _, part := range parts {
		if len(part) == 0 || len(part) > 3 {
			return false
		}
		if len(part) > 1 && part[0] == '0' {
			return false
		}
		for index := 0; index < len(part); index++ {
			if part[index] < '0' || part[index] > '9' {
				return false
			}
		}
		if number, err := strconv.Atoi(part); err != nil || number > 255 {
			return false
		}
	}
	return true
}

func validIPv6(value string) bool {
	if !strings.Contains(value, ":") {
		return false
	}
	return net.ParseIP(value) != nil
}

func containsSpace(value string) bool {
	for _, char := range value {
		if unicode.IsSpace(char) {
			return true
		}
	}
	return false
}
