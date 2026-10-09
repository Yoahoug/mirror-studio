package web

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"mirror-studio/internal/config"
	"mirror-studio/internal/crypt"
	"mirror-studio/internal/db"
	"mirror-studio/internal/httpapi"
	"mirror-studio/internal/siteconfig"
)

// AuthService is the user domain as the server-rendered pages consume it.
type AuthService interface {
	ByID(ctx context.Context, id int64) (httpapi.Principal, error)
	ByUsername(ctx context.Context, username string) (httpapi.Principal, error)
	Authenticate(ctx context.Context, username, password string) (httpapi.Principal, error)
	Register(ctx context.Context, username, email, password, ip string) (httpapi.Principal, error)
	SetPassword(ctx context.Context, userID int64, password string) error
}

// QuotaService reports the caller's quota counters.
type QuotaService interface {
	Summary(ctx context.Context, userID int64) (map[string]any, error)
}

// MediaService serves the image history the account pages render.
type MediaService interface {
	HistoryImages(ctx context.Context, userID int64, limit int) ([]map[string]any, error)
	HistoryDetail(ctx context.Context, userID int64, taskNo string) (map[string]any, []map[string]any, bool, error)
}

// Deps is the wiring main.go passes in.
type Deps struct {
	DB        *db.DB
	Config    *config.Config
	Site      *siteconfig.Service
	Location  *time.Location
	Cipher    *crypt.FieldCipher
	Auth      AuthService
	Quota     QuotaService
	Media     MediaService
	Generator interface{}
	Logger    *slog.Logger
}

// Service renders every server-side page.
type Service struct {
	cfg          *config.Config
	site         *siteconfig.Service
	location     *time.Location
	auth         AuthService
	quota        QuotaService
	media        MediaService
	logger       *slog.Logger
	engine       *templateSet
	assetVersion string
}

// NewService builds the page service over the embedded templates.
func NewService(deps Deps) *Service {
	location := deps.Location
	if location == nil {
		location = time.UTC
	}
	root, err := fs.Sub(templateFiles, "templates")
	if err != nil {
		panic(err)
	}
	release := ""
	if deps.Config != nil {
		release = deps.Config.Release.Version + "|" + deps.Config.Release.PublishedAt
	}
	return &Service{
		cfg:          deps.Config,
		site:         deps.Site,
		location:     location,
		auth:         deps.Auth,
		quota:        deps.Quota,
		media:        deps.Media,
		logger:       deps.Logger,
		engine:       newTemplateSet(root),
		assetVersion: assetVersion(root, release),
	}
}

// Register attaches the page routes to the shared mux.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("/", s.dispatch)
}

func (s *Service) dispatch(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		redirectFound(w, "/workspace")
	case "/workspace":
		s.page(w, r, s.workspacePage)
	case "/console":
		s.page(w, r, s.consolePage)
	case "/console/":
		s.page(w, r, s.consolePage)
	case "/admin", "/admin/":
		redirectFound(w, "/console/")
	case "/healthz":
		s.healthz(w, r)
	case "/login":
		s.formPage(w, r, s.loginPage)
	case "/register":
		s.formPage(w, r, s.registerPage)
	case "/forgot-password":
		s.formPage(w, r, s.forgotPasswordPage)
	case "/account":
		s.page(w, r, s.accountPage)
	case "/account/security":
		s.page(w, r, s.securityPage)
	case "/account/history":
		s.page(w, r, s.historyPage)
	default:
		if taskNo, ok := strings.CutPrefix(r.URL.Path, "/account/history/"); ok && taskNo != "" && !strings.Contains(taskNo, "/") {
			s.page(w, r, func(w http.ResponseWriter, r *http.Request) error {
				return s.historyDetailPage(w, r, taskNo)
			})
			return
		}
		renderNotFound(w, r)
	}
}

// page runs a GET/HEAD page handler and answers every other method the way the
// reference's class-based views do.
func (s *Service) page(w http.ResponseWriter, r *http.Request, handler func(http.ResponseWriter, *http.Request) error) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, OPTIONS")
		return
	}
	if err := handler(w, r); err != nil {
		s.logError(r, err)
		renderServerError(w)
	}
}

// formPage additionally accepts POST, like the reference's FormView pages.
func (s *Service) formPage(w http.ResponseWriter, r *http.Request, handler func(http.ResponseWriter, *http.Request) error) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		methodNotAllowed(w, "GET, POST, HEAD, OPTIONS")
		return
	}
	if err := handler(w, r); err != nil {
		s.logError(r, err)
		renderServerError(w)
	}
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeHTML(w, http.StatusMethodNotAllowed, "")
}

func (s *Service) healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD, OPTIONS")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status": "ok"}`))
}

func (s *Service) logError(r *http.Request, err error) {
	if s.logger != nil {
		s.logger.Error("page rendering failed", "path", r.URL.Path, "error", err)
	}
}

// ── pages ──

func (s *Service) workspacePage(w http.ResponseWriter, r *http.Request) error {
	values := s.baseValues(r)
	values["BRAND_NAME"] = s.site.String("site.brand_name", s.cfg.BrandName)
	values["WORKSPACE_VITE_DEV_URL"] = s.cfg.WorkspaceViteURL
	values["WORKSPACE_ASSET_VERSION"] = s.assetVersion
	values["WORKSPACE_RELEASE_JSON"] = safeHTML(s.releaseJSON())
	values["IS_AUTHENTICATED"] = httpapi.UserOf(r) != nil
	values["CAPTCHA_CODE"] = ""
	values["CAPTCHA_SVG"] = ""
	if httpapi.UserOf(r) == nil && s.cfg.LoginCaptchaEnabled {
		code := s.issueCaptcha(r)
		values["CAPTCHA_CODE"] = code
		values["CAPTCHA_SVG"] = renderCaptchaDataURI(code)
	}
	return s.render(w, r, "workspace/index.html", values)
}

func (s *Service) consolePage(w http.ResponseWriter, r *http.Request) error {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRequiredRedirect(w, r)
		return nil
	}
	if !user.IsActiveStatus() || !user.IsOperator() {
		renderForbidden(w)
		return nil
	}
	values := s.baseValues(r)
	values["BRAND_NAME"] = s.site.String("site.brand_name", s.cfg.BrandName)
	values["CONSOLE_VITE_DEV_URL"] = s.cfg.ConsoleViteURL
	return s.render(w, r, "console/index.html", values)
}

func (s *Service) loginPage(w http.ResponseWriter, r *http.Request) error {
	if r.Method == http.MethodPost {
		return s.loginSubmit(w, r)
	}
	values := s.baseValues(r)
	values["form"] = loginFormSpec()
	values["captcha_code"], values["captcha_svg"] = s.issueCaptchaPair(r)
	return s.render(w, r, "account/login.html", values)
}

func (s *Service) registerPage(w http.ResponseWriter, r *http.Request) error {
	if !s.cfg.RegisterEnabled {
		// The reference redirects to the "home" route, which redirects again.
		redirectFound(w, "/")
		return nil
	}
	if r.Method == http.MethodPost {
		return s.registerSubmit(w, r)
	}
	values := s.baseValues(r)
	values["form"] = s.registerFormSpec()
	values["email_verification_enabled"] = s.emailVerificationEnabled()
	values["email_code_resend_seconds"] = s.site.Int("email.verification_resend_seconds", int(s.cfg.EmailResendInterval.Seconds()))
	return s.render(w, r, "account/register.html", values)
}

func (s *Service) forgotPasswordPage(w http.ResponseWriter, r *http.Request) error {
	if r.Method == http.MethodPost {
		return s.forgotPasswordSubmit(w, r)
	}
	values := s.baseValues(r)
	values["form"] = resetPasswordFormSpec()
	values["captcha_code"], values["captcha_svg"] = s.issueCaptchaPair(r)
	return s.render(w, r, "account/forgot_password.html", values)
}

func (s *Service) accountPage(w http.ResponseWriter, r *http.Request) error {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRequiredRedirect(w, r)
		return nil
	}
	summary, err := s.quota.Summary(r.Context(), user.ID())
	if err != nil {
		return err
	}
	values := s.baseValues(r)
	values["quota_summary"] = summary
	return s.render(w, r, "account/dashboard.html", values)
}

func (s *Service) securityPage(w http.ResponseWriter, r *http.Request) error {
	if httpapi.UserOf(r) == nil {
		loginRequiredRedirect(w, r)
		return nil
	}
	values := s.baseValues(r)
	values["captcha_code"], values["captcha_svg"] = s.issueCaptchaPair(r)
	return s.render(w, r, "account/security.html", values)
}

func (s *Service) historyPage(w http.ResponseWriter, r *http.Request) error {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRequiredRedirect(w, r)
		return nil
	}
	images, err := s.media.HistoryImages(r.Context(), user.ID(), 50)
	if err != nil {
		return err
	}
	values := s.baseValues(r)
	values["images"] = images
	return s.render(w, r, "account/history.html", values)
}

func (s *Service) historyDetailPage(w http.ResponseWriter, r *http.Request, taskNo string) error {
	user := httpapi.UserOf(r)
	if user == nil {
		loginRequiredRedirect(w, r)
		return nil
	}
	task, images, found, err := s.media.HistoryDetail(r.Context(), user.ID(), taskNo)
	if err != nil {
		return err
	}
	if !found {
		renderNotFound(w, r)
		return nil
	}
	values := s.baseValues(r)
	values["task"] = task
	values["images"] = images
	return s.render(w, r, "account/history_detail.html", values)
}

// ── shared context ──

func (s *Service) baseValues(r *http.Request) map[string]any {
	return map[string]any{
		"request":                map[string]any{"user": requestUser(httpapi.UserOf(r)), "GET": requestQuery(r)},
		"csrf_token":             csrfTokenOf(r), // read only when a template references it
		"SITE_NAME":              s.cfg.SiteName,
		"BRAND_NAME":             s.cfg.BrandName,
		"BRAND_ENGLISH_NAME":     s.cfg.BrandEnglishName,
		"BRAND_TAGLINE":          s.cfg.BrandTagline,
		"WORKSPACE_VITE_DEV_URL": s.cfg.WorkspaceViteURL,
		"CONSOLE_VITE_DEV_URL":   s.cfg.ConsoleViteURL,
	}
}

func (s *Service) render(w http.ResponseWriter, r *http.Request, name string, values map[string]any) error {
	csrfUsed := false
	body, err := s.engine.render(name, s.location, values, func() {
		if csrfUsed {
			return
		}
		csrfUsed = true
		// Django's CsrfViewMiddleware sets the cookie on any response whose
		// template rendered {% csrf_token %}.
		httpapi.SetCSRFCookie(w)
	})
	if err != nil {
		return err
	}
	writeHTML(w, http.StatusOK, body)
	return nil
}

func (s *Service) issueCaptcha(r *http.Request) string {
	code := generateCaptchaCode()
	if session := httpapi.SessionOf(r); session != nil {
		session.Set("captcha_code", code)
	}
	return code
}

func (s *Service) issueCaptchaPair(r *http.Request) (string, string) {
	code := s.issueCaptcha(r)
	return code, renderCaptchaDataURI(code)
}

func (s *Service) emailVerificationEnabled() bool {
	return s.site.Bool("email.verification_enabled", s.cfg.EmailVerification)
}

func (s *Service) releaseJSON() string {
	release := s.cfg.Release
	version := firstNonEmpty(s.site.String("site.workspace_release_version", release.Version), release.Version)
	title := firstNonEmpty(s.site.String("site.workspace_release_title", release.Title), release.Title)
	notes := firstNonEmpty(s.site.String("site.workspace_release_notes", release.Notes), release.Notes)
	publishedAt := firstNonEmpty(s.site.String("site.workspace_release_published_at", release.PublishedAt), release.PublishedAt)
	return pythonJSONDumps([][2]string{
		{"version", version},
		{"title", title},
		{"notes", notes},
		{"published_at", publishedAt},
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// ── forms ──

func loginFormSpec() *form {
	return newForm([]formField{
		{name: "username", inputType: "text", maxLength: 150, required: true},
		{name: "password", inputType: "password", required: true},
		{name: "captcha_code", inputType: "text", maxLength: 8, required: true},
	})
}

func (s *Service) registerFormSpec() *form {
	return newForm([]formField{
		{name: "username", inputType: "text", maxLength: 150, required: true},
		{name: "email", inputType: "email", maxLength: 320, required: true},
		inEmailCodeField(s.emailVerificationEnabled()),
		{name: "password", inputType: "password", required: true},
		{name: "confirm_password", inputType: "password", required: true},
	})
}

func inEmailCodeField(required bool) formField {
	return formField{
		name: "email_verification_code", inputType: "text", maxLength: 8, required: required,
		attrs: [][2]string{{"inputmode", "numeric"}},
	}
}

func resetPasswordFormSpec() *form {
	return newForm([]formField{
		{name: "username", inputType: "text", maxLength: 150, required: true},
		{name: "email", inputType: "email", maxLength: 320},
		{name: "new_password", inputType: "password", required: true},
		{name: "confirm_password", inputType: "password", required: true},
		{name: "captcha_code", inputType: "text", maxLength: 8, required: true},
	})
}

func (s *Service) loginSubmit(w http.ResponseWriter, r *http.Request) error {
	accountForm := loginFormSpec()
	accountForm.bind(postedValues(r))
	accountForm.validateRequired()
	accountForm.validateMaxLength()
	// clean_captcha_code runs only when the field itself passed, like the
	// reference field cleaner.
	if !accountForm.hasError("captcha_code") && !s.captchaMatches(r, accountForm.value("captcha_code")) {
		accountForm.addError("captcha_code", "验证码错误")
	}
	// Form.clean() runs even when individual fields failed, and a field that
	// failed validation contributes nothing to the cleaned data, so the
	// credential check fails the same way it does in the reference.
	var authenticated httpapi.Principal
	if s.auth != nil {
		if accountForm.hasError("username") || accountForm.hasError("password") {
			accountForm.addNonFieldError("账号或密码错误")
		} else {
			user, err := s.auth.Authenticate(r.Context(), accountForm.value("username"), accountForm.value("password"))
			switch {
			case err != nil || user == nil:
				accountForm.addNonFieldError("账号或密码错误")
			case !user.IsActiveStatus():
				accountForm.addNonFieldError("账号状态不可用")
			default:
				authenticated = user
			}
		}
	}
	if authenticated != nil {
		s.signIn(r, authenticated)
		redirectFound(w, s.loginSuccessURL(r))
		return nil
	}
	values := s.baseValues(r)
	values["form"] = accountForm
	values["captcha_code"], values["captcha_svg"] = s.issueCaptchaPair(r)
	return s.render(w, r, "account/login.html", values)
}

func (s *Service) registerSubmit(w http.ResponseWriter, r *http.Request) error {
	accountForm := s.registerFormSpec()
	accountForm.bind(postedValues(r))
	accountForm.validateRequired()
	accountForm.validateMaxLength()
	accountForm.validateEmail("email")
	if !accountForm.hasError("password") && accountForm.value("password") != accountForm.value("confirm_password") {
		accountForm.addNonFieldError("两次密码输入不一致")
	}
	if accountForm.valid() {
		user, err := s.auth.Register(
			r.Context(),
			strings.TrimSpace(accountForm.value("username")),
			strings.ToLower(strings.TrimSpace(accountForm.value("email"))),
			accountForm.value("password"),
			httpapi.From(r).ClientIP,
		)
		if err == nil && user != nil {
			s.signIn(r, user)
			redirectFound(w, "/workspace")
			return nil
		}
		accountForm.addNonFieldError(errorMessage(err, "注册失败，请稍后再试"))
	}
	values := s.baseValues(r)
	values["form"] = accountForm
	values["email_verification_enabled"] = s.emailVerificationEnabled()
	values["email_code_resend_seconds"] = s.site.Int("email.verification_resend_seconds", int(s.cfg.EmailResendInterval.Seconds()))
	return s.render(w, r, "account/register.html", values)
}

func (s *Service) forgotPasswordSubmit(w http.ResponseWriter, r *http.Request) error {
	accountForm := resetPasswordFormSpec()
	accountForm.bind(postedValues(r))
	accountForm.validateRequired()
	accountForm.validateMaxLength()
	accountForm.validateEmail("email")
	if !accountForm.hasError("captcha_code") && !s.captchaMatches(r, accountForm.value("captcha_code")) {
		accountForm.addError("captcha_code", "验证码错误")
	}
	if !accountForm.hasError("new_password") && accountForm.value("new_password") != accountForm.value("confirm_password") {
		accountForm.addNonFieldError("两次密码输入不一致")
	}
	if accountForm.valid() {
		user, err := s.auth.ByUsername(r.Context(), strings.TrimSpace(accountForm.value("username")))
		switch {
		case err != nil || user == nil:
			accountForm.addNonFieldError("用户不存在")
		case accountForm.value("email") != "" && user.Email() != "" &&
			!strings.EqualFold(user.Email(), strings.TrimSpace(accountForm.value("email"))):
			accountForm.addNonFieldError("邮箱不匹配")
		default:
			if err := s.auth.SetPassword(r.Context(), user.ID(), accountForm.value("new_password")); err != nil {
				return err
			}
			redirectFound(w, "/login")
			return nil
		}
	}
	values := s.baseValues(r)
	values["form"] = accountForm
	values["captcha_code"], values["captcha_svg"] = s.issueCaptchaPair(r)
	return s.render(w, r, "account/forgot_password.html", values)
}

func (s *Service) captchaMatches(r *http.Request, submitted string) bool {
	if !s.cfg.LoginCaptchaEnabled {
		return true
	}
	expected := ""
	if session := httpapi.SessionOf(r); session != nil {
		expected = session.Get("captcha_code")
	}
	if expected == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(submitted), expected)
}

func (s *Service) signIn(r *http.Request, user httpapi.Principal) {
	session := httpapi.SessionOf(r)
	if session == nil {
		return
	}
	session.SetInt("user_id", user.ID())
	session.CycleKey()
}

func (s *Service) loginSuccessURL(r *http.Request) string {
	if r.PostForm != nil {
		if target := r.PostForm.Get("next"); target != "" && allowedNext(target) {
			return target
		}
	}
	if target := r.URL.Query().Get("next"); target != "" && allowedNext(target) {
		return target
	}
	return "/workspace"
}

func allowedNext(target string) bool {
	if strings.HasPrefix(target, "//") || strings.Contains(target, "://") {
		return false
	}
	return strings.HasPrefix(target, "/")
}

func postedValues(r *http.Request) map[string]string {
	values := map[string]string{}
	_ = r.ParseForm()
	for key, entries := range r.PostForm {
		if len(entries) > 0 {
			values[key] = entries[0]
		}
	}
	return values
}

func errorMessage(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	if text := strings.TrimSpace(err.Error()); text != "" {
		return text
	}
	return fallback
}
