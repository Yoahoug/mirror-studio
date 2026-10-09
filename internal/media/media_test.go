package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mirror-studio/internal/config"
	"mirror-studio/internal/crypt"
	"mirror-studio/internal/db"
	"mirror-studio/internal/httpapi"
)

const csrfToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type testPrincipal struct {
	id       int64
	username string
}

func (p *testPrincipal) ID() int64            { return p.id }
func (p *testPrincipal) Username() string     { return p.username }
func (p *testPrincipal) IsOperator() bool     { return false }
func (p *testPrincipal) IsActiveStatus() bool { return true }
func (p *testPrincipal) Email() string        { return "" }

type testEnv struct {
	t       *testing.T
	dir     string
	cfg     *config.Config
	db      *db.DB
	service *Service
	server  *httptest.Server
	client  *http.Client
	userIDs map[string]int64
	channel int64
}

func newTestEnv(t *testing.T, mutate ...func(*config.Config)) *testEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		SecretKey:                 "0123456789abcdef0123456789abcdef",
		MediaRoot:                 dir,
		ImageRetentionDays:        30,
		SoftDeleteRetentionDays:   7,
		ImagePreviewMaxDimension:  768,
		MaxReferenceImageBytes:    10 * 1024 * 1024,
		MaxRemoteImageBytes:       25 * 1024 * 1024,
		RemoteImageAllowedHosts:   []string{"s3.siliconflow.cn"},
		ResumableUploadChunkSize:  8,
		ResumableUploadSessionTTL: 24 * time.Hour,
		NovelvizMaxEpubBytes:      80 * 1024 * 1024,
	}
	for _, apply := range mutate {
		apply(cfg)
	}

	handle, err := db.Open(filepath.Join(dir, "app.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	env := &testEnv{
		t:       t,
		dir:     dir,
		cfg:     cfg,
		db:      handle,
		userIDs: map[string]int64{},
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	env.service = NewService(Deps{
		DB:     handle,
		Config: cfg,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	env.seedUser("owner")
	env.seedUser("other")
	mux := http.NewServeMux()
	env.service.NewHandlers().Register(mux)
	middleware := httpapi.NewMiddleware(httpapi.Options{
		Config:   cfg,
		Sessions: httpapi.NewSessionStore(handle, false),
		ResolveUser: func(ctx context.Context, userID int64) (httpapi.Principal, error) {
			var username string
			if err := handle.QueryRow(`SELECT username FROM users_user WHERE id = ?`, userID).Scan(&username); err != nil {
				return nil, err
			}
			return &testPrincipal{id: userID, username: username}, nil
		},
	}, httpapi.NewRouter(mux))
	server := httptest.NewServer(middleware)
	t.Cleanup(server.Close)
	env.server = server
	return env
}

func (env *testEnv) seedUser(username string) int64 {
	env.t.Helper()
	now := db.FormatTime(time.Now())
	result, err := env.db.Exec(`INSERT INTO users_user
		(password, is_superuser, username, first_name, last_name, is_staff, is_active, date_joined,
		 email, role, status)
		VALUES ('x', 0, ?, '', '', 0, 1, ?, ?, 'user', 'active')`,
		username, now, username+"@example.com")
	if err != nil {
		env.t.Fatalf("seed user: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		env.t.Fatalf("seed user id: %v", err)
	}
	env.userIDs[username] = id
	key := env.sessionKey(id)
	if _, err := env.db.Exec(`INSERT INTO app_session (session_key, data, expires_at) VALUES (?, ?, ?)`,
		key, `{"user_id":`+strconv.FormatInt(id, 10)+`}`, db.FormatTime(time.Now().Add(time.Hour))); err != nil {
		env.t.Fatalf("seed session: %v", err)
	}
	return id
}

func (env *testEnv) sessionKey(userID int64) string {
	return fmt.Sprintf("sessionkey-%d-%s", userID, strings.Repeat("0", 20))
}

func (env *testEnv) user(name string) int64 {
	env.t.Helper()
	id, ok := env.userIDs[name]
	if !ok {
		env.t.Fatalf("unknown user %q", name)
	}
	return id
}

// seedTask inserts the minimal task row an image belongs to and returns its id.
func (env *testEnv) seedTask(userID int64, taskNo, prompt string, deleted bool) int64 {
	env.t.Helper()
	now := db.FormatTime(time.Now())
	result, err := env.db.Exec(`INSERT INTO generator_generatetask
		(created_at, updated_at, task_no, status, prompt, negative_prompt, style_code, size_code,
		 quota_cost, provider_request_id, provider_response_json, error_code, error_message,
		 submitted_at, client_request_id, quota_finalized, user_id, progress_percent, is_deleted,
		 generation_mode, quality_code, reference_image_name, reference_image_data_url,
		 reference_storage_provider, reference_bucket_name, reference_object_key,
		 reference_image_mime_type, reference_image_size, reference_image_width,
		 reference_image_height, reference_image_sha256, execution_attempts, lease_expires_at, channel_id)
		VALUES (?, ?, ?, 'succeeded', ?, '', 'general', '1024x1024', 1, '', '{}', '', '', ?,
		 '', 1, ?, 100, ?, 'text_to_image', 'auto', '', '', '', '', '', '', 0, 0, 0, '', 0, NULL, ?)`,
		now, now, taskNo, prompt, now, userID, db.Bool(deleted), env.channelID(env.t))
	if err != nil {
		env.t.Fatalf("seed task: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

// channelID returns the single image channel the seeded task rows reference.
func (env *testEnv) channelID(t *testing.T) int64 {
	t.Helper()
	if env.channel != 0 {
		return env.channel
	}
	now := db.FormatTime(time.Now())
	result, err := env.db.Exec(`INSERT INTO generator_imagechannel
		(created_at, updated_at, name, channel_code, base_url, api_path, api_key_encrypted, model_name,
		 timeout_seconds, max_retries, is_active, priority, channel_type, provider_name, edit_api_path,
		 provider_vendor)
		VALUES (?, ?, '默认渠道', 'default', 'https://api.openai.com/v1', '/images/generations', '', 'gpt-image-1',
		 60, 2, 1, 1, 'image', 'OpenAI-Compatible', '', 'openai')`, now, now)
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	id, _ := result.LastInsertId()
	env.channel = id
	return id
}

func (env *testEnv) seedImage(userID, taskID int64, imageIndex int, taskNo, prompt, mimeType, objectKey string) int64 {
	env.t.Helper()
	now := time.Now()
	image := &Image{
		TaskID:     taskID,
		UserID:     userID,
		TaskNo:     taskNo,
		Prompt:     prompt,
		Status:     StatusAvailable,
		ImageIndex: imageIndex,
		Width:      128,
		Height:     128,
		FileSize:   16,
		MimeType:   mimeType,
		ObjectKey:  objectKey,
		BucketName: BucketName,
		SHA256:     "abc123",
		ExpiresAt:  now.Add(24 * time.Hour),
	}
	id, err := env.service.insertImage(context.Background(), env.db, image)
	if err != nil {
		env.t.Fatalf("seed image: %v", err)
	}
	return id
}

func (env *testEnv) do(method, target string, body []byte, userID int64, contentType string) *http.Response {
	env.t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequest(method, env.server.URL+target, reader)
	if err != nil {
		env.t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if userID > 0 {
		request.AddCookie(&http.Cookie{Name: httpapi.SessionCookieName, Value: env.sessionKey(userID)})
	}
	request.AddCookie(&http.Cookie{Name: httpapi.CSRFCookieName, Value: csrfToken})
	request.Header.Set("X-CSRFToken", csrfToken)
	response, err := env.client.Do(request)
	if err != nil {
		env.t.Fatalf("request %s %s: %v", method, target, err)
	}
	return response
}

func decodeEnvelope(t *testing.T, response *http.Response) envelope {
	t.Helper()
	defer response.Body.Close()
	payload := envelope{}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode envelope %q: %v", string(body), err)
	}
	return payload
}

func decodeData[T any](t *testing.T, payload envelope) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(payload.Data, &value); err != nil {
		t.Fatalf("decode data %s: %v", string(payload.Data), err)
	}
	return value
}

func pngBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, solidImage(width, height)); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buffer.Bytes()
}

func jpegBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	if err := jpeg.Encode(buffer, solidImage(width, height), nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buffer.Bytes()
}

func solidImage(width, height int) image.Image {
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.Set(x, y, color.RGBA{R: 48, G: 92, B: 210, A: 255})
		}
	}
	return canvas
}

func (env *testEnv) writeObject(key string, content []byte) {
	env.t.Helper()
	target := filepath.Join(env.dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		env.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, content, 0o644); err != nil {
		env.t.Fatalf("write object: %v", err)
	}
}

// ── image library, tenant isolation and downloads ──

func TestOtherUserCannotFetchDownloadURL(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	other := env.user("other")
	taskID := env.seedTask(owner, "GTEST0001", "owner image", false)
	imageID := env.seedImage(owner, taskID, 1, "GTEST0001", "owner image", "image/png",
		"users/1/tasks/GTEST0001/original-1.png")

	response := env.do(http.MethodPost, fmt.Sprintf("/api/images/%d/download-url", imageID), nil, other, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("content type = %q, want text/html", contentType)
	}
}

func TestImageDetailIsScopedToTheOwner(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	other := env.user("other")
	taskID := env.seedTask(owner, "GTEST0001", "owner image", false)
	imageID := env.seedImage(owner, taskID, 1, "GTEST0001", "owner image", "image/png",
		"users/1/tasks/GTEST0001/original-1.png")

	response := env.do(http.MethodGet, fmt.Sprintf("/api/images/%d", imageID), nil, owner, "")
	payload := decodeEnvelope(t, response)
	if response.StatusCode != http.StatusOK || payload.Code != 0 {
		t.Fatalf("status = %d code = %d, want 200/0", response.StatusCode, payload.Code)
	}
	item := decodeData[map[string]any](t, payload)
	if item["task_no"] != "GTEST0001" || item["prompt"] != "owner image" {
		t.Fatalf("unexpected item %+v", item)
	}

	missing := env.do(http.MethodGet, "/api/images/999999", nil, owner, "")
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound || !strings.HasPrefix(missing.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("missing image status = %d content type = %q", missing.StatusCode, missing.Header.Get("Content-Type"))
	}

	foreign := env.do(http.MethodGet, fmt.Sprintf("/api/images/%d", imageID), nil, other, "")
	defer foreign.Body.Close()
	if foreign.StatusCode != http.StatusNotFound {
		t.Fatalf("other user status = %d, want 404", foreign.StatusCode)
	}
}

func TestUnauthenticatedMediaRequestRedirectsToLogin(t *testing.T) {
	env := newTestEnv(t)
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequest(http.MethodGet, env.server.URL+"/api/images?status=available", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", response.StatusCode)
	}
	location := response.Header.Get("Location")
	if location != "/workspace?auth=login&next=/api/images%3Fstatus%3Davailable" {
		t.Fatalf("location = %q", location)
	}
	body, _ := io.ReadAll(response.Body)
	if len(body) != 0 {
		t.Fatalf("redirect body = %q, want empty", string(body))
	}
}

func TestDownloadFilenameUsesPrompt(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	taskID := env.seedTask(owner, "GTEST0001", "owner image", false)
	objectKey := "users/1/tasks/GTEST0001/original-1.png"
	imageID := env.seedImage(owner, taskID, 1, "GTEST0001", "owner image", "image/png", objectKey)
	env.writeObject(objectKey, []byte("fake image bytes"))

	response := env.do(http.MethodPost, fmt.Sprintf("/api/images/%d/download-url", imageID), nil, owner, "")
	payload := decodeEnvelope(t, response)
	urls := decodeData[map[string]string](t, payload)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	download := env.do(http.MethodGet, strings.TrimPrefix(urls["url"], "http://"), nil, owner, "")
	defer download.Body.Close()
	if download.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d", download.StatusCode)
	}
	disposition := download.Header.Get("Content-Disposition")
	if !strings.Contains(disposition, "owner%20image.png") {
		t.Fatalf("content disposition = %q", disposition)
	}
	if !strings.HasPrefix(disposition, `attachment; filename="generated-image.png"`) {
		t.Fatalf("content disposition = %q", disposition)
	}
	if cacheControl := download.Header.Get("Cache-Control"); cacheControl != "private, max-age=300" {
		t.Fatalf("cache control = %q", cacheControl)
	}
	if !strings.Contains(urls["preview_url"], "preview=1") ||
		!strings.Contains(urls["preview_url"], "disposition=inline") {
		t.Fatalf("preview url = %q", urls["preview_url"])
	}
}

func TestDownloadURLPreviewReturnsResizedInlineImage(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	taskID := env.seedTask(owner, "GTEST0001", "owner image", false)
	objectKey := "users/1/tasks/GTEST0001/original-1.png"
	imageID := env.seedImage(owner, taskID, 1, "GTEST0001", "owner image", "image/png", objectKey)
	env.writeObject(objectKey, pngBytes(t, 2048, 1024))

	response := env.do(http.MethodPost, fmt.Sprintf("/api/images/%d/download-url", imageID), nil, owner, "")
	urls := decodeData[map[string]string](t, decodeEnvelope(t, response))
	preview := env.do(http.MethodGet, strings.TrimPrefix(urls["preview_url"], "http://"), nil, owner, "")
	defer preview.Body.Close()
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("preview status = %d", preview.StatusCode)
	}
	if contentType := preview.Header.Get("Content-Type"); contentType != "image/jpeg" {
		t.Fatalf("preview content type = %q", contentType)
	}
	body, _ := io.ReadAll(preview.Body)
	config, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if config.Width > 768 || config.Height > 768 {
		t.Fatalf("preview size = %dx%d, want within 768", config.Width, config.Height)
	}
	if config.Width != 768 || config.Height != 384 {
		t.Fatalf("preview size = %dx%d, want 768x384", config.Width, config.Height)
	}
}

func TestDownloadURLPreviewUsesConfiguredMaxDimension(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) { cfg.ImagePreviewMaxDimension = 320 })
	owner := env.user("owner")
	taskID := env.seedTask(owner, "GTEST0001", "owner image", false)
	objectKey := "users/1/tasks/GTEST0001/original-1.png"
	imageID := env.seedImage(owner, taskID, 1, "GTEST0001", "owner image", "image/png", objectKey)
	env.writeObject(objectKey, pngBytes(t, 2048, 1024))

	response := env.do(http.MethodPost, fmt.Sprintf("/api/images/%d/download-url", imageID), nil, owner, "")
	urls := decodeData[map[string]string](t, decodeEnvelope(t, response))
	preview := env.do(http.MethodGet, strings.TrimPrefix(urls["preview_url"], "http://"), nil, owner, "")
	defer preview.Body.Close()
	body, _ := io.ReadAll(preview.Body)
	config, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if config.Width > 320 || config.Height > 320 {
		t.Fatalf("preview size = %dx%d, want within 320", config.Width, config.Height)
	}
}

func TestPreviewKeepsTransparencyAsPNG(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	taskID := env.seedTask(owner, "GTEST0001", "alpha", false)
	canvas := image.NewNRGBA(image.Rect(0, 0, 1600, 900))
	for y := 0; y < 900; y++ {
		for x := 0; x < 1600; x++ {
			canvas.SetNRGBA(x, y, color.NRGBA{R: 10, G: 200, B: 40, A: uint8(x % 255)})
		}
	}
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, canvas); err != nil {
		t.Fatalf("encode: %v", err)
	}
	objectKey := "users/1/tasks/GTEST0001/original-1.png"
	imageID := env.seedImage(owner, taskID, 1, "GTEST0001", "alpha", "image/png", objectKey)
	record, err := env.service.imageByID(context.Background(), owner, imageID)
	if err != nil {
		t.Fatalf("load image: %v", err)
	}
	previewKey := env.service.buildAndStorePreview(owner, "GTEST0001", 1, buffer.Bytes())
	if previewKey == "" || !strings.HasSuffix(previewKey, ".png") {
		t.Fatalf("preview key = %q, want a png object", previewKey)
	}
	record.PreviewObjectKey = previewKey
	content, contentType, _, _, err := env.service.BuildDownloadResponse(context.Background(), record, true, true)
	if err != nil {
		t.Fatalf("build preview: %v", err)
	}
	if contentType != "image/png" {
		t.Fatalf("content type = %q", contentType)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if config.Width != 768 || config.Height != 432 {
		t.Fatalf("preview size = %dx%d, want 768x432", config.Width, config.Height)
	}
}

func TestPreviewIsRebuiltForRowsWithoutOne(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	taskID := env.seedTask(owner, "GTEST0001", "owner image", false)
	objectKey := "users/1/tasks/GTEST0001/original-1.png"
	imageID := env.seedImage(owner, taskID, 1, "GTEST0001", "owner image", "image/png", objectKey)
	env.writeObject(objectKey, pngBytes(t, 1024, 512))
	record, err := env.service.imageByID(context.Background(), owner, imageID)
	if err != nil {
		t.Fatalf("load image: %v", err)
	}
	if record.PreviewObjectKey != "" {
		t.Fatalf("expected no stored preview, got %q", record.PreviewObjectKey)
	}
	content, contentType, _, _, err := env.service.BuildDownloadResponse(context.Background(), record, true, true)
	if err != nil {
		t.Fatalf("build preview: %v", err)
	}
	if contentType != "image/jpeg" {
		t.Fatalf("content type = %q", contentType)
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if config.Width != 768 || config.Height != 384 {
		t.Fatalf("preview size = %dx%d, want 768x384", config.Width, config.Height)
	}
	reloaded, err := env.service.imageByID(context.Background(), owner, imageID)
	if err != nil {
		t.Fatalf("reload image: %v", err)
	}
	if reloaded.PreviewObjectKey == "" {
		t.Fatal("expected the lazy preview to be recorded")
	}
}

func TestDownloadTokenValidityAndExpiry(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	taskID := env.seedTask(owner, "GTEST0001", "owner image", false)
	objectKey := "users/1/tasks/GTEST0001/original-1.png"
	imageID := env.seedImage(owner, taskID, 1, "GTEST0001", "owner image", "image/png", objectKey)
	env.writeObject(objectKey, pngBytes(t, 64, 64))
	record, err := env.service.imageByID(context.Background(), owner, imageID)
	if err != nil {
		t.Fatalf("load image: %v", err)
	}

	plain, err := env.service.IssueDownloadURL(context.Background(), record, owner, false, false)
	if err != nil {
		t.Fatalf("issue url: %v", err)
	}
	token := tokenFromURL(t, plain)
	resolved, err := env.service.ResolveDownloadToken(context.Background(), token, downloadTokenMaxAge)
	if err != nil || resolved == nil {
		t.Fatalf("resolve plain token: %v", err)
	}
	if _, err := env.service.ResolveDownloadToken(context.Background(), token, time.Nanosecond); err == nil {
		t.Fatal("expected the timestamped token to expire")
	}

	preview, err := env.service.IssueDownloadURL(context.Background(), record, owner, true, true)
	if err != nil {
		t.Fatalf("issue preview url: %v", err)
	}
	previewToken := tokenFromURL(t, preview)
	resolvedPreview, err := env.service.ResolveDownloadToken(context.Background(), previewToken, time.Nanosecond)
	if err != nil || resolvedPreview == nil {
		t.Fatalf("preview token must not expire: %v", err)
	}
	if previewToken != tokenFromURL(t, mustURL(t, env, record, owner)) {
		t.Fatal("preview token must be stable across renders")
	}

	response := env.do(http.MethodGet, "/downloads/not-a-token/", nil, owner, "")
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("bad token status = %d, want 404", response.StatusCode)
	}
}

func mustURL(t *testing.T, env *testEnv, image *Image, userID int64) string {
	t.Helper()
	url, err := env.service.IssueDownloadURL(context.Background(), image, userID, true, true)
	if err != nil {
		t.Fatalf("issue url: %v", err)
	}
	return url
}

func tokenFromURL(t *testing.T, url string) string {
	t.Helper()
	rest := strings.TrimPrefix(url, "/downloads/")
	if index := strings.Index(rest, "?"); index >= 0 {
		rest = rest[:index]
	}
	rest = strings.TrimSuffix(rest, "/")
	unescaped, err := urlUnescape(rest)
	if err != nil {
		t.Fatalf("unescape token: %v", err)
	}
	return unescaped
}

func urlUnescape(value string) (string, error) {
	builder := &strings.Builder{}
	for index := 0; index < len(value); index++ {
		if value[index] != '%' || index+2 >= len(value) {
			builder.WriteByte(value[index])
			continue
		}
		decoded, err := strconv.ParseUint(value[index+1:index+3], 16, 8)
		if err != nil {
			return "", err
		}
		builder.WriteByte(byte(decoded))
		index += 2
	}
	return builder.String(), nil
}

func TestDownloadWithoutTrailingSlashIsRedirected(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	taskID := env.seedTask(owner, "GTEST0001", "owner image", false)
	objectKey := "users/1/tasks/GTEST0001/original-1.png"
	imageID := env.seedImage(owner, taskID, 1, "GTEST0001", "owner image", "image/png", objectKey)
	env.writeObject(objectKey, pngBytes(t, 32, 32))
	record, _ := env.service.imageByID(context.Background(), owner, imageID)
	url, err := env.service.IssueDownloadURL(context.Background(), record, owner, false, false)
	if err != nil {
		t.Fatalf("issue url: %v", err)
	}

	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequest(http.MethodGet, env.server.URL+strings.TrimSuffix(url, "/"), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusMovedPermanently, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
	default:
		t.Fatalf("status = %d, want a redirect to the slash form", response.StatusCode)
	}
}

func TestImageListFiltersAndBareArray(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	taskA := env.seedTask(owner, "GTEST0001", "一只橘猫在咖啡馆看报纸", false)
	taskB := env.seedTask(owner, "GTEST0002", "赛博朋克城市夜景", false)
	deletedTask := env.seedTask(owner, "GTEST0003", "已删除的任务", true)
	env.seedImage(owner, taskA, 1, "GTEST0001", "一只橘猫在咖啡馆看报纸", "image/png", "users/1/a.png")
	secondID := env.seedImage(owner, taskB, 1, "GTEST0002", "赛博朋克城市夜景", "image/jpeg", "users/1/b.jpg")
	env.seedImage(owner, deletedTask, 1, "GTEST0003", "已删除的任务", "image/png", "users/1/c.png")
	if _, err := env.db.Exec(`UPDATE mediahub_generatedimage SET status = ? WHERE id = ?`, StatusExpired, secondID); err != nil {
		t.Fatalf("update status: %v", err)
	}

	all := env.do(http.MethodGet, "/api/images", nil, owner, "")
	allPayload := decodeEnvelope(t, all)
	items := decodeData[[]map[string]any](t, allPayload)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (soft-deleted tasks are filtered)", len(items))
	}
	if items[0]["id"].(float64) != float64(secondID) {
		t.Fatalf("list must be newest first, got %+v", items)
	}
	first := items[1]
	if first["prompt"] != "一只橘猫在咖啡馆看报纸" || first["file_size"].(float64) != 16 {
		t.Fatalf("unexpected item %+v", first)
	}
	if _, ok := first["preview_url"]; ok {
		t.Fatal("image list items must not carry urls")
	}

	filtered := decodeData[[]map[string]any](t, decodeEnvelope(t,
		env.do(http.MethodGet, "/api/images?status=available", nil, owner, "")))
	if len(filtered) != 1 {
		t.Fatalf("status filter items = %d, want 1", len(filtered))
	}
	keyword := decodeData[[]map[string]any](t, decodeEnvelope(t,
		env.do(http.MethodGet, "/api/images?keyword=橘猫", nil, owner, "")))
	if len(keyword) != 1 || keyword[0]["task_no"] != "GTEST0001" {
		t.Fatalf("keyword filter = %+v", keyword)
	}
	empty := decodeData[[]map[string]any](t, decodeEnvelope(t,
		env.do(http.MethodGet, "/api/images?keyword=不存在", nil, owner, "")))
	if len(empty) != 0 {
		t.Fatalf("keyword filter = %+v, want empty", empty)
	}
}

func TestImageListIsCappedAtOneHundred(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	taskID := env.seedTask(owner, "GTEST0001", "capped", false)
	for index := 0; index < 105; index++ {
		env.seedImage(owner, taskID, index+1, "GTEST0001", "capped", "image/png",
			fmt.Sprintf("users/1/tasks/GTEST0001/original-%d.png", index+1))
	}
	response := env.do(http.MethodGet, "/api/images", nil, owner, "")
	items := decodeData[[]map[string]any](t, decodeEnvelope(t, response))
	if len(items) != 100 {
		t.Fatalf("items = %d, want 100", len(items))
	}
}

func TestDownloadURLRejectsUnavailableImage(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	taskID := env.seedTask(owner, "GTEST0001", "owner image", false)
	imageID := env.seedImage(owner, taskID, 1, "GTEST0001", "owner image", "image/png", "users/1/a.png")
	if _, err := env.db.Exec(`UPDATE mediahub_generatedimage SET status = ? WHERE id = ?`, StatusDeleted, imageID); err != nil {
		t.Fatalf("update: %v", err)
	}
	response := env.do(http.MethodPost, fmt.Sprintf("/api/images/%d/download-url", imageID), nil, owner, "")
	payload := decodeEnvelope(t, response)
	if response.StatusCode != http.StatusNotFound || payload.Code != 400404 {
		t.Fatalf("status = %d code = %d", response.StatusCode, payload.Code)
	}
	if payload.Message != "图片已过期或不可下载" {
		t.Fatalf("message = %q", payload.Message)
	}
}

func TestHistoryPages(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	taskID := env.seedTask(owner, "GTEST0001", "owner image", false)
	env.seedImage(owner, taskID, 1, "GTEST0001", "owner image", "image/png", "users/1/a.png")

	images, err := env.service.HistoryImages(context.Background(), owner, 50)
	if err != nil {
		t.Fatalf("history images: %v", err)
	}
	if len(images) != 1 || images[0]["task_no"] != "GTEST0001" {
		t.Fatalf("history images = %+v", images)
	}
	if images[0]["status_display"] != "可用" {
		t.Fatalf("status display = %v", images[0]["status_display"])
	}

	task, detailImages, found, err := env.service.HistoryDetail(context.Background(), owner, "GTEST0001")
	if err != nil || !found {
		t.Fatalf("history detail: found=%v err=%v", found, err)
	}
	if task["task_no"] != "GTEST0001" || len(detailImages) != 1 {
		t.Fatalf("history detail = %+v %+v", task, detailImages)
	}

	if _, _, found, err := env.service.HistoryDetail(context.Background(), owner, "NOSUCHTASK"); err != nil || found {
		t.Fatalf("missing task: found=%v err=%v", found, err)
	}
}

// ── resumable uploads ──

func (env *testEnv) createSession(kind, filename, contentType string, totalSize any, clientUploadID string, userID int64) envelope {
	env.t.Helper()
	payload, err := json.Marshal(map[string]any{
		"kind":             kind,
		"filename":         filename,
		"content_type":     contentType,
		"total_size":       totalSize,
		"client_upload_id": clientUploadID,
	})
	if err != nil {
		env.t.Fatalf("marshal: %v", err)
	}
	response := env.do(http.MethodPost, "/api/uploads/sessions", payload, userID, "application/json")
	return decodeEnvelope(env.t, response)
}

func (env *testEnv) uploadAllChunks(session map[string]any, content []byte, userID int64) *http.Response {
	env.t.Helper()
	totalChunks := int(session["total_chunks"].(float64))
	chunkSize := int(session["chunk_size"].(float64))
	uploadID := session["upload_id"].(string)
	for index := 0; index < totalChunks; index++ {
		start := index * chunkSize
		end := start + chunkSize
		if end > len(content) {
			end = len(content)
		}
		response := env.do(http.MethodPut,
			fmt.Sprintf("/api/uploads/sessions/%s/chunks/%d", uploadID, index),
			content[start:end], userID, "application/octet-stream")
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			env.t.Fatalf("chunk %d status = %d", index, response.StatusCode)
		}
	}
	return env.do(http.MethodPost, fmt.Sprintf("/api/uploads/sessions/%s/complete", uploadID), nil, userID, "")
}

func TestCreateSessionAndResumeByClientUploadID(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	content := pngBytes(t, 16, 16)

	first := env.createSession(KindGenerationReferenceImage, "ref.png", "image/png", len(content), "same-file", owner)
	if first.Code != 0 {
		t.Fatalf("first session: %+v", first)
	}
	firstSession := decodeData[map[string]any](t, first)
	second := env.createSession(KindGenerationReferenceImage, "ref.png", "image/png", len(content), "same-file", owner)
	secondSession := decodeData[map[string]any](t, second)

	if firstSession["upload_id"] != secondSession["upload_id"] {
		t.Fatalf("resume must return the same session: %v vs %v", firstSession["upload_id"], secondSession["upload_id"])
	}
	if secondSession["chunk_size"].(float64) != 8 {
		t.Fatalf("chunk size = %v, want 8", secondSession["chunk_size"])
	}
	totalChunks := int(secondSession["total_chunks"].(float64))
	if want := (len(content) + 7) / 8; totalChunks != want {
		t.Fatalf("total chunks = %d, want %d", totalChunks, want)
	}
	chunks, ok := secondSession["uploaded_chunks"].([]any)
	if !ok || len(chunks) != 0 {
		t.Fatalf("uploaded chunks = %v", secondSession["uploaded_chunks"])
	}
	if _, ok := secondSession["result"].(map[string]any); !ok {
		t.Fatalf("result = %v, want an object", secondSession["result"])
	}
}

func TestChunkUploadIsIdempotentAndCompleteReferenceReturnsToken(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	content := pngBytes(t, 16, 16)
	session := decodeData[map[string]any](t,
		env.createSession(KindGenerationReferenceImage, "ref.png", "image/png", len(content), "client-1", owner))
	uploadID := session["upload_id"].(string)
	chunk := content[:8]

	for attempt := 0; attempt < 2; attempt++ {
		response := env.do(http.MethodPut, fmt.Sprintf("/api/uploads/sessions/%s/chunks/0", uploadID),
			chunk, owner, "application/octet-stream")
		payload := decodeEnvelope(t, response)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("chunk attempt %d status = %d", attempt, response.StatusCode)
		}
		data := decodeData[map[string]any](t, payload)
		chunks := data["uploaded_chunks"].([]any)
		if len(chunks) != 1 || chunks[0].(float64) != 0 {
			t.Fatalf("uploaded chunks = %v", chunks)
		}
	}

	complete := env.uploadAllChunks(session, content, owner)
	payload := decodeEnvelope(t, complete)
	if complete.StatusCode != http.StatusOK {
		t.Fatalf("complete status = %d", complete.StatusCode)
	}
	data := decodeData[map[string]any](t, payload)
	result := data["result"].(map[string]any)
	if result["reference_image_name"] != "ref.png" {
		t.Fatalf("result = %+v", result)
	}
	token, _ := result["reference_upload_token"].(string)
	if token == "" {
		t.Fatalf("result = %+v", result)
	}
	if data["status"] != UploadStatusCompleted {
		t.Fatalf("status = %v", data["status"])
	}

	// The token is signed with the generator's salt and carries the storage meta.
	signer := crypt.NewTimestampSigner(env.cfg.SecretKey, "generator.reference-upload")
	unsigned, err := signer.Unsign(token, ReferenceUploadTokenMaxAge)
	if err != nil {
		t.Fatalf("resolve token: %v", err)
	}
	payloadText := unsigned[:strings.LastIndex(unsigned, ":")]
	resolved := map[string]any{}
	if err := json.Unmarshal([]byte(payloadText), &resolved); err != nil {
		t.Fatalf("decode token payload %q: %v", payloadText, err)
	}
	if resolved["user_id"].(float64) != float64(owner) {
		t.Fatalf("token user = %v", resolved["user_id"])
	}
	if resolved["reference_image_name"] != "ref.png" {
		t.Fatalf("token name = %v", resolved["reference_image_name"])
	}
	objectKey, _ := resolved["reference_object_key"].(string)
	if !strings.HasPrefix(objectKey, fmt.Sprintf("users/%d/tasks/R%d", owner, owner)) ||
		!strings.HasSuffix(objectKey, "/reference.png") {
		t.Fatalf("object key = %q", objectKey)
	}
	if _, err := os.Stat(filepath.Join(env.dir, filepath.FromSlash(objectKey))); err != nil {
		t.Fatalf("reference object missing: %v", err)
	}
	if resolved["reference_image_width"].(float64) != 16 || resolved["reference_image_height"].(float64) != 16 {
		t.Fatalf("token dimensions = %v x %v", resolved["reference_image_width"], resolved["reference_image_height"])
	}
	if !strings.Contains(payloadText, `"reference_image_size":`) {
		t.Fatalf("token payload must carry numeric size: %s", payloadText)
	}

	// result_json keeps python's separators and key order.
	var stored string
	if err := env.db.QueryRow(`SELECT result_json FROM mediahub_uploadsession WHERE upload_id = ?`, uploadID).Scan(&stored); err != nil {
		t.Fatalf("read result_json: %v", err)
	}
	if !strings.HasPrefix(stored, `{"reference_upload_token": "`) ||
		!strings.HasSuffix(stored, `, "reference_image_name": "ref.png", "reference_image_attached": true}`) {
		t.Fatalf("result_json = %s", stored)
	}
}

func TestCompleteWithMissingChunkFails(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	content := pngBytes(t, 16, 16)
	session := decodeData[map[string]any](t,
		env.createSession(KindGenerationReferenceImage, "ref.png", "image/png", len(content), "client-1", owner))

	response := env.do(http.MethodPost,
		fmt.Sprintf("/api/uploads/sessions/%s/complete", session["upload_id"].(string)), nil, owner, "")
	payload := decodeEnvelope(t, response)
	if response.StatusCode != http.StatusBadRequest || payload.Code != 400100 {
		t.Fatalf("status = %d code = %d", response.StatusCode, payload.Code)
	}
	if !strings.Contains(payload.Message, "分片不完整") {
		t.Fatalf("message = %q", payload.Message)
	}
	var status string
	if err := env.db.QueryRow(`SELECT status FROM mediahub_uploadsession WHERE upload_id = ?`,
		session["upload_id"]).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != UploadStatusUploading {
		t.Fatalf("status = %q, want uploading", status)
	}
}

func TestOtherUserCannotAccessUploadSession(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	other := env.user("other")
	content := pngBytes(t, 16, 16)
	session := decodeData[map[string]any](t,
		env.createSession(KindGenerationReferenceImage, "ref.png", "image/png", len(content), "client-1", owner))
	uploadID := session["upload_id"].(string)

	for _, probe := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, fmt.Sprintf("/api/uploads/sessions/%s", uploadID)},
		{http.MethodDelete, fmt.Sprintf("/api/uploads/sessions/%s", uploadID)},
		{http.MethodPost, fmt.Sprintf("/api/uploads/sessions/%s/complete", uploadID)},
	} {
		response := env.do(probe.method, probe.path, nil, other, "")
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404", probe.method, probe.path, response.StatusCode)
		}
		if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
			t.Fatalf("%s %s content type = %q", probe.method, probe.path, contentType)
		}
	}
}

func TestOversizedReferenceUploadIsRejected(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) { cfg.MaxReferenceImageBytes = 10 })
	owner := env.user("owner")
	payload := env.createSession(KindGenerationReferenceImage, "ref.png", "image/png", 11, "too-big", owner)
	if payload.Code != 400100 || payload.Message != "上传文件不能超过 0 MB" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestUploadSessionValidationMessages(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")

	cases := []struct {
		name    string
		kind    string
		size    any
		message string
	}{
		{"kind", "unsupported", 10, "不支持的上传类型"},
		{"zero", KindGenerationReferenceImage, 0, "上传文件不能为空"},
		{"text", KindGenerationReferenceImage, "abc", "文件大小不合法"},
		{"missing", KindGenerationReferenceImage, nil, "文件大小不合法"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			payload := env.createSession(testCase.kind, "ref.png", "image/png", testCase.size, "", owner)
			if payload.Code != 400100 || payload.Message != testCase.message {
				t.Fatalf("payload = %+v, want %q", payload, testCase.message)
			}
		})
	}

	chunk := env.do(http.MethodPut, "/api/uploads/sessions/nonexistent/chunks/0", []byte("x"), owner, "application/octet-stream")
	chunk.Body.Close()
	if chunk.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown session chunk status = %d", chunk.StatusCode)
	}
}

func TestUploadSessionChunkLengthAndIndexValidation(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	content := pngBytes(t, 16, 16)
	session := decodeData[map[string]any](t,
		env.createSession(KindGenerationReferenceImage, "ref.png", "image/png", len(content), "client-1", owner))
	uploadID := session["upload_id"].(string)

	short := env.do(http.MethodPut, fmt.Sprintf("/api/uploads/sessions/%s/chunks/0", uploadID),
		content[:4], owner, "application/octet-stream")
	shortPayload := decodeEnvelope(t, short)
	if short.StatusCode != http.StatusBadRequest || shortPayload.Message != "分片大小不匹配" {
		t.Fatalf("short chunk: %d %q", short.StatusCode, shortPayload.Message)
	}
	outOfRange := env.do(http.MethodPut, fmt.Sprintf("/api/uploads/sessions/%s/chunks/999", uploadID),
		content[:8], owner, "application/octet-stream")
	outOfRangePayload := decodeEnvelope(t, outOfRange)
	if outOfRange.StatusCode != http.StatusBadRequest || outOfRangePayload.Message != "分片序号超出范围" {
		t.Fatalf("out of range: %d %q", outOfRange.StatusCode, outOfRangePayload.Message)
	}
	nonNumeric := env.do(http.MethodPut, fmt.Sprintf("/api/uploads/sessions/%s/chunks/abc", uploadID),
		content[:8], owner, "application/octet-stream")
	nonNumeric.Body.Close()
	if nonNumeric.StatusCode != http.StatusNotFound {
		t.Fatalf("non numeric index status = %d, want 404", nonNumeric.StatusCode)
	}
}

func TestCancelUploadSession(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	content := pngBytes(t, 16, 16)
	session := decodeData[map[string]any](t,
		env.createSession(KindNovelEPUB, "novel.epub", "application/epub+zip", len(content), "cancel-1", owner))
	uploadID := session["upload_id"].(string)
	chunk := env.do(http.MethodPut, fmt.Sprintf("/api/uploads/sessions/%s/chunks/0", uploadID),
		content[:8], owner, "application/octet-stream")
	chunk.Body.Close()

	canceled := env.do(http.MethodDelete, fmt.Sprintf("/api/uploads/sessions/%s", uploadID), nil, owner, "")
	payload := decodeEnvelope(t, canceled)
	data := decodeData[map[string]any](t, payload)
	if canceled.StatusCode != http.StatusOK || data["status"] != UploadStatusCanceled {
		t.Fatalf("cancel: %d %+v", canceled.StatusCode, data)
	}
	if _, err := os.Stat(env.service.sessionDir(&UploadSession{UserID: owner, UploadID: uploadID})); !os.IsNotExist(err) {
		t.Fatalf("session directory still present: %v", err)
	}
	complete := env.do(http.MethodPost, fmt.Sprintf("/api/uploads/sessions/%s/complete", uploadID), nil, owner, "")
	completePayload := decodeEnvelope(t, complete)
	if complete.StatusCode != http.StatusBadRequest || completePayload.Message != "上传会话不可完成" {
		t.Fatalf("complete canceled: %d %q", complete.StatusCode, completePayload.Message)
	}
}

func TestExpireUploadSessionsMarksSessionAndRemovesChunks(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	content := pngBytes(t, 16, 16)
	session := decodeData[map[string]any](t,
		env.createSession(KindGenerationReferenceImage, "ref.png", "image/png", len(content), "client-1", owner))
	uploadID := session["upload_id"].(string)
	chunk := env.do(http.MethodPut, fmt.Sprintf("/api/uploads/sessions/%s/chunks/0", uploadID),
		content[:8], owner, "application/octet-stream")
	chunk.Body.Close()

	if _, err := env.db.Exec(`UPDATE mediahub_uploadsession SET expires_at = ? WHERE upload_id = ?`,
		db.FormatTime(time.Now().Add(-time.Second)), uploadID); err != nil {
		t.Fatalf("age session: %v", err)
	}
	expired, err := env.service.ExpireUploadSessions(context.Background(), 100)
	if err != nil || expired != 1 {
		t.Fatalf("expired = %d err = %v, want 1", expired, err)
	}
	var status string
	if err := env.db.QueryRow(`SELECT status FROM mediahub_uploadsession WHERE upload_id = ?`, uploadID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != UploadStatusExpired {
		t.Fatalf("status = %q, want expired", status)
	}
	if _, err := os.Stat(env.service.sessionDir(&UploadSession{UserID: owner, UploadID: uploadID})); !os.IsNotExist(err) {
		t.Fatalf("session directory still present: %v", err)
	}
	again, err := env.service.ExpireUploadSessions(context.Background(), 100)
	if err != nil || again != 0 {
		t.Fatalf("second sweep = %d err = %v, want 0", again, err)
	}
}

func TestGetUploadSessionMarksItExpired(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	content := pngBytes(t, 16, 16)
	session := decodeData[map[string]any](t,
		env.createSession(KindNovelEPUB, "novel.epub", "application/epub+zip", len(content), "late-1", owner))
	uploadID := session["upload_id"].(string)
	if _, err := env.db.Exec(`UPDATE mediahub_uploadsession SET expires_at = ? WHERE upload_id = ?`,
		db.FormatTime(time.Now().Add(-time.Minute)), uploadID); err != nil {
		t.Fatalf("age session: %v", err)
	}
	response := env.do(http.MethodGet, fmt.Sprintf("/api/uploads/sessions/%s", uploadID), nil, owner, "")
	data := decodeData[map[string]any](t, decodeEnvelope(t, response))
	if data["status"] != UploadStatusExpired {
		t.Fatalf("status = %v, want expired", data["status"])
	}
}

func TestResumeCreatesANewSessionWhenTheChunkSizeChanged(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	content := pngBytes(t, 16, 16)
	first := decodeData[map[string]any](t,
		env.createSession(KindGenerationReferenceImage, "ref.png", "image/png", len(content), "size-change", owner))

	env.cfg.ResumableUploadChunkSize = 4
	second := decodeData[map[string]any](t,
		env.createSession(KindGenerationReferenceImage, "ref.png", "image/png", len(content), "size-change", owner))

	if first["upload_id"] == second["upload_id"] {
		t.Fatal("a new chunk size must start a new session")
	}
	var status string
	if err := env.db.QueryRow(`SELECT status FROM mediahub_uploadsession WHERE upload_id = ?`,
		first["upload_id"]).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != UploadStatusCanceled {
		t.Fatalf("previous session status = %q, want canceled", status)
	}
}

func TestEPUBUploadCompletionCreatesBookRow(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	content := []byte("PK\x03\x04fake-epub-content-for-upload")
	session := decodeData[map[string]any](t,
		env.createSession(KindNovelEPUB, "story.epub", "application/epub+zip", len(content), "novel-1", owner))

	complete := env.uploadAllChunks(session, content, owner)
	payload := decodeEnvelope(t, complete)
	if complete.StatusCode != http.StatusOK {
		t.Fatalf("complete status = %d", complete.StatusCode)
	}
	result := decodeData[map[string]any](t, payload)["result"].(map[string]any)
	if result["source"] != "uploaded" || result["filename"] != "story.epub" {
		t.Fatalf("result = %+v", result)
	}
	if result["id"] != "uploaded:1" || result["uploaded_book_id"].(float64) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if result["size"].(float64) != float64(len(content)) || result["created_at"] == "" {
		t.Fatalf("result = %+v", result)
	}

	var storagePath, displayName, sha string
	var fileSize int64
	if err := env.db.QueryRow(`SELECT storage_path, display_name, file_size, sha256
		FROM novelviz_noveluploadedbook WHERE user_id = ?`, owner).Scan(&storagePath, &displayName, &fileSize, &sha); err != nil {
		t.Fatalf("read book: %v", err)
	}
	if displayName != "story.epub" || fileSize != int64(len(content)) || sha != sha256Hex(content) {
		t.Fatalf("book row = %q %q %d %q", storagePath, displayName, fileSize, sha)
	}
	parts := strings.Split(storagePath, "/")
	if len(parts) != 5 || parts[0] != "novelviz" || parts[1] != "uploads" || parts[2] != "users" ||
		parts[3] != strconv.FormatInt(owner, 10) || len(parts[4]) != 32+len("-story.epub") {
		t.Fatalf("storage path = %q", storagePath)
	}
	stored, err := os.ReadFile(filepath.Join(env.dir, filepath.FromSlash(storagePath)))
	if err != nil {
		t.Fatalf("read stored epub: %v", err)
	}
	if !bytes.Equal(stored, content) {
		t.Fatal("stored epub differs from the uploaded bytes")
	}

	// Completing twice is idempotent.
	again := env.do(http.MethodPost,
		fmt.Sprintf("/api/uploads/sessions/%s/complete", session["upload_id"].(string)), nil, owner, "")
	againResult := decodeData[map[string]any](t, decodeEnvelope(t, again))["result"].(map[string]any)
	if againResult["uploaded_book_id"].(float64) != 1 {
		t.Fatalf("second completion created another book: %+v", againResult)
	}
	var books int
	if err := env.db.QueryRow(`SELECT COUNT(1) FROM novelviz_noveluploadedbook`).Scan(&books); err != nil {
		t.Fatalf("count books: %v", err)
	}
	if books != 1 {
		t.Fatalf("books = %d, want 1", books)
	}
}

func TestEPUBUploadRejectsNonEPUBFilename(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	content := []byte("PK\x03\x04not-an-epub")
	session := decodeData[map[string]any](t,
		env.createSession(KindNovelEPUB, "story.txt", "application/octet-stream", len(content), "novel-2", owner))
	complete := env.uploadAllChunks(session, content, owner)
	payload := decodeEnvelope(t, complete)
	if complete.StatusCode != http.StatusBadRequest || payload.Message != "请上传 EPUB 小说文件" {
		t.Fatalf("complete: %d %q", complete.StatusCode, payload.Message)
	}
}

func TestConcurrentCompletionStoresOneBook(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	content := []byte("PK\x03\x04concurrent-epub")
	session := decodeData[map[string]any](t,
		env.createSession(KindNovelEPUB, "story.epub", "application/epub+zip", len(content), "race-1", owner))
	uploadID := session["upload_id"].(string)
	totalChunks := int(session["total_chunks"].(float64))
	chunkSize := int(session["chunk_size"].(float64))
	for index := 0; index < totalChunks; index++ {
		start := index * chunkSize
		end := start + chunkSize
		if end > len(content) {
			end = len(content)
		}
		if _, err := env.service.StoreUploadChunk(context.Background(), owner, uploadID, index, content[start:end]); err != nil {
			t.Fatalf("chunk %d: %v", index, err)
		}
	}

	var wait sync.WaitGroup
	results := make([]map[string]any, 2)
	errs := make([]error, 2)
	for worker := 0; worker < 2; worker++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errs[index] = env.service.CompleteUploadSession(context.Background(), owner, uploadID)
		}(worker)
	}
	wait.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("worker %d failed: %v", index, err)
		}
	}
	first, err := json.Marshal(results[0]["result"])
	if err != nil {
		t.Fatalf("marshal first result: %v", err)
	}
	second, err := json.Marshal(results[1]["result"])
	if err != nil {
		t.Fatalf("marshal second result: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("completions disagree: %s vs %s", first, second)
	}
	var books int
	if err := env.db.QueryRow(`SELECT COUNT(1) FROM novelviz_noveluploadedbook`).Scan(&books); err != nil {
		t.Fatalf("count books: %v", err)
	}
	if books != 1 {
		t.Fatalf("books = %d, want exactly 1", books)
	}
	entries, err := os.ReadDir(filepath.Join(env.dir, "novelviz", "uploads", "users", strconv.FormatInt(owner, 10)))
	if err != nil {
		t.Fatalf("read upload dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("stored files = %d, want exactly 1", len(entries))
	}
}

// ── retention and storage cleanup ──

func TestPurgeDeletedImageFilesRemovesStorageAfterRetention(t *testing.T) {
	env := newTestEnv(t, func(cfg *config.Config) { cfg.SoftDeleteRetentionDays = 7 })
	owner := env.user("owner")
	taskID := env.seedTask(owner, "GTEST0001", "owner image", false)
	oldKey := "users/1/tasks/GTEST0001/original-old.png"
	freshKey := "users/1/tasks/GTEST0001/original-fresh.png"
	env.writeObject(oldKey, []byte("old"))
	env.writeObject(freshKey, []byte("fresh"))

	oldID := env.seedImage(owner, taskID, 1, "GTEST0001", "old", "image/png", oldKey)
	freshID := env.seedImage(owner, taskID, 2, "GTEST0001", "fresh", "image/png", freshKey)
	if _, err := env.db.Exec(`UPDATE mediahub_generatedimage
		SET status = ?, deleted_at = ?, file_size = 3 WHERE id = ?`,
		StatusDeleted, db.FormatTime(time.Now().AddDate(0, 0, -8)), oldID); err != nil {
		t.Fatalf("age image: %v", err)
	}
	if _, err := env.db.Exec(`UPDATE mediahub_generatedimage
		SET status = ?, deleted_at = ? WHERE id = ?`,
		StatusDeleted, db.FormatTime(time.Now().AddDate(0, 0, -1)), freshID); err != nil {
		t.Fatalf("age image: %v", err)
	}

	purged, err := env.service.CleanupStorage(context.Background(), 10)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if purged["deleted_image_files"] != 1 {
		t.Fatalf("counts = %+v", purged)
	}
	if _, err := os.Stat(filepath.Join(env.dir, filepath.FromSlash(oldKey))); !os.IsNotExist(err) {
		t.Fatal("expired object must be removed")
	}
	if _, err := os.Stat(filepath.Join(env.dir, filepath.FromSlash(freshKey))); err != nil {
		t.Fatal("fresh object must stay")
	}
	var objectKey string
	var fileSize int64
	if err := env.db.QueryRow(`SELECT object_key, file_size FROM mediahub_generatedimage WHERE id = ?`, oldID).
		Scan(&objectKey, &fileSize); err != nil {
		t.Fatalf("read purged row: %v", err)
	}
	if objectKey != "" || fileSize != 0 {
		t.Fatalf("purged row = %q %d", objectKey, fileSize)
	}
	if err := env.db.QueryRow(`SELECT object_key FROM mediahub_generatedimage WHERE id = ?`, freshID).Scan(&objectKey); err != nil {
		t.Fatalf("read fresh row: %v", err)
	}
	if objectKey != freshKey {
		t.Fatalf("fresh row = %q", objectKey)
	}
}

func TestCleanupStorageReturnsCategoryCounts(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	taskID := env.seedTask(owner, "GTEST0001", "owner image", false)
	objectKey := "users/1/tasks/GTEST0001/original-1.png"
	imageID := env.seedImage(owner, taskID, 1, "GTEST0001", "owner image", "image/png", objectKey)
	env.writeObject(objectKey, pngBytes(t, 32, 32))
	if _, err := env.db.Exec(`UPDATE mediahub_generatedimage SET expires_at = ? WHERE id = ?`,
		db.FormatTime(time.Now().Add(-time.Second)), imageID); err != nil {
		t.Fatalf("age image: %v", err)
	}

	temporary := filepath.Join(env.dir, "users", strconv.FormatInt(owner, 10), "tasks", "R876543", "reference.png")
	if err := os.MkdirAll(filepath.Dir(temporary), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(temporary, []byte("temporary reference"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	expired := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(temporary, expired, expired); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	counts, err := env.service.CleanupStorage(context.Background(), 10)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if counts["expired_images"] != 1 || counts["temporary_reference_images"] != 1 {
		t.Fatalf("counts = %+v", counts)
	}
	for _, key := range []string{"expired_images", "temporary_reference_images", "upload_sessions", "deleted_image_files"} {
		if _, ok := counts[key]; !ok {
			t.Fatalf("counts missing %q: %+v", key, counts)
		}
	}
	var status string
	if err := env.db.QueryRow(`SELECT status FROM mediahub_generatedimage WHERE id = ?`, imageID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != StatusExpired {
		t.Fatalf("status = %q, want expired", status)
	}
	if _, err := os.Stat(filepath.Join(env.dir, filepath.FromSlash(objectKey))); !os.IsNotExist(err) {
		t.Fatal("expired object must be removed")
	}
	second, err := env.service.CleanupStorage(context.Background(), 10)
	if err != nil || second["expired_images"] != 0 {
		t.Fatalf("second pass = %+v err = %v", second, err)
	}
}

func TestExpireTemporaryReferenceImagesOnlyRemovesExpiredUploads(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	base := filepath.Join(env.dir, "users", strconv.FormatInt(owner, 10), "tasks")
	expiredPath := filepath.Join(base, "R123456", "reference.png")
	freshPath := filepath.Join(base, "R123457", "reference.png")
	permanentPath := filepath.Join(base, "GTEST0001", "reference.png")
	for _, path := range []string{expiredPath, freshPath, permanentPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("reference"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(expiredPath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	deleted, err := env.service.expireTemporaryReferenceImages(10)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted = %d err = %v, want 1", deleted, err)
	}
	if _, err := os.Stat(expiredPath); !os.IsNotExist(err) {
		t.Fatal("expired temporary reference must be removed")
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatal("fresh temporary reference must stay")
	}
	if _, err := os.Stat(permanentPath); err != nil {
		t.Fatal("task reference must stay")
	}
}

func TestStoreGeneratedImageLayout(t *testing.T) {
	env := newTestEnv(t)
	owner := env.user("owner")
	taskID := env.seedTask(owner, "G20260101000000ABCD1234", "一只橘猫", false)
	content := pngBytes(t, 1024, 768)
	record, err := env.service.StoreGeneratedImage(context.Background(), StoreImageInput{
		UserID: owner, TaskID: taskID, TaskNo: "G20260101000000ABCD1234", Index: 1,
		Content: content, MimeType: "image/png", Prompt: "一只橘猫",
	})
	if err != nil {
		t.Fatalf("store image: %v", err)
	}
	wantKey := fmt.Sprintf("users/%d/tasks/G20260101000000ABCD1234/original-1.png", owner)
	if record.ObjectKey != wantKey {
		t.Fatalf("object key = %q, want %q", record.ObjectKey, wantKey)
	}
	wantPreview := fmt.Sprintf("users/%d/tasks/G20260101000000ABCD1234/preview-1.jpg", owner)
	if record.PreviewObjectKey != wantPreview {
		t.Fatalf("preview key = %q, want %q", record.PreviewObjectKey, wantPreview)
	}
	if record.Width != 1024 || record.Height != 768 {
		t.Fatalf("dimensions = %dx%d", record.Width, record.Height)
	}
	if record.FileSize != int64(len(content)) || record.SHA256 != sha256Hex(content) {
		t.Fatalf("size/hash = %d %q", record.FileSize, record.SHA256)
	}
	if record.BucketName != BucketName || record.Status != StatusAvailable {
		t.Fatalf("row = %+v", record)
	}
	stored, err := os.ReadFile(filepath.Join(env.dir, filepath.FromSlash(record.ObjectKey)))
	if err != nil {
		t.Fatalf("read stored object: %v", err)
	}
	storedConfig, _, err := image.DecodeConfig(bytes.NewReader(stored))
	if err != nil || storedConfig.Width != 1024 || storedConfig.Height != 768 {
		t.Fatalf("stored image must keep its dimensions: %+v err = %v", storedConfig, err)
	}

	reference, err := env.service.StoreReferenceImage(context.Background(), owner, "R123456", content, "image/jpeg")
	if err != nil {
		t.Fatalf("store reference: %v", err)
	}
	wantReferenceKey := fmt.Sprintf("users/%d/tasks/R123456/reference.jpg", owner)
	if reference["reference_object_key"] != wantReferenceKey {
		t.Fatalf("reference key = %q", reference["reference_object_key"])
	}
	if reference["reference_storage_provider"] != StorageProviderLocal || reference["reference_bucket_name"] != BucketName {
		t.Fatalf("reference meta = %+v", reference)
	}
	if reference["reference_image_size"] != strconv.Itoa(len(content)) {
		t.Fatalf("reference size = %q", reference["reference_image_size"])
	}
	for _, key := range ReferenceMetaKeys {
		if _, ok := reference[key]; !ok {
			t.Fatalf("reference meta missing %q: %+v", key, reference)
		}
	}
}

// ── remote image fetch ──

func TestFetchRemoteImageBlocksPrivateTargets(t *testing.T) {
	env := newTestEnv(t)
	cases := []string{
		"http://127.0.0.1/private.png",
		"http://10.0.0.1/private.png",
		"http://169.254.169.254/latest/meta-data.png",
		"http://192.168.1.1/private.png",
		"http://172.16.0.1/private.png",
		"http://100.64.0.1/private.png",
		"http://[::1]/private.png",
		"http://[fc00::1]/private.png",
		"http://[fe80::1]/private.png",
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			_, _, err := env.service.FetchRemoteImage(context.Background(), target)
			if err == nil || !strings.Contains(err.Error(), "远程图片地址禁止访问内网主机") {
				t.Fatalf("err = %v, want the private-address rejection", err)
			}
		})
	}
}

func TestFetchRemoteImageRejectsInvalidURLs(t *testing.T) {
	env := newTestEnv(t)
	for _, target := range []string{"ftp://example.com/x.png", "file:///etc/passwd", "notaurl", "http:///x.png"} {
		if _, _, err := env.service.FetchRemoteImage(context.Background(), target); err == nil ||
			!strings.Contains(err.Error(), "远程图片地址不合法") {
			t.Fatalf("%s: err = %v", target, err)
		}
	}
}

func TestIsGlobalIP(t *testing.T) {
	notGlobal := []string{
		"0.0.0.0", "10.1.2.3", "127.0.0.1", "169.254.10.10", "172.20.0.1", "192.168.0.1",
		"100.64.0.1", "100.127.255.255", "192.0.0.1", "192.0.2.5", "198.18.0.1", "198.19.255.255",
		"198.51.100.7", "203.0.113.9", "224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
		"::", "::1", "fc00::1", "fd12:3456::1", "fe80::1", "2001:db8::1", "ff02::1",
		"::ffff:127.0.0.1",
	}
	for _, address := range notGlobal {
		parsed := netParseIP(t, address)
		if isGlobalIP(parsed) {
			t.Fatalf("%s must not be treated as a global address", address)
		}
	}
	global := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "::ffff:8.8.8.8", "2001:4860:4860::8888", "2606:4700::1111"}
	for _, address := range global {
		parsed := netParseIP(t, address)
		if !isGlobalIP(parsed) {
			t.Fatalf("%s must be treated as a global address", address)
		}
	}
}

func TestFetchRemoteImageAllowsConfiguredHostAndSniffsContent(t *testing.T) {
	env := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pngBytes(t, 32, 32))
	}))
	defer upstream.Close()
	env.cfg.RemoteImageAllowedHosts = []string{"127.0.0.1"}

	content, mimeType, err := env.service.FetchRemoteImage(context.Background(), upstream.URL+"/result.png")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if mimeType != "image/png" {
		t.Fatalf("mime = %q, want the sniffed image/png", mimeType)
	}
	if !bytes.Equal(content, pngBytes(t, 32, 32)) {
		t.Fatal("content differs from the served body")
	}
}

func TestFetchRemoteImageRejectsNonImageContent(t *testing.T) {
	env := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not an image</html>"))
	}))
	defer upstream.Close()
	env.cfg.RemoteImageAllowedHosts = []string{"127.0.0.1"}

	if _, _, err := env.service.FetchRemoteImage(context.Background(), upstream.URL+"/x"); err == nil ||
		!strings.Contains(err.Error(), "远程地址未返回图片内容") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchRemoteImageEnforcesTheSizeCap(t *testing.T) {
	env := newTestEnv(t)
	content := pngBytes(t, 32, 32)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(content)
	}))
	defer upstream.Close()
	env.cfg.RemoteImageAllowedHosts = []string{"127.0.0.1"}
	env.cfg.MaxRemoteImageBytes = 8

	if _, _, err := env.service.FetchRemoteImage(context.Background(), upstream.URL+"/x.png"); err == nil ||
		!strings.Contains(err.Error(), "远程图片超过大小限制") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchRemoteImageFollowsRedirectsWithinTheLimit(t *testing.T) {
	env := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(pngBytes(t, 16, 16))
		default:
			http.Redirect(w, r, "/loop", http.StatusFound)
		}
	}))
	defer upstream.Close()
	env.cfg.RemoteImageAllowedHosts = []string{"127.0.0.1"}

	if _, mimeType, err := env.service.FetchRemoteImage(context.Background(), upstream.URL+"/start"); err != nil || mimeType != "image/png" {
		t.Fatalf("redirect: mime = %q err = %v", mimeType, err)
	}
	if _, _, err := env.service.FetchRemoteImage(context.Background(), upstream.URL+"/loop"); err == nil ||
		!strings.Contains(err.Error(), "远程图片重定向次数过多") {
		t.Fatalf("loop err = %v", err)
	}
}

func TestFetchRemoteImageRevalidatesRedirectTargets(t *testing.T) {
	env := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://192.168.0.10/private.png", http.StatusFound)
	}))
	defer upstream.Close()
	env.cfg.RemoteImageAllowedHosts = []string{"127.0.0.1"}

	if _, _, err := env.service.FetchRemoteImage(context.Background(), upstream.URL+"/start"); err == nil ||
		!strings.Contains(err.Error(), "远程图片禁止访问内网主机") &&
			!strings.Contains(err.Error(), "远程图片地址禁止访问内网主机") {
		t.Fatalf("err = %v", err)
	}
}

func netParseIP(t *testing.T, value string) net.IP {
	t.Helper()
	parsed := net.ParseIP(value)
	if parsed == nil {
		t.Fatalf("cannot parse %q", value)
	}
	return parsed
}

// ── JSON and filename helpers ──

func TestPythonJSONDumpMatchesTheStoredContract(t *testing.T) {
	if got := pythonJSONDump([]int{0, 1, 22}); got != "[0, 1, 22]" {
		t.Fatalf("chunks = %s", got)
	}
	if got := pythonJSONDump([]int{}); got != "[]" {
		t.Fatalf("empty chunks = %s", got)
	}
	ordered := jsonObject{{"id", "uploaded:1"}, {"size", 1465}, {"created_at", time.Unix(1791550493, 268693000)}}
	want := `{"id": "uploaded:1", "size": 1465, "created_at": "2026-10-09T12:54:53.268693+00:00"}`
	if got := pythonJSONDump(ordered); got != want {
		t.Fatalf("dump = %s, want %s", got, want)
	}
	compact := jsonObject{{"user_id", int64(3)}, {"reference_image_name", "ref.png"}}
	if got := pythonJSONCompact(compact); got != `{"user_id":3,"reference_image_name":"ref.png"}` {
		t.Fatalf("compact = %s", got)
	}
}

func TestDownloadFilenameSanitisesThePrompt(t *testing.T) {
	env := newTestEnv(t)
	image := &Image{TaskNo: "GTEST0001", MimeType: "image/jpeg", Prompt: `  a/b\c:d*e?f"g<h>i|j   k  `}
	if got := env.service.downloadFilename(image); got != "abcdefghij k.jpg" {
		t.Fatalf("filename = %q", got)
	}
	image.Prompt = "   "
	image.TaskNo = "GTEST0002"
	if got := env.service.downloadFilename(image); got != "GTEST0002.jpg" {
		t.Fatalf("fallback filename = %q", got)
	}
	image.Prompt = strings.Repeat("字", 60)
	image.MimeType = "image/png"
	if got := env.service.downloadFilename(image); len([]rune(got)) != 48+4 {
		t.Fatalf("truncated filename = %q", got)
	}
}

func TestObjectKeysFollowTheReferenceLayout(t *testing.T) {
	if got := objectKey(3, "G20261009ABCD", 2, "image/webp"); got != "users/3/tasks/G20261009ABCD/original-2.webp" {
		t.Fatalf("object key = %q", got)
	}
	if got := objectKey(3, "G20261009ABCD", 1, ""); got != "users/3/tasks/G20261009ABCD/original-1.png" {
		t.Fatalf("object key = %q", got)
	}
	if got := previewObjectKey(3, "G20261009ABCD", 1, "image/png"); got != "users/3/tasks/G20261009ABCD/preview-1.png" {
		t.Fatalf("preview key = %q", got)
	}
	if got := previewObjectKey(3, "G20261009ABCD", 1, "image/jpeg"); got != "users/3/tasks/G20261009ABCD/preview-1.jpg" {
		t.Fatalf("preview key = %q", got)
	}
	if got := safeUploadFilename("../../etc/pa:sswd.png", "reference-image"); got != "pa-sswd.png" {
		t.Fatalf("safe filename = %q", got)
	}
	if got := safeUploadFilename("   ", "novel.epub"); got != "novel.epub" {
		t.Fatalf("fallback filename = %q", got)
	}
}
