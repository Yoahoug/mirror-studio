package generator

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mirror-studio/internal/audit"
	"mirror-studio/internal/config"
	"mirror-studio/internal/crypt"
	"mirror-studio/internal/db"
	"mirror-studio/internal/httpapi"
	"mirror-studio/internal/media"
	"mirror-studio/internal/quota"
	"mirror-studio/internal/risk"
	"mirror-studio/internal/siteconfig"

	"mirror-studio/internal/imageutil"
)

// Ported from apps/generator/tests.py (GeneratorFlowTests). Method names and
// assertions follow the reference one for one; the omitted cases are listed in
// the package report.

// upstreamPNGBase64 is the payload the reference tests feed back from their
// fake upstream: it carries a valid PNG header (so dimensions are readable) but
// is not fully decodable, which is why it is only ever used as an upstream
// response, never as a reference upload.
const upstreamPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAEAAAABACAIAAAAlC+aJAAAAY0lEQVR4nO3PQQ0AIBDAsAP/nuGNAvZoFSzZOjNnyNi/A3gV0AqoFVAroFZAraBaAbUCagXUCqgVUCugVkCtgFoBtQJqBdQKqBVQK6BWQK2AWgG1AmoF1AqoFVAroFbwA5wBgrQMzDIAAAAASUVORK5CYII="

type testEnv struct {
	t       *testing.T
	handle  *db.DB
	cfg     *config.Config
	site    *siteconfig.Service
	quota   *quota.Service
	risk    *risk.Service
	media   *media.Service
	audit   *audit.Service
	service *Service
	server  *httptest.Server
	userID  int64
	channel int64

	workerCancel context.CancelFunc
}

type testPrincipal struct {
	id       int64
	username string
}

func (p testPrincipal) ID() int64            { return p.id }
func (p testPrincipal) Username() string     { return p.username }
func (p testPrincipal) IsOperator() bool     { return false }
func (p testPrincipal) IsActiveStatus() bool { return true }
func (p testPrincipal) Email() string        { return p.username + "@example.com" }

func newTestEnv(t *testing.T, options ...func(*config.Config)) *testEnv {
	t.Helper()
	dir := t.TempDir()
	handle, err := db.Open(filepath.Join(dir, "app.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	cfg := &config.Config{
		Debug:                    true,
		TimeZone:                 "UTC",
		SecretKey:                "test-secret-key-for-generator-tests",
		FieldCryptKey:            "test-field-crypt-key",
		MediaRoot:                filepath.Join(dir, "media"),
		DatabasePath:             filepath.Join(dir, "app.sqlite3"),
		MockProviderEnabled:      true,
		WorkerPollInterval:       200 * time.Millisecond,
		WorkerConcurrency:        2,
		DefaultDailyFreeQuota:    20,
		DailyUserTaskLimit:       50,
		MaxPendingTasks:          3,
		ImageRetentionDays:       30,
		ImagePreviewMaxDimension: 768,
		MaxReferenceImageBytes:   10 * 1024 * 1024,
		MaxRemoteImageBytes:      25 * 1024 * 1024,
		RemoteImageAllowedHosts:  []string{"s3.siliconflow.cn"},
		ProviderName:             "默认渠道",
		ProviderVendor:           "OpenAI-Compatible",
		ProviderVendorCode:       "openai",
		ProviderBaseURL:          "https://api.openai.com/v1",
		ProviderAPIPath:          "/images/generations",
		ProviderModelName:        "gpt-image-1",
		ProviderTimeoutSeconds:   60,
		ProviderMaxRetries:       1,
		OpenAIImageEditTimeout:   420 * time.Second,
		DefaultChannelCode:       "default",
		RiskLoginFailLimit:       5,
		RiskLoginLockMinutes:     15,
		RiskTaskWindow:           60 * time.Second,
		RiskTaskLimit:            10,
	}
	for _, apply := range options {
		apply(cfg)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cipher, err := crypt.NewFieldCipher(cfg.FieldCryptKey)
	if err != nil {
		t.Fatalf("field cipher: %v", err)
	}
	site := siteconfig.New(handle, cipher, logger)
	quotaService := quota.NewService(handle, cfg, site, logger)
	riskService := risk.NewService(handle, site, cfg, logger)
	mediaService := media.NewService(media.Deps{DB: handle, Config: cfg, Site: site, Logger: logger})
	auditService := audit.NewService(handle, logger)
	service := NewService(Deps{
		DB: handle, Config: cfg, Site: site, Location: time.UTC,
		Risk: riskService, Quota: quotaService, Media: mediaService,
		Audit: auditService, Cipher: cipher, Logger: logger,
	})

	env := &testEnv{
		t: t, handle: handle, cfg: cfg, site: site, quota: quotaService,
		risk: riskService, media: mediaService, audit: auditService, service: service,
	}
	env.userID = env.seedUser(t, "creator")
	env.channel = env.seedDefaultChannel(t)
	if _, err := quotaService.Ensure(context.Background(), env.userID); err != nil {
		t.Fatalf("ensure quota account: %v", err)
	}
	env.setQuota("available_quota", 1)
	env.mountHandlers()
	return env
}

func (e *testEnv) seedUser(t *testing.T, username string) int64 {
	t.Helper()
	now := db.FormatTime(time.Now())
	result, err := e.handle.Exec(
		`INSERT INTO users_user (password, last_login, is_superuser, username, first_name, last_name, is_staff, is_active, date_joined, email, mobile, role, status, last_login_ip, last_login_at)
		 VALUES ('x', NULL, 0, ?, '', '', 0, 1, ?, ?, NULL, 'user', 'active', NULL, NULL)`,
		username, now, username+"@example.com")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed user id: %v", err)
	}
	return id
}

func (e *testEnv) seedDefaultChannel(t *testing.T) int64 {
	t.Helper()
	now := db.FormatTime(time.Now())
	result, err := e.handle.Exec(
		`INSERT INTO generator_imagechannel (created_at, updated_at, name, channel_code, base_url, api_path, api_key_encrypted, model_name, timeout_seconds, max_retries, is_active, priority, channel_type, provider_name, edit_api_path, provider_vendor)
		 VALUES (?, ?, '默认渠道', 'default', 'https://api.openai.com/v1', '/images/generations', '', 'gpt-image-1', 60, 1, 1, 1, 'image', '', '', 'openai')`,
		now, now)
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed channel id: %v", err)
	}
	return id
}

func (e *testEnv) setQuota(column string, value int) {
	e.t.Helper()
	if _, err := e.handle.Exec(
		`UPDATE billing_quotaaccount SET `+column+` = ? WHERE user_id = ?`, value, e.userID); err != nil {
		e.t.Fatalf("update quota %s: %v", column, err)
	}
}

func (e *testEnv) quotaValue(column string) int {
	e.t.Helper()
	var value int
	if err := e.handle.QueryRow(
		`SELECT `+column+` FROM billing_quotaaccount WHERE user_id = ?`, e.userID).Scan(&value); err != nil {
		e.t.Fatalf("read quota %s: %v", column, err)
	}
	return value
}

func (e *testEnv) mountHandlers() {
	mux := http.NewServeMux()
	e.service.NewHandlers().Register(mux)
	sessions := httpapi.NewSessionStore(e.handle, false)
	middleware := httpapi.NewMiddleware(httpapi.Options{
		Config:   e.cfg,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sessions: sessions,
		ResolveUser: func(ctx context.Context, userID int64) (httpapi.Principal, error) {
			var username string
			if err := e.handle.QueryRow(`SELECT username FROM users_user WHERE id = ?`, userID).Scan(&username); err != nil {
				return nil, err
			}
			return testPrincipal{id: userID, username: username}, nil
		},
		IPBlocked: func(ctx context.Context, ip string) (bool, string) { return false, "" },
	}, httpapi.NewRouter(mux))
	e.server = httptest.NewServer(middleware)
	e.t.Cleanup(e.server.Close)
}

func (e *testEnv) startWorker() {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e.workerCancel = cancel
	e.service.StartWorker(ctx)
	e.t.Cleanup(func() {
		cancel()
		e.service.StopWorker()
	})
}

// runTask is the port of tasks.generate_image_task: claim then execute.
func (e *testEnv) runTask(taskNo string) error {
	e.t.Helper()
	task, err := e.service.claimTask(context.Background(), taskNo)
	if err != nil {
		return err
	}
	if task == nil {
		return nil
	}
	return e.service.executeClaimedTask(context.Background(), task)
}

func (e *testEnv) taskRow(taskNo string) *Task {
	e.t.Helper()
	task, err := e.service.taskByNoAny(context.Background(), taskNo)
	if err != nil || task == nil {
		e.t.Fatalf("load task %s: %v", taskNo, err)
	}
	return task
}

func (e *testEnv) imageCount(taskID int64) int {
	e.t.Helper()
	var count int
	if err := e.handle.QueryRow(
		`SELECT COUNT(1) FROM mediahub_generatedimage WHERE task_id = ?`, taskID).Scan(&count); err != nil {
		e.t.Fatalf("count images: %v", err)
	}
	return count
}

// ── HTTP client ──

type testClient struct {
	t       *testing.T
	base    string
	session string
	csrf    string
}

func (e *testEnv) client() *testClient {
	e.t.Helper()
	sessionKey := strings.Repeat("s", 32) + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := e.handle.QueryRow(`SELECT 1`).Scan(new(int)); err != nil {
		e.t.Fatalf("ping: %v", err)
	}
	expires := db.FormatTime(time.Now().Add(time.Hour))
	if _, err := e.handle.Exec(
		`INSERT INTO app_session (session_key, data, expires_at) VALUES (?, ?, ?)`,
		sessionKey, fmt.Sprintf(`{"user_id": %d}`, e.userID), expires); err != nil {
		e.t.Fatalf("seed session: %v", err)
	}
	const csrf = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789ab"
	return &testClient{t: e.t, base: e.server.URL, session: sessionKey, csrf: csrf}
}

func (c *testClient) request(method, path string, body any) (int, map[string]any, http.Header) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatalf("build request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(&http.Cookie{Name: "sessionid", Value: c.session})
	if method != http.MethodGet {
		request.AddCookie(&http.Cookie{Name: "csrftoken", Value: c.csrf})
		request.Header.Set("X-CSRFToken", c.csrf)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		c.t.Fatalf("request %s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		c.t.Fatalf("read response: %v", err)
	}
	payload := map[string]any{}
	if strings.Contains(response.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &payload); err != nil {
			c.t.Fatalf("decode response %s %s: %v (%s)", method, path, err, string(raw))
		}
	}
	return response.StatusCode, payload, response.Header
}

func (c *testClient) post(path string, body any) (int, map[string]any) {
	status, payload, _ := c.request(http.MethodPost, path, body)
	return status, payload
}

func (c *testClient) get(path string) (int, map[string]any) {
	status, payload, _ := c.request(http.MethodGet, path, nil)
	return status, payload
}

func (c *testClient) del(path string) (int, map[string]any) {
	status, payload, _ := c.request(http.MethodDelete, path, nil)
	return status, payload
}

func dataOf(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	value, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("response has no data object: %v", payload)
	}
	return value
}

// referencePNG builds the 64x64 reference upload the tests use, mirroring
// make_png_bytes() in the reference test suite.
func referencePNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 64, 64))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{color.RGBA{R: 47, G: 107, B: 255, A: 255}}, image.Point{}, draw.Src)
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, canvas); err != nil {
		t.Fatalf("encode fixture png: %v", err)
	}
	return buffer.Bytes()
}

func pngDataURL(t *testing.T) string {
	t.Helper()
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(referencePNG(t))
}

func upstreamPNG(t *testing.T) []byte {
	t.Helper()
	content, err := base64.StdEncoding.DecodeString(upstreamPNGBase64)
	if err != nil {
		t.Fatalf("decode upstream png: %v", err)
	}
	return content
}

// ── fake upstream ──

type upstreamCall struct {
	path          string
	contentType   string
	body          []byte
	authorization string
}

type fakeUpstream struct {
	server    *httptest.Server
	mu        sync.Mutex
	calls     []upstreamCall
	responses []fakeResponse
	index     int
}

type fakeResponse struct {
	status int
	body   string
	header map[string]string
}

func newFakeUpstream(t *testing.T, responses ...fakeResponse) *fakeUpstream {
	t.Helper()
	upstream := &fakeUpstream{responses: responses}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		upstream.mu.Lock()
		upstream.calls = append(upstream.calls, upstreamCall{
			path: r.URL.Path, contentType: r.Header.Get("Content-Type"),
			body: raw, authorization: r.Header.Get("Authorization"),
		})
		response := fakeResponse{status: 200, body: "{}"}
		if upstream.index < len(upstream.responses) {
			response = upstream.responses[upstream.index]
			upstream.index++
		} else if len(upstream.responses) > 0 {
			response = upstream.responses[len(upstream.responses)-1]
		}
		upstream.mu.Unlock()
		for key, value := range response.header {
			w.Header().Set(key, value)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.status)
		_, _ = io.WriteString(w, response.body)
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

func (u *fakeUpstream) recorded() []upstreamCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]upstreamCall{}, u.calls...)
}

// ── tests ──

func TestGenerateSuccessConsumesFrozenQuotaAndPersistsImage(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "一只橘猫在咖啡馆看报纸", StyleCode: "cinematic",
		SizeCode: "1024x1024", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := env.runTask(task.TaskNo); err != nil {
		t.Fatalf("run task: %v", err)
	}

	updated := env.taskRow(task.TaskNo)
	if updated.Status != StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", updated.Status)
	}
	if updated.ProgressPercent != 100 {
		t.Fatalf("progress = %d, want 100", updated.ProgressPercent)
	}
	if count := env.imageCount(updated.ID); count != 1 {
		t.Fatalf("images = %d, want 1", count)
	}
	if available := env.quotaValue("available_quota"); available != 0 {
		t.Fatalf("available_quota = %d, want 0", available)
	}
	if frozen := env.quotaValue("frozen_quota"); frozen != 0 {
		t.Fatalf("frozen_quota = %d, want 0", frozen)
	}
	if consumed := env.quotaValue("total_generated_count"); consumed != 1 {
		t.Fatalf("total_generated_count = %d, want 1", consumed)
	}
}

func TestDuplicateClientRequestIDReturnsExistingTaskWithoutExtraQuota(t *testing.T) {
	env := newTestEnv(t)
	first, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "同一个请求只创建一次", ClientRequestID: "req-001", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	second, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "同一个请求只创建一次", ClientRequestID: "req-001", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("second submit: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("task ids differ: %d vs %d", first.ID, second.ID)
	}
	var total int
	if err := env.handle.QueryRow(
		`SELECT COUNT(1) FROM generator_generatetask WHERE user_id = ?`, env.userID).Scan(&total); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if total != 1 {
		t.Fatalf("tasks = %d, want 1", total)
	}
	if available := env.quotaValue("available_quota"); available != 0 {
		t.Fatalf("available_quota = %d, want 0", available)
	}
	if frozen := env.quotaValue("frozen_quota"); frozen != 1 {
		t.Fatalf("frozen_quota = %d, want 1", frozen)
	}
}

func TestTaskCreateWaitReturnsFullProgressPayload(t *testing.T) {
	env := newTestEnv(t)
	env.startWorker()
	client := env.client()

	status, payload := client.post("/api/generate/tasks", map[string]any{
		"prompt":       "一只橘猫在窗边晒太阳",
		"style_code":   "photography",
		"size_code":    "1536x864",
		"quality_code": "low",
		"wait_seconds": 2,
	})
	if status != 200 {
		t.Fatalf("status = %d, want 200 (%v)", status, payload)
	}
	data := dataOf(t, payload)
	if data["status"] != StatusSucceeded {
		t.Fatalf("status = %v, want succeeded", data["status"])
	}
	if data["progress_percent"] != float64(100) {
		t.Fatalf("progress = %v, want 100", data["progress_percent"])
	}
	if data["size_code"] != "1536x864" {
		t.Fatalf("size_code = %v", data["size_code"])
	}
	if data["quality_code"] != "low" {
		t.Fatalf("quality_code = %v", data["quality_code"])
	}
	images, _ := data["images"].([]any)
	if len(images) != 1 {
		t.Fatalf("images = %d, want 1", len(images))
	}
	image := images[0].(map[string]any)
	if _, ok := image["preview_url"]; !ok {
		t.Fatalf("missing preview_url in %v", image)
	}
	if _, ok := image["download_url"]; !ok {
		t.Fatalf("missing download_url in %v", image)
	}
}

func TestSubmitGenerationTaskAccepts2KCustomSizeAndQuality(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "2K 自定义比例海报", SizeCode: "2048x1152", QualityCode: "high",
		ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if task.SizeCode != "2048x1152" {
		t.Fatalf("size_code = %q", task.SizeCode)
	}
	if task.QualityCode != "high" {
		t.Fatalf("quality_code = %q", task.QualityCode)
	}
}

func TestSubmitGenerationTaskRejectsInvalidSizeAndQuality(t *testing.T) {
	env := newTestEnv(t)
	cases := []struct {
		sizeCode    string
		qualityCode string
		message     string
	}{
		{"1537x864", "auto", "尺寸宽高必须是 16px 的倍数"},
		{"2048x512", "auto", "尺寸长短边比例不能超过 3:1"},
		{"640x640", "auto", "尺寸总像素过小，请提高分辨率"},
		{"2160x1216", "auto", "尺寸最大边不能超过 2048px"},
		{"1024x1024", "ultra", "不支持的质量"},
	}
	for _, testCase := range cases {
		t.Run(testCase.sizeCode+"-"+testCase.qualityCode, func(t *testing.T) {
			_, err := env.service.Submit(context.Background(), SubmitInput{
				UserID: env.userID, Prompt: "非法参数",
				SizeCode: testCase.sizeCode, QualityCode: testCase.qualityCode, ClientIP: "127.0.0.1",
			})
			if err == nil {
				t.Fatalf("expected error %q", testCase.message)
			}
			if err.Error() != testCase.message {
				t.Fatalf("message = %q, want %q", err.Error(), testCase.message)
			}
			if errorKindOf(err) != kindValidation {
				t.Fatalf("kind = %v, want validation", errorKindOf(err))
			}
		})
	}
}

func TestDispatchTaskKeepsTaskQueuedForWorkerPull(t *testing.T) {
	// The reference's dispatch function only runs the task inline when the
	// GENERATION_TASKS_RUN_INLINE / CELERY_TASK_ALWAYS_EAGER switches are on.
	// 0.0.1 has a single execution mode, so the observable contract is the one
	// asserted here: submitting leaves the task queued and untouched until the
	// worker claims it.
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "等待 worker 拉取", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	updated := env.taskRow(task.TaskNo)
	if updated.Status != StatusQueued {
		t.Fatalf("status = %q, want queued", updated.Status)
	}
	if updated.ExecutionAttempts != 0 {
		t.Fatalf("execution_attempts = %d, want 0", updated.ExecutionAttempts)
	}
}

func TestRunGenerationWorkerOncePullsQueuedTask(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "worker 主动拉取任务", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	processed, err := env.service.workerPass(context.Background(), 0)
	if err != nil {
		t.Fatalf("worker pass: %v", err)
	}
	if processed != task.TaskNo {
		t.Fatalf("processed = %q, want %q", processed, task.TaskNo)
	}
	updated := env.taskRow(task.TaskNo)
	if updated.Status != StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", updated.Status)
	}
	if updated.ExecutionAttempts != 1 {
		t.Fatalf("execution_attempts = %d, want 1", updated.ExecutionAttempts)
	}
}

func TestImageToImageTaskRequiresReferenceAndReturnsReferencePayload(t *testing.T) {
	env := newTestEnv(t)
	env.startWorker()
	client := env.client()

	status, payload := client.post("/api/generate/tasks", map[string]any{
		"prompt":                   "参考原图重绘成电商海报",
		"generation_mode":          "image_to_image",
		"reference_image_name":     "fruit.png",
		"reference_image_data_url": pngDataURL(t),
		"wait_seconds":             2,
	})
	if status != 200 {
		t.Fatalf("status = %d, want 200 (%v)", status, payload)
	}
	data := dataOf(t, payload)
	if data["generation_mode"] != "image_to_image" {
		t.Fatalf("generation_mode = %v", data["generation_mode"])
	}
	if data["reference_image_name"] != "fruit.png" {
		t.Fatalf("reference_image_name = %v", data["reference_image_name"])
	}
	if data["reference_image_attached"] != true {
		t.Fatalf("reference_image_attached = %v", data["reference_image_attached"])
	}
	if data["reference_image_data_url"] != "" {
		t.Fatalf("reference_image_data_url = %v, want empty", data["reference_image_data_url"])
	}

	// Image-to-image without any reference is refused with the reference text.
	status, payload = client.post("/api/generate/tasks", map[string]any{
		"prompt": "图生图缺少参考图", "generation_mode": "image_to_image",
	})
	if status != 400 || payload["code"] != float64(httpapi.CodeValidation) {
		t.Fatalf("missing reference: status=%d payload=%v", status, payload)
	}
	if payload["message"] != "图生图模式需要上传参考图" {
		t.Fatalf("message = %v", payload["message"])
	}
}

func TestReferenceUploadEndpointReturnsToken(t *testing.T) {
	env := newTestEnv(t)
	client := env.client()
	status, payload := client.post("/api/generate/reference-images", map[string]any{
		"reference_image_name":     "fruit.png",
		"reference_image_data_url": pngDataURL(t),
	})
	if status != 200 {
		t.Fatalf("status = %d, want 200 (%v)", status, payload)
	}
	data := dataOf(t, payload)
	token, _ := data["reference_upload_token"].(string)
	if token == "" {
		t.Fatal("reference_upload_token is empty")
	}
	if data["reference_image_name"] != "fruit.png" {
		t.Fatalf("reference_image_name = %v", data["reference_image_name"])
	}
	if data["reference_image_attached"] != true {
		t.Fatalf("reference_image_attached = %v", data["reference_image_attached"])
	}

	// An empty data URL is 请先上传参考图 / 400100.
	status, payload = client.post("/api/generate/reference-images", map[string]any{"reference_image_data_url": ""})
	if status != 400 || payload["code"] != float64(httpapi.CodeValidation) {
		t.Fatalf("empty upload: status=%d payload=%v", status, payload)
	}
	if payload["message"] != "请先上传参考图" {
		t.Fatalf("message = %v", payload["message"])
	}
}

func TestImageToImageTaskAcceptsReferenceUploadToken(t *testing.T) {
	env := newTestEnv(t)
	env.startWorker()
	client := env.client()

	status, payload := client.post("/api/generate/reference-images", map[string]any{
		"reference_image_name":     "fruit.png",
		"reference_image_data_url": pngDataURL(t),
	})
	if status != 200 {
		t.Fatalf("upload status = %d (%v)", status, payload)
	}
	upload := dataOf(t, payload)

	status, payload = client.post("/api/generate/tasks", map[string]any{
		"prompt":                 "参考原图重绘成电商海报",
		"generation_mode":        "image_to_image",
		"reference_image_name":   upload["reference_image_name"],
		"reference_upload_token": upload["reference_upload_token"],
		"wait_seconds":           2,
	})
	if status != 200 {
		t.Fatalf("task status = %d (%v)", status, payload)
	}
	data := dataOf(t, payload)
	if data["generation_mode"] != "image_to_image" {
		t.Fatalf("generation_mode = %v", data["generation_mode"])
	}
	if data["reference_image_name"] != "fruit.png" {
		t.Fatalf("reference_image_name = %v", data["reference_image_name"])
	}
	if data["reference_image_attached"] != true {
		t.Fatalf("reference_image_attached = %v", data["reference_image_attached"])
	}

	// A forged token is rejected with the reference's 400100 message.
	status, payload = client.post("/api/generate/tasks", map[string]any{
		"prompt": "伪造的凭证", "generation_mode": "image_to_image", "reference_upload_token": "bogus-token",
	})
	if status != 400 || payload["code"] != float64(httpapi.CodeValidation) {
		t.Fatalf("forged token: status=%d payload=%v", status, payload)
	}
	if payload["message"] != "参考图上传凭证已失效，请重新上传" {
		t.Fatalf("message = %v", payload["message"])
	}
}

func TestTaskListIsPaginatedAndExposesPreviewURLs(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "分页历史任务", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := env.runTask(task.TaskNo); err != nil {
		t.Fatalf("run task: %v", err)
	}
	client := env.client()

	status, payload := client.get("/api/generate/tasks?page=1&page_size=10")
	if status != 200 {
		t.Fatalf("status = %d (%v)", status, payload)
	}
	data := dataOf(t, payload)
	items, _ := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0].(map[string]any)
	if item["task_no"] != task.TaskNo {
		t.Fatalf("task_no = %v, want %v", item["task_no"], task.TaskNo)
	}
	images, _ := item["images"].([]any)
	if len(images) != 1 {
		t.Fatalf("images = %d, want 1", len(images))
	}
	if _, ok := images[0].(map[string]any)["preview_url"]; !ok {
		t.Fatalf("missing preview_url: %v", images[0])
	}
	pagination, _ := data["pagination"].(map[string]any)
	if pagination["total"] != float64(1) || pagination["total_pages"] != float64(1) {
		t.Fatalf("pagination = %v", pagination)
	}
}

type fakeNovelHistory struct {
	items []map[string]any
	total int
}

func (f fakeNovelHistory) matching(keyword string) []map[string]any {
	if keyword == "" {
		return f.items
	}
	needle := strings.ToLower(keyword)
	result := []map[string]any{}
	for _, item := range f.items {
		serialized, _ := item["__serialized"].(map[string]any)
		if strings.Contains(strings.ToLower(stringValueOf(serialized["book_filename"])), needle) {
			result = append(result, item)
		}
	}
	return result
}

func (f fakeNovelHistory) HistoryCandidates(ctx context.Context, userID int64, keyword string, limit int) ([]map[string]any, error) {
	items := f.matching(keyword)
	if limit < len(items) {
		return items[:limit], nil
	}
	return items, nil
}

func (f fakeNovelHistory) HistoryTotal(ctx context.Context, userID int64, keyword string) (int, error) {
	if keyword == "" {
		return f.total, nil
	}
	if len(f.matching(keyword)) == 0 {
		return 0, nil
	}
	return f.total, nil
}

func stringValueOf(value any) string {
	text, _ := value.(string)
	return text
}

func TestWorkspaceHistoryMergesNovelTasks(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "统一历史里的普通生图", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := env.runTask(task.TaskNo); err != nil {
		t.Fatalf("run task: %v", err)
	}
	// The novel row is one second newer so it sorts first.
	created := time.Now().UTC().Add(time.Second)
	serialized := map[string]any{
		"task_no": "novelviz:1", "task_kind": "novelviz", "novel_task_id": 1,
		"status": "failed", "book_filename": "story.epub", "images": []any{},
	}
	env.service.SetNovelHistory(fakeNovelHistory{
		items: []map[string]any{{"created_at": created.Format(time.RFC3339Nano), "id": int64(1), "__serialized": serialized}},
		total: 1,
	})
	client := env.client()

	status, payload := client.get("/api/workspace/history?page=1&page_size=10")
	if status != 200 {
		t.Fatalf("status = %d (%v)", status, payload)
	}
	data := dataOf(t, payload)
	items, _ := data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (%v)", len(items), items)
	}
	first := items[0].(map[string]any)
	if first["task_no"] != "novelviz:1" {
		t.Fatalf("first item = %v, want the novel task", first["task_no"])
	}
	second := items[1].(map[string]any)
	if second["task_no"] != task.TaskNo {
		t.Fatalf("second item = %v, want %v", second["task_no"], task.TaskNo)
	}
	pagination, _ := data["pagination"].(map[string]any)
	if pagination["total"] != float64(2) {
		t.Fatalf("total = %v, want 2", pagination["total"])
	}

	status, payload = client.get("/api/workspace/history?q=story")
	if status != 200 {
		t.Fatalf("keyword status = %d (%v)", status, payload)
	}
	data = dataOf(t, payload)
	items, _ = data["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["task_no"] != "novelviz:1" {
		t.Fatalf("keyword items = %v, want only the novel task", items)
	}
	pagination, _ = data["pagination"].(map[string]any)
	if pagination["total"] != float64(1) {
		t.Fatalf("keyword total = %v, want 1", pagination["total"])
	}
}

func TestDeleteHistoryHidesTaskAndMarksImageDeleted(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "需要删除的历史", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := env.runTask(task.TaskNo); err != nil {
		t.Fatalf("run task: %v", err)
	}
	client := env.client()

	status, payload := client.del("/api/generate/tasks/" + task.TaskNo)
	if status != 200 {
		t.Fatalf("status = %d (%v)", status, payload)
	}
	updated := env.taskRow(task.TaskNo)
	if !updated.IsDeleted {
		t.Fatal("task is not soft-deleted")
	}
	var imageStatus string
	if err := env.handle.QueryRow(
		`SELECT status FROM mediahub_generatedimage WHERE task_id = ?`, updated.ID).Scan(&imageStatus); err != nil {
		t.Fatalf("read image status: %v", err)
	}
	if imageStatus != "deleted" {
		t.Fatalf("image status = %q, want deleted", imageStatus)
	}

	_, payload = client.get("/api/generate/tasks")
	data := dataOf(t, payload)
	items, _ := data["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("items = %v, want empty", items)
	}
}

func TestDeleteRunningTaskCancelsAndHidesTask(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "删除运行中任务", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.handle.Exec(
		`UPDATE generator_generatetask SET status = 'running', started_at = ? WHERE id = ?`,
		db.FormatTime(time.Now()), task.ID); err != nil {
		t.Fatalf("force running: %v", err)
	}
	client := env.client()

	status, payload := client.del("/api/generate/tasks/" + task.TaskNo)
	if status != 200 {
		t.Fatalf("status = %d (%v)", status, payload)
	}
	updated := env.taskRow(task.TaskNo)
	if updated.Status != StatusCanceled {
		t.Fatalf("status = %q, want canceled", updated.Status)
	}
	if !updated.IsDeleted {
		t.Fatal("task is not soft-deleted")
	}
	if available := env.quotaValue("available_quota"); available != 1 {
		t.Fatalf("available_quota = %d, want 1", available)
	}
	if frozen := env.quotaValue("frozen_quota"); frozen != 0 {
		t.Fatalf("frozen_quota = %d, want 0", frozen)
	}
}

func TestTaskDetailAutoMarksStalledRunningTaskFailed(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "卡住的运行任务", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.handle.Exec(
		`UPDATE generator_generatetask SET status = 'running', started_at = ? WHERE id = ?`,
		db.FormatTime(time.Now().Add(-10*time.Minute)), task.ID); err != nil {
		t.Fatalf("force running: %v", err)
	}
	client := env.client()

	status, payload := client.get("/api/generate/tasks/" + task.TaskNo)
	if status != 200 {
		t.Fatalf("status = %d (%v)", status, payload)
	}
	data := dataOf(t, payload)
	if data["status"] != StatusFailed {
		t.Fatalf("status = %v, want failed", data["status"])
	}
	updated := env.taskRow(task.TaskNo)
	if updated.ErrorCode != "task_stalled" {
		t.Fatalf("error_code = %q, want task_stalled", updated.ErrorCode)
	}
	if available := env.quotaValue("available_quota"); available != 1 {
		t.Fatalf("available_quota = %d, want 1 (stalled task releases its freeze)", available)
	}
}

func TestReconcileRequeuesRunningTaskWithExpiredLease(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "租约过期后重新排队", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := env.handle.Exec(
		`UPDATE generator_generatetask SET status = 'running', started_at = ?, lease_expires_at = ?, execution_attempts = 1 WHERE id = ?`,
		db.FormatTime(time.Now().Add(-30*time.Second)), db.FormatTime(time.Now().Add(-time.Second)), task.ID); err != nil {
		t.Fatalf("force running: %v", err)
	}
	loaded := env.taskRow(task.TaskNo)
	reconciled := env.service.reconcileTaskRuntimeState(context.Background(), loaded)

	if reconciled.Status != StatusQueued {
		t.Fatalf("status = %q, want queued", reconciled.Status)
	}
	if reconciled.LeaseExpiresAt != nil {
		t.Fatalf("lease_expires_at = %v, want nil", reconciled.LeaseExpiresAt)
	}
	if reconciled.ExecutionAttempts != 1 {
		t.Fatalf("execution_attempts = %d, want 1", reconciled.ExecutionAttempts)
	}
	if reconciled.ProgressPercent < 8 || reconciled.ProgressPercent > 20 {
		t.Fatalf("progress = %d, want within 8..20", reconciled.ProgressPercent)
	}
}

func TestGenerateFailureReleasesFrozenQuota(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) { cfg.MockProviderEnabled = false })
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "失败任务", StyleCode: "general", SizeCode: "1024x1024",
		ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := env.runTask(task.TaskNo); err == nil {
		t.Fatal("expected the task to fail")
	}

	updated := env.taskRow(task.TaskNo)
	if updated.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", updated.Status)
	}
	if updated.ProgressPercent != 100 {
		t.Fatalf("progress = %d, want 100", updated.ProgressPercent)
	}
	if available := env.quotaValue("available_quota"); available != 1 {
		t.Fatalf("available_quota = %d, want 1", available)
	}
	if frozen := env.quotaValue("frozen_quota"); frozen != 0 {
		t.Fatalf("frozen_quota = %d, want 0", frozen)
	}
	if updated.ErrorMessage != "缺少 OpenAI 兼容渠道 API Key" {
		t.Fatalf("error_message = %q", updated.ErrorMessage)
	}
	failure := failureOf(t, updated.ProviderResponseJSON)
	if failure["exception_type"] != "ValueError" {
		t.Fatalf("exception_type = %v, want ValueError", failure["exception_type"])
	}
	if failure["attempts_used"] != float64(1) {
		t.Fatalf("attempts_used = %v, want 1", failure["attempts_used"])
	}

	var (
		payloadJSON string
		success     int
	)
	if err := env.handle.QueryRow(
		`SELECT payload_json, success FROM audit_callbackeventlog WHERE event_type = 'generation_failed' ORDER BY id DESC LIMIT 1`,
	).Scan(&payloadJSON, &success); err != nil {
		t.Fatalf("read callback log: %v", err)
	}
	if success != 0 {
		t.Fatalf("callback success = %d, want 0", success)
	}
	if !strings.Contains(payloadJSON, updated.TaskNo) {
		t.Fatalf("callback payload misses the task number: %s", payloadJSON)
	}
	var callback map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &callback); err != nil {
		t.Fatalf("callback payload is not JSON: %v", err)
	}
	if callback["error_message"] != "缺少 OpenAI 兼容渠道 API Key" {
		t.Fatalf("callback error_message = %v", callback["error_message"])
	}
	rawResponse, _ := callback["raw_response"].(map[string]any)
	if rawResponse["exception_type"] != "ValueError" {
		t.Fatalf("callback raw_response = %v", callback["raw_response"])
	}
	if rawResponse["attempts_used"] != float64(1) || rawResponse["max_attempts"] != float64(1) {
		t.Fatalf("callback attempts = %v", rawResponse)
	}
}

func TestFailedTaskAPIOnlyExposesGenericErrorToWorkspaceUser(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) { cfg.MockProviderEnabled = false })
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "失败任务对用户隐藏真实错误", StyleCode: "general",
		SizeCode: "1024x1024", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := env.runTask(task.TaskNo); err == nil {
		t.Fatal("expected the task to fail")
	}
	client := env.client()

	status, payload := client.get("/api/generate/tasks/" + task.TaskNo)
	if status != 200 {
		t.Fatalf("detail status = %d (%v)", status, payload)
	}
	if message := dataOf(t, payload)["error_message"]; message != publicFailureMessage {
		t.Fatalf("detail error_message = %v", message)
	}
	_, payload = client.get("/api/generate/tasks")
	items, _ := dataOf(t, payload)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if message := items[0].(map[string]any)["error_message"]; message != publicFailureMessage {
		t.Fatalf("list error_message = %v", message)
	}
	updated := env.taskRow(task.TaskNo)
	if updated.ErrorMessage != "缺少 OpenAI 兼容渠道 API Key" {
		t.Fatalf("stored error_message = %q", updated.ErrorMessage)
	}
}

func failureOf(t *testing.T, providerResponseJSON string) map[string]any {
	t.Helper()
	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(providerResponseJSON), &decoded); err != nil {
		t.Fatalf("decode provider_response_json %q: %v", providerResponseJSON, err)
	}
	failure, _ := decoded["failure"].(map[string]any)
	if failure == nil {
		t.Fatalf("provider_response_json has no failure object: %s", providerResponseJSON)
	}
	return failure
}

func TestRetryHappensInsideSameTaskRecord(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) { cfg.MockProviderEnabled = false })
	payload := base64.StdEncoding.EncodeToString(upstreamPNG(t))
	upstream := newFakeUpstream(t,
		fakeResponse{status: 500, body: `{"error": {"message": "temporary 500"}}`},
		fakeResponse{status: 502, body: `{"error": {"message": "temporary 502"}}`},
		fakeResponse{status: 200, body: `{"created": "ok", "data": [{"b64_json": "` + payload + `"}]}`},
	)
	if err := env.configureChannel(upstream, 3); err != nil {
		t.Fatalf("configure channel: %v", err)
	}
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "同一任务内部自动重试", StyleCode: "general",
		SizeCode: "1024x1024", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := env.runTask(task.TaskNo); err != nil {
		t.Fatalf("run task: %v", err)
	}

	updated := env.taskRow(task.TaskNo)
	if updated.Status != StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", updated.Status)
	}
	if count := env.imageCount(updated.ID); count != 1 {
		t.Fatalf("images = %d, want 1", count)
	}
	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(updated.ProviderResponseJSON), &decoded); err != nil {
		t.Fatalf("decode provider_response_json: %v", err)
	}
	meta, _ := decoded["_meta"].(map[string]any)
	if meta["attempts_used"] != float64(3) {
		t.Fatalf("attempts_used = %v, want 3", meta["attempts_used"])
	}
	if calls := len(upstream.recorded()); calls != 3 {
		t.Fatalf("upstream calls = %d, want 3", calls)
	}
}

func TestOpenAICompatibleChannelCanDecodeB64AndStoreImage(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) { cfg.MockProviderEnabled = false })
	upstream := newFakeUpstream(t, fakeResponse{
		status: 200,
		header: map[string]string{"x-request-id": "req-openai-1"},
		body: `{"created": 1777220495, "data": [{"b64_json": "` +
			base64.StdEncoding.EncodeToString(upstreamPNG(t)) +
			`", "revised_prompt": "一只蓝色小鸟站在茶杯上"}], "model": "gpt-image-2", "size": "1024x1024"}`,
	})
	if err := env.configureChannel(upstream, 1); err != nil {
		t.Fatalf("configure channel: %v", err)
	}
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "一只蓝色小鸟站在茶杯上", StyleCode: "general",
		SizeCode: "2048x1152", QualityCode: "medium", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := env.runTask(task.TaskNo); err != nil {
		t.Fatalf("run task: %v", err)
	}

	updated := env.taskRow(task.TaskNo)
	if updated.Status != StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", updated.Status)
	}
	if updated.ProviderRequestID != "req-openai-1" {
		t.Fatalf("provider_request_id = %q", updated.ProviderRequestID)
	}
	if count := env.imageCount(updated.ID); count != 1 {
		t.Fatalf("images = %d, want 1", count)
	}
	if !strings.Contains(updated.ProviderResponseJSON, `"b64_json": "<omitted:`) {
		t.Fatalf("base64 payload was not trimmed: %s", updated.ProviderResponseJSON)
	}
	if available := env.quotaValue("available_quota"); available != 0 {
		t.Fatalf("available_quota = %d, want 0", available)
	}
	if frozen := env.quotaValue("frozen_quota"); frozen != 0 {
		t.Fatalf("frozen_quota = %d, want 0", frozen)
	}

	calls := upstream.recorded()
	if len(calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(calls))
	}
	body := map[string]any{}
	if err := json.Unmarshal(calls[0].body, &body); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	keys := []string{}
	for key := range body {
		keys = append(keys, key)
	}
	if len(keys) != 4 {
		t.Fatalf("upstream JSON keys = %v, want exactly model/prompt/size/quality", keys)
	}
	for _, key := range []string{"model", "prompt", "size", "quality"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("upstream body misses %q: %v", key, body)
		}
	}
	if body["size"] != "2048x1152" {
		t.Fatalf("upstream size = %v", body["size"])
	}
	if body["quality"] != "medium" {
		t.Fatalf("upstream quality = %v", body["quality"])
	}
}

func TestOpenAICompatibleChannelSendsReferenceImageForImageToImage(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) { cfg.MockProviderEnabled = false })
	upstream := newFakeUpstream(t, fakeResponse{
		status: 200,
		header: map[string]string{"x-request-id": "req-openai-2"},
		body: `{"created": 1777220495, "data": [{"b64_json": "` +
			base64.StdEncoding.EncodeToString(upstreamPNG(t)) + `"}]}`,
	})
	if err := env.configureChannel(upstream, 1); err != nil {
		t.Fatalf("configure channel: %v", err)
	}
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "把原图重绘成海报", GenerationMode: ModeImageToImage,
		SizeCode: "1536x864", QualityCode: "high", ReferenceImageName: "ref.png",
		ReferenceImageDataURL: pngDataURL(t), ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := env.runTask(task.TaskNo); err != nil {
		t.Fatalf("run task: %v", err)
	}

	calls := upstream.recorded()
	if len(calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if !strings.HasSuffix(call.path, "/v1/images/edits") {
		t.Fatalf("edit path = %q, want /v1/images/edits", call.path)
	}
	if !strings.HasPrefix(call.contentType, "multipart/form-data") {
		t.Fatalf("content type = %q, want multipart/form-data", call.contentType)
	}

	reader := multipart.NewReader(bytes.NewReader(call.body), boundaryOf(t, call.contentType))
	fields := map[string]string{}
	var (
		imageName string
		imageType string
		imageData []byte
	)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read multipart: %v", err)
		}
		content, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FileName() != "" {
			imageName = part.FileName()
			imageType = part.Header.Get("Content-Type")
			imageData = content
			continue
		}
		fields[part.FormName()] = string(content)
	}
	if fields["model"] != "gpt-image-2" {
		t.Fatalf("model field = %q", fields["model"])
	}
	if fields["prompt"] != "把原图重绘成海报" {
		t.Fatalf("prompt field = %q", fields["prompt"])
	}
	if fields["size"] != "1536x864" {
		t.Fatalf("size field = %q", fields["size"])
	}
	if fields["quality"] != "high" {
		t.Fatalf("quality field = %q", fields["quality"])
	}
	if !strings.HasPrefix(imageName, "ref") {
		t.Fatalf("image filename = %q, want the reference stem", imageName)
	}
	if imageType != "image/jpeg" && imageType != "image/png" {
		t.Fatalf("image content type = %q", imageType)
	}
	if len(imageData) == 0 {
		t.Fatal("image part is empty")
	}
	if _, _, err := imageutil.Decode(imageData); err != nil {
		t.Fatalf("image part is not decodable: %v", err)
	}
}

func boundaryOf(t *testing.T, contentType string) string {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content type %q: %v", contentType, err)
	}
	return params["boundary"]
}

// configureChannel points the seeded channel at a test upstream and sets its key.
func (e *testEnv) configureChannel(upstream *fakeUpstream, maxRetries int) error {
	encrypted, err := e.service.cipher.Encrypt("compat-key")
	if err != nil {
		return err
	}
	now := db.FormatTime(time.Now())
	_, err = e.handle.Exec(
		`UPDATE generator_imagechannel
		 SET base_url = ?, api_path = '/v1/images/generations', model_name = 'gpt-image-2',
		     api_key_encrypted = ?, max_retries = ?, updated_at = ?
		 WHERE id = ?`,
		upstream.server.URL, encrypted, maxRetries, now, e.channel)
	return err
}

// ── concurrency, retry classification and size boundaries ──

func TestConcurrentClaimProcessesEachTaskExactlyOnce(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) { cfg.MaxPendingTasks = 64 })
	env.setQuota("available_quota", 64)
	env.setQuota("daily_quota", 64)
	const queued = 8
	ids := make([]int64, 0, queued)
	for index := 0; index < queued; index++ {
		task, err := env.service.Submit(context.Background(), SubmitInput{
			UserID: env.userID, Prompt: fmt.Sprintf("并发认领 %d", index), ClientIP: "127.0.0.1",
		})
		if err != nil {
			t.Fatalf("submit %d: %v", index, err)
		}
		ids = append(ids, task.ID)
	}

	claimed := make([]int64, 0, queued)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for worker := 0; worker < queued*2; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				task, err := env.service.claimTask(context.Background(), "")
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if task == nil {
					return
				}
				mu.Lock()
				claimed = append(claimed, task.ID)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(claimed) != queued {
		t.Fatalf("claimed %d tasks, want %d", len(claimed), queued)
	}
	seen := map[int64]int{}
	for _, id := range claimed {
		seen[id]++
	}
	for _, id := range ids {
		if seen[id] != 1 {
			t.Fatalf("task %d claimed %d times, want exactly once", id, seen[id])
		}
	}
	for _, id := range ids {
		var attempts int
		if err := env.handle.QueryRow(
			`SELECT execution_attempts FROM generator_generatetask WHERE id = ?`, id).Scan(&attempts); err != nil {
			t.Fatalf("read attempts: %v", err)
		}
		if attempts != 1 {
			t.Fatalf("task %d execution_attempts = %d, want 1", id, attempts)
		}
	}
}

func TestRetryClassification(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"no status", &providerRequestError{message: "network"}, true},
		{"status 500", &providerRequestError{message: "server", statusCode: intPointer(500)}, true},
		{"status 503", &providerRequestError{message: "server", statusCode: intPointer(503)}, true},
		{"status 408", &providerRequestError{message: "timeout", statusCode: intPointer(408)}, true},
		{"status 409", &providerRequestError{message: "conflict", statusCode: intPointer(409)}, true},
		{"status 425", &providerRequestError{message: "early", statusCode: intPointer(425)}, true},
		{"status 429", &providerRequestError{message: "slow down", statusCode: intPointer(429)}, true},
		{"status 400", &providerRequestError{message: "bad", statusCode: intPointer(400)}, false},
		{"status 401", &providerRequestError{message: "auth", statusCode: intPointer(401)}, false},
		{"status 404", &providerRequestError{message: "gone", statusCode: intPointer(404)}, false},
		{"status 422", &providerRequestError{message: "unprocessable", statusCode: intPointer(422)}, false},
		{"transport", &transportFailure{cause: fmt.Errorf("connection reset")}, true},
		{"timeout", &timeoutError{cause: context.DeadlineExceeded}, true},
		{"validation", validationFailure("参考图解析失败"), false},
		{"plain", fmt.Errorf("缺少 OpenAI 兼容渠道 API Key"), false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isRetryableError(testCase.err); got != testCase.retryable {
				t.Fatalf("isRetryableError(%v) = %v, want %v", testCase.err, got, testCase.retryable)
			}
		})
	}
}

func intPointer(value int) *int { return &value }

func TestNormalizeSizeCodeBoundaries(t *testing.T) {
	// Expectations produced by running apps/generator/services.py
	// normalize_size_code on the same inputs.
	cases := []struct {
		input   string
		want    string
		message string
	}{
		{"1024x1024", "1024x1024", ""},
		{"1536x864", "1536x864", ""},
		{"2048x1152", "2048x1152", ""},
		{"2048x1024", "2048x1024", ""},
		{"2048x2048", "2048x2048", ""},
		{"2048x688", "2048x688", ""},
		{"1536x512", "1536x512", ""},
		{"640x1024", "640x1024", ""},
		{"832x800", "832x800", ""},
		{"", "1024x1024", ""},
		{"2047x1024", "", "尺寸宽高必须是 16px 的倍数"},
		{"2048x2047", "", "尺寸宽高必须是 16px 的倍数"},
		{"2049x1152", "", "尺寸宽高必须是 16px 的倍数"},
		{"1920X1080", "", "尺寸宽高必须是 16px 的倍数"},
		{"2064x1168", "", "尺寸最大边不能超过 2048px"},
		{"2160x1216", "", "尺寸最大边不能超过 2048px"},
		{"3072x1024", "", "尺寸最大边不能超过 2048px"},
		{"2048x512", "", "尺寸长短边比例不能超过 3:1"},
		{"2048x672", "", "尺寸长短边比例不能超过 3:1"},
		{"1536x496", "", "尺寸长短边比例不能超过 3:1"},
		{"624x1024", "", "尺寸总像素过小，请提高分辨率"},
		{"816x800", "", "尺寸总像素过小，请提高分辨率"},
		{"32x32", "", "尺寸总像素过小，请提高分辨率"},
		{" ", "", "不支持的尺寸"},
		{"nope", "", "不支持的尺寸"},
	}
	for _, testCase := range cases {
		t.Run(testCase.input, func(t *testing.T) {
			got, err := normalizeSizeCode(testCase.input)
			if testCase.message == "" {
				if err != nil {
					t.Fatalf("normalizeSizeCode(%q) error: %v", testCase.input, err)
				}
				if got != testCase.want {
					t.Fatalf("normalizeSizeCode(%q) = %q, want %q", testCase.input, got, testCase.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("normalizeSizeCode(%q) = %q, want error %q", testCase.input, got, testCase.message)
			}
			if err.Error() != testCase.message {
				t.Fatalf("normalizeSizeCode(%q) error = %q, want %q", testCase.input, err.Error(), testCase.message)
			}
		})
	}
}

func TestNormalizeQualityCodeBoundaries(t *testing.T) {
	for _, value := range []string{"auto", "low", "medium", "high", "AUTO", " high "} {
		normalized, err := normalizeQualityCode(value)
		if err != nil {
			t.Fatalf("normalizeQualityCode(%q) error: %v", value, err)
		}
		if normalized != strings.ToLower(strings.TrimSpace(value)) {
			t.Fatalf("normalizeQualityCode(%q) = %q", value, normalized)
		}
	}
	normalized, err := normalizeQualityCode("")
	if err != nil || normalized != "auto" {
		t.Fatalf("normalizeQualityCode(\"\") = %q, %v", normalized, err)
	}
	// "AUTO " strips to a valid value; a whitespace-only one does not.
	if normalized, err := normalizeQualityCode("AUTO "); err != nil || normalized != "auto" {
		t.Fatalf("normalizeQualityCode(\"AUTO \") = %q, %v", normalized, err)
	}
	for _, value := range []string{"ultra", " "} {
		if _, err := normalizeQualityCode(value); err == nil {
			t.Fatalf("normalizeQualityCode(%q) accepted", value)
		}
	}
}

func TestMockProviderDrawsRequestedSize(t *testing.T) {
	env := newTestEnv(t)
	task, err := env.service.Submit(context.Background(), SubmitInput{
		UserID: env.userID, Prompt: "mock 尺寸", SizeCode: "1536x864", ClientIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := env.runTask(task.TaskNo); err != nil {
		t.Fatalf("run task: %v", err)
	}
	updated := env.taskRow(task.TaskNo)
	var (
		width, height int
		mimeType      string
	)
	if err := env.handle.QueryRow(
		`SELECT width, height, mime_type FROM mediahub_generatedimage WHERE task_id = ?`, updated.ID,
	).Scan(&width, &height, &mimeType); err != nil {
		t.Fatalf("read image: %v", err)
	}
	if width != 1536 || height != 864 {
		t.Fatalf("image size = %dx%d, want 1536x864", width, height)
	}
	if mimeType != "image/png" {
		t.Fatalf("mime = %q, want image/png", mimeType)
	}
	if updated.ProviderRequestID != "mock-"+task.TaskNo {
		t.Fatalf("provider_request_id = %q", updated.ProviderRequestID)
	}
	want := fmt.Sprintf(`{"mock": true, "task_no": "%s", "_meta": {"attempts_used": 1, "max_attempts": 1}}`, task.TaskNo)
	if updated.ProviderResponseJSON != want {
		t.Fatalf("provider_response_json =\n%s\nwant\n%s", updated.ProviderResponseJSON, want)
	}
}

func TestReferenceUploadTokenBindsUserAndExpiry(t *testing.T) {
	env := newTestEnv(t)
	meta := map[string]string{
		"reference_storage_provider": "local",
		"reference_bucket_name":      "generated-images",
		"reference_object_key":       "users/1/tasks/R1/reference.png",
		"reference_image_mime_type":  "image/png",
		"reference_image_size":       "199",
		"reference_image_width":      "96",
		"reference_image_height":     "96",
		"reference_image_sha256":     strings.Repeat("a", 64),
	}
	token := createReferenceUploadToken(env.cfg.SecretKey, 7, "fruit.png", meta)
	payload, err := resolveReferenceUploadToken(env.cfg.SecretKey, token, 7)
	if err != nil {
		t.Fatalf("resolve token: %v", err)
	}
	if intValue(payload["user_id"]) != 7 {
		t.Fatalf("user_id = %v", payload["user_id"])
	}
	if stringValue(payload["reference_object_key"]) != meta["reference_object_key"] {
		t.Fatalf("object key = %v", payload["reference_object_key"])
	}
	if _, err := resolveReferenceUploadToken(env.cfg.SecretKey, token, 8); errorKindOf(err) != kindPermission {
		t.Fatalf("other user error = %v, want permission", err)
	}
	if _, err := resolveReferenceUploadToken(env.cfg.SecretKey, "bogus", 7); errorKindOf(err) != kindValidation {
		t.Fatalf("bogus token error = %v, want validation", err)
	}
}

func TestDeleteUnknownTaskReportsMissing(t *testing.T) {
	env := newTestEnv(t)
	client := env.client()
	status, payload := client.del("/api/generate/tasks/NOSUCHTASK")
	if status != 400 || payload["code"] != float64(httpapi.CodeFileMissing) {
		t.Fatalf("delete unknown: status=%d payload=%v", status, payload)
	}
	status, payload = client.get("/api/generate/tasks/NOSUCHTASK")
	if status != 404 {
		t.Fatalf("detail unknown: status=%d payload=%v", status, payload)
	}
}

func TestResolveLLMChannelChatsWithoutExposingTheKey(t *testing.T) {
	env := newTestEnv(t)
	upstream := newFakeUpstream(t, fakeResponse{
		status: 200,
		body:   `{"choices": [{"message": {"content": "你好，我是绘镜助手"}}]}`,
	})
	encrypted, err := env.service.cipher.Encrypt("llm-secret-key")
	if err != nil {
		t.Fatalf("encrypt key: %v", err)
	}
	now := db.FormatTime(time.Now())
	if _, err := env.handle.Exec(
		`INSERT INTO generator_imagechannel (created_at, updated_at, name, channel_code, base_url, api_path, api_key_encrypted, model_name, timeout_seconds, max_retries, is_active, priority, channel_type, provider_name, edit_api_path, provider_vendor)
		 VALUES (?, ?, '对话渠道', 'chat', ?, '/v1/chat/completions', ?, 'diff-llm-model', 60, 1, 1, 5, 'llm', '', '', 'openai')`,
		now, now, upstream.server.URL, encrypted); err != nil {
		t.Fatalf("seed llm channel: %v", err)
	}

	channel, err := env.service.ResolveLLMChannel(context.Background(), "chat")
	if err != nil {
		t.Fatalf("resolve llm channel: %v", err)
	}
	if channel == nil || channel.ModelName != "diff-llm-model" {
		t.Fatalf("channel = %+v", channel)
	}
	content, err := channel.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "你好"}}, 0.7)
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if content != "你好，我是绘镜助手" {
		t.Fatalf("content = %q", content)
	}

	calls := upstream.recorded()
	if len(calls) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(calls))
	}
	if calls[0].authorization != "Bearer llm-secret-key" {
		t.Fatalf("authorization = %q", calls[0].authorization)
	}
	var body map[string]any
	if err := json.Unmarshal(calls[0].body, &body); err != nil {
		t.Fatalf("chat body: %v", err)
	}
	for _, key := range []string{"model", "messages", "temperature"} {
		if _, present := body[key]; !present {
			t.Fatalf("chat body misses %q: %s", key, calls[0].body)
		}
	}
	encoded, err := json.Marshal(channel)
	if err != nil {
		t.Fatalf("marshal channel: %v", err)
	}
	if strings.Contains(string(encoded), "llm-secret-key") {
		t.Fatalf("serialised channel leaks the key: %s", encoded)
	}

	if _, err := env.service.ResolveLLMChannel(context.Background(), "missing"); err != nil {
		t.Fatalf("unknown code error = %v, want nil (no active channel)", err)
	}
}
