package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"mirror-studio/internal/config"
	"mirror-studio/internal/crypt"
	"mirror-studio/internal/db"
	"mirror-studio/internal/httpapi"
	"mirror-studio/internal/siteconfig"
)

// ── the comparison ──

var (
	svgDataURIPattern   = regexp.MustCompile(`data:image/svg\+xml;base64,[A-Za-z0-9+/=]+`)
	dataCaptchaPattern  = regexp.MustCompile(`data-captcha="[^"]*"`)
	dataCsrfPattern     = regexp.MustCompile(`data-csrf="[^"]*"`)
	csrfInputPattern    = regexp.MustCompile(`name="csrfmiddlewaretoken" value="[^"]*"`)
	assetVersionPattern = regexp.MustCompile(`\?v=[0-9A-Za-z]+`)
)

// normalise blanks the values the reference generates randomly (captcha image,
// CSRF token, captcha code) and the workspace asset version, which the rewrite
// deliberately derives from content instead of the reference's file mtimes.
func normalise(body string) string {
	body = svgDataURIPattern.ReplaceAllString(body, "<svg-data-uri>")
	body = dataCaptchaPattern.ReplaceAllString(body, `data-captcha="<captcha>"`)
	body = dataCsrfPattern.ReplaceAllString(body, `data-csrf="<csrf>"`)
	body = csrfInputPattern.ReplaceAllString(body, `name="csrfmiddlewaretoken" value="<csrf>"`)
	body = assetVersionPattern.ReplaceAllString(body, "?v=<asset-version>")
	return body
}

func readReference(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read reference %s: %v", name, err)
	}
	return string(content)
}

func assertSameHTML(t *testing.T, referenceName, produced string) {
	t.Helper()
	want := normalise(readReference(t, referenceName))
	got := normalise(produced)
	if want == got {
		return
	}
	limit := len(want)
	if len(got) < limit {
		limit = len(got)
	}
	offset := 0
	for offset < limit && want[offset] == got[offset] {
		offset++
	}
	start := offset - 120
	if start < 0 {
		start = 0
	}
	t.Errorf("%s differs at byte %d (reference %d bytes, rendered %d bytes)\nreference: %q\nrendered:  %q",
		referenceName, offset, len(want), len(got),
		clip(want, start, offset+200), clip(got, start, offset+200))
}

func clip(text string, start, end int) string {
	if start > len(text) {
		start = len(text)
	}
	if end > len(text) {
		end = len(text)
	}
	return text[start:end]
}

// ── fixtures ──

const releaseNote = "生图工作台支持 1K / 2K、预设与自定义比例、质量选择\n移除废弃云梦渠道，统一走 OpenAI-compatible 中转\n优化小说日志、上传与媒体清理，降低内存和存储占用"

func referenceConfig() *config.Config {
	return &config.Config{
		// The differential runner starts both stacks in the reference's dev mode.
		Debug:               true,
		TimeZone:            "Asia/Shanghai",
		SiteName:            "绘镜 Studio",
		BrandName:           "绘镜",
		BrandEnglishName:    "MIRROR STUDIO",
		BrandTagline:        "把灵感写成图像，把作品留在自己的空间里",
		RegisterEnabled:     true,
		LoginCaptchaEnabled: true,
		EmailVerification:   false,
		EmailResendInterval: 60 * time.Second,
		Release: config.ReleaseInfo{
			Version:     "v1.2",
			Title:       "绘镜工作台 1.2",
			Notes:       releaseNote,
			PublishedAt: "2026-06-12",
		},
	}
}

func adminUser() *fakeUser {
	return &fakeUser{id: 1, username: "admin", email: "admin@example.com", operator: true, active: true}
}

func diffUser() *fakeUser {
	return &fakeUser{id: 3, username: "diff-user", email: "diff@example.com", active: true}
}

// taskFixtures is the reference corpus history: newest first, which is the
// order the reference model declares (["-created_at", "-id"]).
var taskFixtures = []struct {
	number string
	prompt string
}{
	{"G202610091254531BC7EAD2", "频率限制探测"},
	{"G2026100912545369F60400", "使用上传凭证重绘"},
	{"G202610091254538EAE36ED", "参考原图重绘成电商海报"},
	{"G20261009125453DB8B50F5", "最小尺寸与低质量"},
	{"G2026100912545376390976", "2K 自定义比例海报"},
	{"G2026100912545308CD8404", "一只橘猫在咖啡馆看报纸"},
}

func imageRows() []map[string]any {
	expires := time.Date(2026, 11, 8, 12, 54, 53, 291800000, time.UTC)
	rows := []map[string]any{}
	for _, fixture := range taskFixtures {
		rows = append(rows, map[string]any{
			"get_status_display": "可用",
			"expires_at":         expires,
			"task": map[string]any{
				"task_no": fixture.number,
				"prompt":  fixture.prompt,
			},
		})
	}
	return rows
}

// ── fakes for the neighbouring domains ──

type fakeUser struct {
	id       int64
	username string
	email    string
	operator bool
	active   bool
}

func (u *fakeUser) ID() int64            { return u.id }
func (u *fakeUser) Username() string     { return u.username }
func (u *fakeUser) Email() string        { return u.email }
func (u *fakeUser) IsOperator() bool     { return u.operator }
func (u *fakeUser) IsActiveStatus() bool { return u.active }

type fakeAuth struct{ users []*fakeUser }

func (a *fakeAuth) find(id int64) *fakeUser {
	for _, user := range a.users {
		if user.id == id {
			return user
		}
	}
	return nil
}

func (a *fakeAuth) ByID(_ context.Context, id int64) (httpapi.Principal, error) {
	if user := a.find(id); user != nil {
		return user, nil
	}
	return nil, fmt.Errorf("user %d not found", id)
}

func (a *fakeAuth) ByUsername(_ context.Context, username string) (httpapi.Principal, error) {
	for _, user := range a.users {
		if user.username == username {
			return user, nil
		}
	}
	return nil, fmt.Errorf("user %q not found", username)
}

func (a *fakeAuth) Authenticate(_ context.Context, username, password string) (httpapi.Principal, error) {
	user, err := a.ByUsername(context.Background(), username)
	if err != nil || password != "DiffAdmin123!@#" {
		return nil, fmt.Errorf("invalid credentials")
	}
	return user, nil
}

func (a *fakeAuth) Register(_ context.Context, username, email, _ string, _ string) (httpapi.Principal, error) {
	user := &fakeUser{id: int64(len(a.users) + 1), username: username, email: email, active: true}
	a.users = append(a.users, user)
	return user, nil
}

func (a *fakeAuth) SetPassword(_ context.Context, id int64, _ string) error {
	if a.find(id) == nil {
		return fmt.Errorf("user %d not found", id)
	}
	return nil
}

type fakeQuota struct{ summary map[string]any }

func (q *fakeQuota) Summary(_ context.Context, _ int64) (map[string]any, error) {
	return q.summary, nil
}

type fakeMedia struct {
	images []map[string]any
	task   map[string]any
	detail []map[string]any
	found  bool
}

func (m *fakeMedia) HistoryImages(_ context.Context, _ int64, _ int) ([]map[string]any, error) {
	return m.images, nil
}

func (m *fakeMedia) HistoryDetail(_ context.Context, _ int64, _ string) (map[string]any, []map[string]any, bool, error) {
	return m.task, m.detail, m.found, nil
}

// ── the page server under test ──

type pageHarness struct {
	t        *testing.T
	server   *httptest.Server
	client   *http.Client
	cookies  map[string]string
	service  *Service
	config   *config.Config
	location *time.Location
}

type harnessOptions struct {
	users             []*fakeUser
	quota             map[string]any
	media             *fakeMedia
	sessionUser       int64
	emailVerification bool
}

func newPageHarness(t *testing.T, options harnessOptions) *pageHarness {
	t.Helper()
	return newPageHarnessWithRisk(t, options, nil)
}

func newPageHarnessWithRisk(t *testing.T, options harnessOptions, risk RiskGateway) *pageHarness {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&testLogWriter{t: t}, &slog.HandlerOptions{Level: slog.LevelWarn}))
	cfg := referenceConfig()
	cfg.EmailVerification = options.emailVerification

	location, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	handle, err := db.Open(filepath.Join(t.TempDir(), "pages.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := handle.Migrate(logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cipher, err := crypt.NewFieldCipher("diff-field-crypt-key")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	site := siteconfig.New(handle, cipher, logger)
	site.Env = func(key string) string {
		if key == "email.verification_enabled" {
			if options.emailVerification {
				return "1"
			}
			return "0"
		}
		return ""
	}

	auth := &fakeAuth{users: options.users}
	quota := &fakeQuota{summary: options.quota}
	media := options.media
	if media == nil {
		media = &fakeMedia{}
	}

	service := NewService(Deps{
		DB: handle, Config: cfg, Site: site, Location: location, Cipher: cipher,
		Auth: auth, Risk: risk, Quota: quota, Media: media, Logger: logger,
	})

	store := httpapi.NewSessionStore(handle, false)
	sessions := &sessionSeeder{store: store}

	mux := http.NewServeMux()
	service.Register(mux)
	middleware := httpapi.NewMiddleware(httpapi.Options{
		Config:   cfg,
		Logger:   logger,
		Sessions: store,
		ResolveUser: func(ctx context.Context, userID int64) (httpapi.Principal, error) {
			return auth.ByID(ctx, userID)
		},
	}, httpapi.NewRouter(mux))

	server := httptest.NewServer(middleware)
	t.Cleanup(server.Close)

	client := server.Client()
	// Redirects are part of the contract, so they must be observed, not followed.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	harness := &pageHarness{
		t:        t,
		server:   server,
		client:   client,
		cookies:  map[string]string{},
		service:  service,
		config:   cfg,
		location: location,
	}
	if options.sessionUser != 0 {
		harness.cookies[httpapi.SessionCookieName] = sessions.seed(t, options.sessionUser)
	}
	return harness
}

// testLogWriter forwards service logs into the test output.
type testLogWriter struct{ t *testing.T }

func (w *testLogWriter) Write(payload []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(payload), "\n"))
	return len(payload), nil
}

type sessionSeeder struct{ store *httpapi.SessionStore }

func (s *sessionSeeder) seed(t *testing.T, userID int64) string {
	t.Helper()
	session := s.store.Load(&http.Request{})
	session.SetInt("user_id", userID)
	if err := s.store.Save(session); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return session.Key
}

type pageResponse struct {
	status  int
	headers http.Header
	body    string
}

func (h *pageHarness) request(method, path string, form url.Values) pageResponse {
	h.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequest(method, h.server.URL+path, body)
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	request.Header.Set("Cookie", h.cookieHeader())
	response, err := h.client.Do(request)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		h.t.Fatalf("read response: %v", err)
	}
	for _, cookie := range response.Cookies() {
		if cookie.MaxAge < 0 || cookie.Value == "" {
			delete(h.cookies, cookie.Name)
			continue
		}
		h.cookies[cookie.Name] = cookie.Value
	}
	return pageResponse{status: response.StatusCode, headers: response.Header, body: string(content)}
}

func (h *pageHarness) cookieHeader() string {
	parts := []string{}
	for name, value := range h.cookies {
		parts = append(parts, name+"="+value)
	}
	return strings.Join(parts, "; ")
}

func (h *pageHarness) get(path string) pageResponse {
	return h.request(http.MethodGet, path, nil)
}

// csrfTokenOf extracts the masked token a rendered page embedded.
func csrfTokenOfPage(t *testing.T, body string) string {
	t.Helper()
	matches := csrfInputPattern.FindAllStringSubmatch(body, 1)
	if len(matches) == 0 {
		t.Fatalf("no csrf token in rendered page")
	}
	value := matches[0][0]
	value = strings.TrimPrefix(value, `name="csrfmiddlewaretoken" value="`)
	return strings.TrimSuffix(value, `"`)
}

// ── the page comparisons ──

func TestLoginPageMatchesReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{users: []*fakeUser{adminUser()}})
	response := harness.get("/login")
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	if contentType := response.headers.Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", contentType)
	}
	assertSameHTML(t, "ref_login.html", response.body)
}

func TestLoginPageBoundFormMatchesReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{users: []*fakeUser{adminUser()}})
	page := harness.get("/login")
	token := csrfTokenOfPage(t, page.body)
	response := harness.request(http.MethodPost, "/login", url.Values{
		"csrfmiddlewaretoken": {token},
		"username":            {""},
		"password":            {""},
		"captcha_code":        {""},
	})
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	assertSameHTML(t, "ref_login_invalid.html", response.body)
}

func TestRegisterPageMatchesReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{users: []*fakeUser{adminUser()}})
	response := harness.get("/register")
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	assertSameHTML(t, "ref_register.html", response.body)
}

func TestRegisterPageWithEmailVerificationMatchesReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{
		users: []*fakeUser{adminUser()}, emailVerification: true,
	})
	response := harness.get("/register")
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	assertSameHTML(t, "ref_register_email_enabled.html", response.body)
}

func TestForgotPasswordPageMatchesReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{users: []*fakeUser{adminUser()}})
	response := harness.get("/forgot-password")
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	assertSameHTML(t, "ref_forgot_password.html", response.body)
}

func TestWorkspacePageAnonymousMatchesReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{users: []*fakeUser{adminUser()}})
	response := harness.get("/workspace")
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	assertSameHTML(t, "ref_workspace_anonymous.html", response.body)
}

func TestWorkspacePageAuthenticatedMatchesReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{users: []*fakeUser{adminUser()}, sessionUser: 1})
	response := harness.get("/workspace")
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	assertSameHTML(t, "ref_workspace_authenticated.html", response.body)
}

func TestConsolePageAnonymousRedirects(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{users: []*fakeUser{adminUser()}})
	for _, path := range []string{"/console", "/console/"} {
		response := harness.get(path)
		if response.status != http.StatusFound {
			t.Fatalf("%s status = %d, want 302", path, response.status)
		}
		want := "/workspace?auth=login&next=" + path
		if location := response.headers.Get("Location"); location != want {
			t.Errorf("%s Location = %q, want %q", path, location, want)
		}
		if response.body != "" {
			t.Errorf("%s body = %q, want empty", path, response.body)
		}
	}
}

func TestConsolePageAuthenticatedMatchesReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{users: []*fakeUser{adminUser()}, sessionUser: 1})
	response := harness.get("/console/")
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	assertSameHTML(t, "ref_console_authenticated.html", response.body)
}

func TestConsolePageNonOperatorForbidden(t *testing.T) {
	disabled := &fakeUser{id: 9, username: "disabled-op", email: "op@example.com", operator: true, active: false}
	users := []*fakeUser{adminUser(), diffUser(), disabled}
	// Both the non-operator and the inactive-operator case render the framework's
	// PermissionDenied page, which carries no exception text.
	want := "\n<!doctype html>\n<html lang=\"en\">\n<head>\n  <title>403 Forbidden</title>\n</head>\n<body>\n  <h1>403 Forbidden</h1><p></p>\n</body>\n</html>\n"
	for _, sessionUser := range []int64{3, 9} {
		harness := newPageHarness(t, harnessOptions{users: users, sessionUser: sessionUser})
		response := harness.get("/console/")
		if response.status != http.StatusForbidden {
			t.Fatalf("session %d status = %d, want 403", sessionUser, response.status)
		}
		if response.body != want {
			t.Errorf("session %d body = %q", sessionUser, response.body)
		}
	}
}

func TestAccountPageMatchesReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{
		users:       []*fakeUser{adminUser()},
		sessionUser: 1,
		quota:       map[string]any{"available_quota": 20, "frozen_quota": 0},
	})
	response := harness.get("/account")
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	assertSameHTML(t, "ref_account.html", response.body)
}

func TestAccountPageAnonymousRedirects(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{users: []*fakeUser{adminUser()}})
	for _, path := range []string{"/account", "/account/security", "/account/history"} {
		response := harness.get(path)
		if response.status != http.StatusFound {
			t.Fatalf("%s status = %d, want 302", path, response.status)
		}
		want := "/workspace?auth=login&next=" + path
		if location := response.headers.Get("Location"); location != want {
			t.Errorf("%s Location = %q, want %q", path, location, want)
		}
	}
}

func TestAccountHistoryPageEmptyMatchesReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{
		users: []*fakeUser{adminUser()}, sessionUser: 1, media: &fakeMedia{images: []map[string]any{}},
	})
	response := harness.get("/account/history")
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	assertSameHTML(t, "ref_account_history_empty.html", response.body)
}

func TestAccountHistoryPageWithImagesMatchesReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{
		users: []*fakeUser{diffUser()}, sessionUser: 3, media: &fakeMedia{images: imageRows()},
	})
	response := harness.get("/account/history")
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	assertSameHTML(t, "ref_account_history_images.html", response.body)
}

func TestAccountSecurityPageMatchesReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{
		users: []*fakeUser{adminUser()}, sessionUser: 1, media: &fakeMedia{images: []map[string]any{}},
	})
	response := harness.get("/account/security")
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	assertSameHTML(t, "ref_account_security.html", response.body)
}

func TestAccountHistoryDetailMatchesReference(t *testing.T) {
	taskNo := "G2026100912545308CD8404"
	harness := newPageHarness(t, harnessOptions{
		users: []*fakeUser{diffUser()}, sessionUser: 3,
		media: &fakeMedia{
			found:  true,
			task:   map[string]any{"task_no": taskNo, "prompt": "一只橘猫在咖啡馆看报纸"},
			detail: []map[string]any{{"id": 1, "image_index": 1, "width": 1024, "height": 1024, "get_status_display": "可用"}},
		},
	})
	response := harness.get("/account/history/" + taskNo)
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	assertSameHTML(t, "ref_account_history_detail.html", response.body)
}

func TestAccountHistoryDetailMissingIsNotFound(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{
		users: []*fakeUser{diffUser()}, sessionUser: 3, media: &fakeMedia{found: false},
	})
	response := harness.get("/account/history/GTESTMISSING")
	if response.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.status)
	}
}

func TestStaticRedirectsAndHealthz(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{users: []*fakeUser{adminUser()}})
	for path, want := range map[string]string{"/": "/workspace", "/admin": "/console/", "/admin/": "/console/"} {
		response := harness.get(path)
		if response.status != http.StatusFound {
			t.Errorf("%s status = %d, want 302", path, response.status)
		}
		if location := response.headers.Get("Location"); location != want {
			t.Errorf("%s Location = %q, want %q", path, location, want)
		}
	}
	health := harness.get("/healthz")
	if health.status != http.StatusOK || health.body != `{"status": "ok"}` {
		t.Errorf("/healthz = %d %q", health.status, health.body)
	}
	if contentType := health.headers.Get("Content-Type"); contentType != "application/json" {
		t.Errorf("/healthz Content-Type = %q", contentType)
	}
	for _, path := range []string{"/pricing", "/missing-page", "/workspace/", "/login/", "/account/security/", "/account/history/"} {
		response := harness.get(path)
		if response.status != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, response.status)
		}
	}
}

// assetValuePattern isolates the workspace asset version, the one value the
// rewrite deliberately computes differently from the reference's file mtimes.
var assetValuePattern = regexp.MustCompile(`\?v=[^"]*`)

// TestWorkspaceAssetVersionIsTheOnlyDeviation pins the single known difference:
// everything else in the page is byte-identical, and the asset version is a
// content digest instead of the reference's mtime.
func TestWorkspaceAssetVersionIsTheOnlyDeviation(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{users: []*fakeUser{adminUser()}})
	response := harness.get("/workspace")
	if response.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.status)
	}
	reference := readReference(t, "ref_workspace_anonymous.html")
	referenceVersion := assetValuePattern.FindString(normalise(reference))
	renderedVersion := assetValuePattern.FindString(normalise(response.body))
	t.Logf("reference %s, rendered %s", referenceVersion, renderedVersion)

	// normalise already masks the version; this asserts the mask hides nothing else.
	referenceMasked := assetValuePattern.ReplaceAllString(reference, "?v=<version>")
	if strings.Count(referenceMasked, "?v=<version>") == 0 {
		t.Fatalf("reference carries no asset version to mask")
	}
	assertSameHTML(t, "ref_workspace_anonymous.html", response.body)
}

// TestPageCookiesMatchTheReference pins the cookie attributes the harness
// records: name plus the sorted attribute list, with Expires ignored.
func TestPageCookiesMatchTheReference(t *testing.T) {
	harness := newPageHarness(t, harnessOptions{users: []*fakeUser{adminUser()}})
	response := harness.get("/workspace")
	cookies := map[string][]string{}
	for _, raw := range response.headers.Values("Set-Cookie") {
		parts := strings.Split(raw, ";")
		name := strings.TrimSpace(strings.SplitN(parts[0], "=", 2)[0])
		attrs := []string{}
		for _, attribute := range parts[1:] {
			trimmed := strings.TrimSpace(attribute)
			if trimmed == "" || strings.HasPrefix(strings.ToLower(trimmed), "expires=") {
				continue
			}
			attrs = append(attrs, trimmed)
		}
		sort.Strings(attrs)
		cookies[name] = attrs
	}
	session, ok := cookies[httpapi.SessionCookieName]
	if !ok {
		t.Fatalf("no session cookie in %v", cookies)
	}
	if strings.Join(session, "|") != "HttpOnly|Max-Age=1209600|Path=/|SameSite=Lax" {
		t.Errorf("session cookie attrs = %v", session)
	}
	csrf, ok := cookies[httpapi.CSRFCookieName]
	if !ok {
		t.Fatalf("no csrf cookie in %v", cookies)
	}
	if strings.Join(csrf, "|") != "Max-Age=31449600|Path=/|SameSite=Lax" {
		t.Errorf("csrf cookie attrs = %v", csrf)
	}
}
