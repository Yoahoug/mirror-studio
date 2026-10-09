package console

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mirror-studio/internal/auth"
	"mirror-studio/internal/config"
	"mirror-studio/internal/crypt"
	"mirror-studio/internal/db"
	"mirror-studio/internal/httpapi"
	"mirror-studio/internal/quota"
	"mirror-studio/internal/siteconfig"
)

type fakeUserService struct {
	users map[int64]*auth.User
}

func (f *fakeUserService) ByID(ctx context.Context, id int64) (*auth.User, error) {
	if user, ok := f.users[id]; ok {
		return user, nil
	}
	return nil, auth.ErrUserNotFound
}

// fakeQuotaService mirrors the account semantics the console relies on, without
// the ledger bookkeeping that internal/quota owns and tests.
type fakeQuotaService struct {
	db      *db.DB
	loc     *time.Location
	remarks []string
}

func (f *fakeQuotaService) Ensure(ctx context.Context, userID int64) (*quota.Account, error) {
	account, found, err := f.load(ctx, userID)
	if err != nil {
		return nil, err
	}
	if found {
		return account, nil
	}
	now := db.FormatTime(time.Now().UTC())
	if _, err := f.db.ExecContext(ctx,
		`INSERT INTO billing_quotaaccount
			(created_at, updated_at, daily_quota, available_quota, frozen_quota, total_generated_count, user_id, last_reset_date)
		 VALUES (?, ?, 3, 3, 0, 0, ?, ?)`,
		now, now, userID, f.today(),
	); err != nil {
		return nil, err
	}
	created, _, err := f.load(ctx, userID)
	return created, err
}

func (f *fakeQuotaService) AdminAdjust(ctx context.Context, userID int64, dailyQuota, availableQuota *int, remark string) (*quota.Account, error) {
	f.remarks = append(f.remarks, remark)
	var account *quota.Account
	err := f.db.WithImmediate(ctx, func(tx *db.Tx) error {
		var (
			id                int64
			available, frozen int
			daily, total      int
			lastReset         string
			found             bool
		)
		err := tx.QueryRow(
			`SELECT id, daily_quota, available_quota, frozen_quota, total_generated_count, last_reset_date
			 FROM billing_quotaaccount WHERE user_id = ?`, userID,
		).Scan(&id, &daily, &available, &frozen, &total, &lastReset)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.Exec(
				`INSERT INTO billing_quotaaccount
					(created_at, updated_at, daily_quota, available_quota, frozen_quota, total_generated_count, user_id, last_reset_date)
				 VALUES (?, ?, 3, 3, 0, 0, ?, ?)`,
				db.FormatTime(time.Now().UTC()), db.FormatTime(time.Now().UTC()), userID, f.today(),
			); err != nil {
				return err
			}
			daily, available, frozen, total = 3, 3, 0, 0
			lastReset = f.today()
			found = true
		} else if err != nil {
			return err
		} else {
			found = true
		}
		if !found {
			return errors.New("quota account missing")
		}
		if dailyQuota != nil {
			if *dailyQuota < 0 {
				return errors.New("每日免费次数不能小于 0")
			}
			daily = *dailyQuota
		}
		if availableQuota != nil {
			if *availableQuota < 0 {
				return errors.New("当前剩余次数不能小于 0")
			}
			available = *availableQuota
		}
		if _, err := tx.Exec(
			`UPDATE billing_quotaaccount SET daily_quota = ?, available_quota = ?, updated_at = ? WHERE user_id = ?`,
			daily, available, db.FormatTime(time.Now().UTC()), userID,
		); err != nil {
			return err
		}
		account = &quota.Account{
			ID: id, UserID: userID, DailyQuota: daily, AvailableQuota: available,
			FrozenQuota: frozen, TotalGeneratedCount: total, LastResetDate: lastReset,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

func (f *fakeQuotaService) today() string {
	if f.loc == nil {
		return time.Now().Format("2006-01-02")
	}
	return time.Now().In(f.loc).Format("2006-01-02")
}

func (f *fakeQuotaService) load(ctx context.Context, userID int64) (*quota.Account, bool, error) {
	var (
		account           quota.Account
		available, frozen int
		daily, total      int
	)
	err := f.db.QueryRowContext(ctx,
		`SELECT id, daily_quota, available_quota, frozen_quota, total_generated_count, last_reset_date
		 FROM billing_quotaaccount WHERE user_id = ?`, userID,
	).Scan(&account.ID, &daily, &available, &frozen, &total, &account.LastResetDate)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	account.UserID = userID
	account.DailyQuota = daily
	account.AvailableQuota = available
	account.FrozenQuota = frozen
	account.TotalGeneratedCount = total
	return &account, true, nil
}

type harness struct {
	t          *testing.T
	db         *db.DB
	site       *siteconfig.Service
	cipher     *crypt.FieldCipher
	auth       *fakeUserService
	quota      *fakeQuotaService
	csrf       string
	sessionKey string
	middleware *httpapi.Middleware
	sessions   *httpapi.SessionStore
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "console.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cipher, err := crypt.NewFieldCipher("console-test-field-crypt-key-0001")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	site := siteconfig.New(handle, cipher, logger)
	cfg := &config.Config{
		Debug:                 true,
		TimeZone:              "Asia/Shanghai",
		ConsoleAPIRateLimit:   60,
		DefaultDailyFreeQuota: 3,
	}
	location, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	authService := &fakeUserService{users: map[int64]*auth.User{}}
	quotaService := &fakeQuotaService{db: handle, loc: location}
	service := NewService(Deps{
		DB: handle, Config: cfg, Site: site, Location: location,
		Auth: authService, Quota: quotaService, Logger: logger,
	})
	mux := http.NewServeMux()
	service.NewHandlers().Register(mux)
	sessions := httpapi.NewSessionStore(handle, false)
	middleware := httpapi.NewMiddleware(httpapi.Options{
		Config:   cfg,
		Logger:   logger,
		Sessions: sessions,
		ResolveUser: func(ctx context.Context, userID int64) (httpapi.Principal, error) {
			var username, role, status string
			var isStaff int
			err := handle.QueryRowContext(ctx,
				`SELECT username, role, status, is_staff FROM users_user WHERE id = ?`, userID).
				Scan(&username, &role, &status, &isStaff)
			if err != nil {
				return nil, err
			}
			return auth.Principal{User: &auth.User{
				IDValue: userID, Username: username, Role: role, Status: status, IsStaff: isStaff == 1,
			}}, nil
		},
	}, mux)
	return &harness{
		t:          t,
		db:         handle,
		site:       site,
		cipher:     cipher,
		auth:       authService,
		quota:      quotaService,
		csrf:       httpapi.NewCSRFToken().Masked,
		middleware: middleware,
		sessions:   sessions,
	}
}

func (h *harness) login(userID int64) {
	h.t.Helper()
	session := &httpapi.Session{Key: "testsession" + strconv.FormatInt(userID, 10) + "abcdefghijklmnop"}
	session.SetInt("user_id", userID)
	if err := h.sessions.Save(session); err != nil {
		h.t.Fatalf("save session: %v", err)
	}
	h.sessionKey = session.Key
}

func (h *harness) request(method, path string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(payload)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if h.sessionKey != "" {
		request.AddCookie(&http.Cookie{Name: "sessionid", Value: h.sessionKey})
	}
	request.AddCookie(&http.Cookie{Name: "csrftoken", Value: h.csrf})
	request.Header.Set("X-CSRFToken", h.csrf)
	recorder := httptest.NewRecorder()
	h.middleware.ServeHTTP(recorder, request)
	return recorder
}

func (h *harness) envelope(recorder *httptest.ResponseRecorder) map[string]any {
	h.t.Helper()
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		h.t.Fatalf("expected a JSON response, got %q with body %q", contentType, recorder.Body.String())
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		h.t.Fatalf("decode response: %v", err)
	}
	return decoded
}

func (h *harness) data(recorder *httptest.ResponseRecorder) map[string]any {
	h.t.Helper()
	envelope := h.envelope(recorder)
	if code, _ := envelope["code"].(float64); code != 0 {
		h.t.Fatalf("expected success, got %v", envelope)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		h.t.Fatalf("expected object data, got %v", envelope["data"])
	}
	return data
}

func (h *harness) dataItems(recorder *httptest.ResponseRecorder) []any {
	h.t.Helper()
	envelope := h.envelope(recorder)
	items, ok := envelope["data"].([]any)
	if !ok {
		h.t.Fatalf("expected array data, got %v", envelope["data"])
	}
	return items
}

func (h *harness) failure(recorder *httptest.ResponseRecorder) (int, string) {
	h.t.Helper()
	envelope := h.envelope(recorder)
	code, _ := envelope["code"].(float64)
	message, _ := envelope["message"].(string)
	return int(code), message
}

func (h *harness) mustEqual(got, want any) {
	h.t.Helper()
	if got != want {
		h.t.Fatalf("got %v, want %v", got, want)
	}
}

func (h *harness) exec(query string, args ...any) {
	h.t.Helper()
	if _, err := h.db.Exec(query, args...); err != nil {
		h.t.Fatalf("exec %q: %v", query, err)
	}
}

func (h *harness) seedUser(id int64, username, role, status string, isStaff bool, joined time.Time) {
	h.t.Helper()
	h.auth.users[id] = &auth.User{IDValue: id, Username: username, Role: role, Status: status, IsStaff: isStaff}
	h.exec(`INSERT INTO users_user
			(id, password, last_login, is_superuser, username, first_name, last_name, is_staff, is_active,
			 date_joined, email, mobile, role, status, last_login_ip, last_login_at)
		VALUES (?, '!', NULL, 0, ?, '', '', ?, 1, ?, ?, NULL, ?, ?, NULL, NULL)`,
		id, username, db.Bool(isStaff), db.FormatTime(joined), username+"@example.com", role, status)
}

func (h *harness) seedQuota(userID int64, daily, available, frozen, total int, lastReset string) {
	h.t.Helper()
	now := db.FormatTime(time.Now().UTC())
	h.exec(`INSERT INTO billing_quotaaccount
			(created_at, updated_at, daily_quota, available_quota, frozen_quota, total_generated_count, user_id, last_reset_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, now, now, daily, available, frozen, total, userID, lastReset)
}

func (h *harness) seedChannel(id int64, code, name string, active bool) {
	h.t.Helper()
	now := db.FormatTime(time.Now().UTC())
	h.exec(`INSERT INTO generator_imagechannel
			(id, created_at, updated_at, name, channel_code, base_url, api_path, api_key_encrypted, model_name,
			 timeout_seconds, max_retries, is_active, priority, channel_type, provider_name, edit_api_path, provider_vendor)
		VALUES (?, ?, ?, ?, ?, 'http://127.0.0.1:9000/v1', '/images/generations', '', 'test-model',
			60, 1, ?, 1, 'image', 'Test', '', 'openai')`,
		id, now, now, name, code, db.Bool(active))
}

type taskSeed struct {
	id           int64
	taskNo       string
	userID       int64
	channelID    int64
	status       string
	prompt       string
	errorMessage string
	responseJSON string
	createdAt    time.Time
	finishedAt   *time.Time
}

func (h *harness) seedTask(seed taskSeed) {
	h.t.Helper()
	response := seed.responseJSON
	if response == "" {
		response = "{}"
	}
	h.exec(`INSERT INTO generator_generatetask
			(id, created_at, updated_at, task_no, status, prompt, negative_prompt, style_code, size_code, quota_cost,
			 provider_request_id, provider_response_json, error_code, error_message, submitted_at, started_at,
			 finished_at, client_request_id, client_ip, quota_finalized, user_id, topic_id, channel_id,
			 progress_percent, deleted_at, is_deleted, generation_mode, reference_image_data_url, reference_image_name,
			 reference_storage_provider, reference_bucket_name, reference_object_key, reference_image_mime_type,
			 reference_image_size, reference_image_width, reference_image_height, reference_image_sha256,
			 execution_attempts, lease_expires_at, quality_code)
		VALUES (?, ?, ?, ?, ?, ?, '', 'general', '1024x1024', 1, '', ?, '', ?, ?, NULL, ?, '', NULL, 0, ?, NULL, ?,
			100, NULL, 0, 'text_to_image', '', '', '', '', '', '', 0, 0, 0, '', 1, NULL, 'auto')`,
		seed.id, db.FormatTime(seed.createdAt), db.FormatTime(seed.createdAt), seed.taskNo, seed.status,
		seed.prompt, response, seed.errorMessage, db.FormatTime(seed.createdAt),
		db.NullableTime(seed.finishedAt), seed.userID, seed.channelID)
}

const failedTaskNo = "G20260101000000ABCDEF01"

func TestDashboardRecentFailuresReadsFailedTasksWithRawDetail(t *testing.T) {
	h := newHarness(t)
	now := time.Now().UTC()
	h.seedUser(1, "operator", "admin", "active", true, now)
	h.seedUser(2, "creator", "user", "active", false, now)
	h.seedChannel(1, "default", "默认渠道", true)
	h.seedQuota(2, 20, 1, 0, 0, now.Format("2006-01-02"))
	h.login(1)

	h.seedTask(taskSeed{
		id:           1,
		taskNo:       failedTaskNo,
		userID:       2,
		channelID:    1,
		status:       "failed",
		prompt:       "后台需要看到失败日志",
		errorMessage: "缺少 OpenAI 兼容渠道 API Key",
		responseJSON: `{"failure": {"exception_type": "ValueError", "error_message": "缺少 OpenAI 兼容渠道 API Key"}}`,
		createdAt:    now,
		finishedAt:   &now,
	})

	recorder := h.request(http.MethodGet, "/console/api/dashboard/", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("dashboard status %d: %s", recorder.Code, recorder.Body.String())
	}
	data := h.data(recorder)

	today, ok := data["today"].(map[string]any)
	if !ok {
		t.Fatalf("today is not an object: %v", data["today"])
	}
	h.mustEqual(today["failed_tasks"], float64(1))

	latest := data["latest_tasks"].([]any)
	if len(latest) != 1 {
		t.Fatalf("latest_tasks length %d", len(latest))
	}
	first := latest[0].(map[string]any)
	h.mustEqual(first["task_no"], failedTaskNo)
	h.mustEqual(first["error_message"], "缺少 OpenAI 兼容渠道 API Key")
	h.mustEqual(first["username"], "creator")
	h.mustEqual(first["channel_name"], "默认渠道")
	response := first["provider_response_json"].(map[string]any)
	if _, ok := response["failure"].(map[string]any); !ok {
		t.Fatalf("provider_response_json is not nested: %v", response)
	}

	failures := data["recent_failures"].([]any)
	if len(failures) != 1 {
		t.Fatalf("recent_failures length %d", len(failures))
	}
	failure := failures[0].(map[string]any)
	h.mustEqual(failure["task_no"], failedTaskNo)
	h.mustEqual(failure["error_message"], "缺少 OpenAI 兼容渠道 API Key")
	h.mustEqual(failure["event_type"], "generation_failed")
	h.mustEqual(failure["source"], "default")
	h.mustEqual(failure["success"], false)
	payload := failure["payload_json"].(map[string]any)
	h.mustEqual(payload["exception_type"], "ValueError")
}

func TestDashboardTotalsShape(t *testing.T) {
	h := newHarness(t)
	now := time.Now().UTC()
	h.seedUser(1, "operator", "admin", "active", true, now)
	h.seedUser(2, "creator", "user", "active", false, now)
	h.seedChannel(1, "default", "默认渠道", true)
	h.seedChannel(2, "disabled", "停用渠道", false)
	h.login(1)
	h.seedTask(taskSeed{id: 1, taskNo: failedTaskNo, userID: 2, channelID: 1, status: "succeeded", prompt: "ok", createdAt: now})
	h.seedTask(taskSeed{id: 2, taskNo: "G20260101000000ABCDEF02", userID: 2, channelID: 1, status: "failed", prompt: "bad", createdAt: now})
	h.exec(`INSERT INTO mediahub_generatedimage
			(created_at, updated_at, image_index, storage_provider, bucket_name, object_key, mime_type, file_size,
			 width, height, sha256, status, expires_at, deleted_at, task_id, user_id, preview_object_key)
		VALUES (?, ?, 1, 'local', 'media', 'k', 'image/png', 1, 1, 1, 'x', 'available', ?, NULL, 1, 2, '')`,
		db.FormatTime(now), db.FormatTime(now), db.FormatTime(now.Add(24*time.Hour)))

	recorder := h.request(http.MethodGet, "/console/api/dashboard/", nil)
	data := h.data(recorder)
	today := data["today"].(map[string]any)
	for _, key := range []string{"new_users", "tasks", "failed_tasks", "succeeded_tasks"} {
		if _, ok := today[key]; !ok {
			t.Fatalf("today is missing %s: %v", key, today)
		}
	}
	h.mustEqual(today["new_users"], float64(2))
	h.mustEqual(today["tasks"], float64(2))
	h.mustEqual(today["failed_tasks"], float64(1))
	h.mustEqual(today["succeeded_tasks"], float64(1))

	totals := data["totals"].(map[string]any)
	h.mustEqual(totals["users"], float64(2))
	h.mustEqual(totals["images"], float64(1))
	h.mustEqual(totals["tasks"], float64(2))
	h.mustEqual(totals["active_channels"], float64(1))
	if _, ok := data["latest_users"].([]any); !ok {
		t.Fatalf("latest_users is not an array: %v", data["latest_users"])
	}
	if _, ok := data["recent_failures"].([]any); !ok {
		t.Fatalf("recent_failures is not an array: %v", data["recent_failures"])
	}
}

func TestDisabledOperatorCannotAccessConsoleAPI(t *testing.T) {
	h := newHarness(t)
	h.seedUser(1, "operator", "admin", "disabled", true, time.Now().UTC())
	h.login(1)

	recorder := h.request(http.MethodGet, "/console/api/dashboard/", nil)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", recorder.Code)
	}
	code, message := h.failure(recorder)
	h.mustEqual(code, 403001)
	h.mustEqual(message, "当前账号状态不可用")
}

func TestConsoleGuardRejectsAnonymousAndNonOperator(t *testing.T) {
	h := newHarness(t)
	h.seedUser(1, "creator", "user", "active", false, time.Now().UTC())

	recorder := h.request(http.MethodGet, "/console/api/dashboard/", nil)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status %d, want 401", recorder.Code)
	}
	code, message := h.failure(recorder)
	h.mustEqual(code, 401000)
	h.mustEqual(message, "未登录")

	h.login(1)
	recorder = h.request(http.MethodGet, "/console/api/dashboard/", nil)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("non-operator status %d, want 403", recorder.Code)
	}
	code, message = h.failure(recorder)
	h.mustEqual(code, 403000)
	h.mustEqual(message, "权限不足")
}

func TestUserListPaginationClamping(t *testing.T) {
	h := newHarness(t)
	now := time.Now().UTC()
	h.seedUser(1, "operator", "admin", "active", true, now)
	for index := 2; index <= 4; index++ {
		h.seedUser(int64(index), "user"+strconv.Itoa(index), "user", "active", false, now.Add(-time.Duration(index)*time.Minute))
	}
	h.login(1)

	record := func(query string) (map[string]any, []any) {
		t.Helper()
		recorder := h.request(http.MethodGet, "/console/api/users/"+query, nil)
		data := h.data(recorder)
		pagination, ok := data["pagination"].(map[string]any)
		if !ok {
			t.Fatalf("pagination is not an object: %v", data["pagination"])
		}
		return pagination, data["items"].([]any)
	}

	pagination, items := record("?per_page=0")
	h.mustEqual(pagination["per_page"], float64(1))
	h.mustEqual(len(items), 1)

	pagination, _ = record("?per_page=999")
	h.mustEqual(pagination["per_page"], float64(100))

	pagination, _ = record("?per_page=not-a-number")
	h.mustEqual(pagination["per_page"], float64(20))

	pagination, _ = record("?page=0")
	h.mustEqual(pagination["page"], float64(1))

	pagination, items = record("?per_page=2&page=99")
	h.mustEqual(pagination["page"], float64(2))
	h.mustEqual(pagination["total_pages"], float64(2))
	h.mustEqual(pagination["total"], float64(4))
	h.mustEqual(pagination["has_next"], false)
	h.mustEqual(pagination["has_previous"], true)
	h.mustEqual(len(items), 2)
}

func TestUserListFiltersAndQuotaEnsuring(t *testing.T) {
	h := newHarness(t)
	now := time.Now().UTC()
	h.seedUser(1, "operator", "admin", "active", true, now)
	h.seedUser(2, "diff-user", "user", "active", false, now.Add(-time.Minute))
	h.seedUser(3, "taken", "user", "disabled", false, now.Add(-2*time.Minute))
	h.login(1)

	recorder := h.request(http.MethodGet, "/console/api/users/?keyword=diff&status=active&role=user", nil)
	data := h.data(recorder)
	items := data["items"].([]any)
	h.mustEqual(len(items), 1)
	user := items[0].(map[string]any)
	h.mustEqual(user["username"], "diff-user")
	h.mustEqual(user["mobile"], "")
	quota := user["quota"].(map[string]any)
	h.mustEqual(quota["daily_quota"], float64(3))
	h.mustEqual(quota["available_quota"], float64(3))
	h.mustEqual(quota["total_generated_count"], float64(0))
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	h.mustEqual(quota["last_reset_date"], time.Now().In(location).Format("2006-01-02"))

	var accounts int
	if err := h.db.QueryRow(`SELECT COUNT(1) FROM billing_quotaaccount WHERE user_id IN (2, 3)`).Scan(&accounts); err != nil {
		t.Fatalf("count quota accounts: %v", err)
	}
	if accounts == 0 {
		t.Fatal("listing users did not ensure quota accounts")
	}

	detail := h.data(h.request(http.MethodGet, "/console/api/users/2/", nil))
	h.mustEqual(detail["last_login_ip"], nil)
	if _, ok := detail["profile"]; !ok {
		t.Fatalf("detail is missing profile: %v", detail)
	}
	missing := h.request(http.MethodGet, "/console/api/users/999999/", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing user status %d, want 404", missing.Code)
	}
	code, message := h.failure(missing)
	h.mustEqual(code, 404000)
	h.mustEqual(message, "用户不存在")
}

func TestConfigPutThenGetRoundTripAndSecretMasking(t *testing.T) {
	h := newHarness(t)
	h.seedUser(1, "operator", "admin", "active", true, time.Now().UTC())
	h.login(1)

	written := h.data(h.request(http.MethodPut, "/console/api/config/site.brand_name/", map[string]any{"value": "绘镜测试"}))
	h.mustEqual(written["key"], "site.brand_name")
	updatedAt, ok := written["updated_at"].(string)
	if !ok || !strings.Contains(updatedAt, "T") || !strings.Contains(updatedAt, "+00:00") {
		t.Fatalf("updated_at is not an ISO timestamp: %v", written["updated_at"])
	}

	read := h.data(h.request(http.MethodGet, "/console/api/config/site.brand_name/", nil))
	h.mustEqual(read["value"], "绘镜测试")
	h.mustEqual(read["is_secret"], false)
	h.mustEqual(read["category"], "site")
	h.mustEqual(read["description"], "品牌中文名")

	const secretValue = "super-secret-value-123"
	h.request(http.MethodPut, "/console/api/config/custom.secret/", map[string]any{
		"value": secretValue, "is_secret": true, "description": "测试密钥", "category": "general",
	})
	secret := h.data(h.request(http.MethodGet, "/console/api/config/custom.secret/", nil))
	h.mustEqual(secret["value"], "******")
	h.mustEqual(secret["is_secret"], true)
	h.mustEqual(secret["description"], "测试密钥")

	var stored string
	if err := h.db.QueryRow(`SELECT value_encrypted FROM core_siteconfig WHERE key = ?`, "custom.secret").Scan(&stored); err != nil {
		t.Fatalf("load stored value: %v", err)
	}
	if stored == secretValue || !strings.HasPrefix(stored, "v1:") {
		t.Fatalf("secret is not encrypted at rest: %q", stored)
	}
	plain, err := h.cipher.Decrypt(stored)
	if err != nil {
		t.Fatalf("decrypt stored value: %v", err)
	}
	h.mustEqual(plain, secretValue)

	list := h.data(h.request(http.MethodGet, "/console/api/config/?category=general", nil))
	listed := list["items"].([]any)
	if len(listed) != 1 {
		t.Fatalf("general category length %d", len(listed))
	}
	h.mustEqual(listed[0].(map[string]any)["value"], "******")
	categories := list["categories"].([]any)
	h.mustEqual(len(categories), 4)
	h.mustEqual(categories[0].(map[string]any)["code"], "general")
	h.mustEqual(categories[0].(map[string]any)["label"], "通用")

	unknown := h.request(http.MethodGet, "/console/api/config/missing.key/", nil)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown config status %d, want 404", unknown.Code)
	}
	code, message := h.failure(unknown)
	h.mustEqual(code, 404000)
	h.mustEqual(message, "配置项不存在")

	missingValue := h.request(http.MethodPut, "/console/api/config/site.brand_name/", map[string]any{})
	if missingValue.Code != http.StatusBadRequest {
		t.Fatalf("missing value status %d, want 400", missingValue.Code)
	}
	code, message = h.failure(missingValue)
	h.mustEqual(code, 400001)
	h.mustEqual(message, "缺少 value")

	created := h.data(h.request(http.MethodPut, "/console/api/config/missing.key/", map[string]any{"value": "1"}))
	h.mustEqual(created["key"], "missing.key")
	createdRead := h.data(h.request(http.MethodGet, "/console/api/config/missing.key/", nil))
	h.mustEqual(createdRead["category"], "general")
	h.mustEqual(createdRead["description"], "")
	h.mustEqual(createdRead["value"], "1")
}

func TestConfigBatchAndCollectionMethodNotAllowed(t *testing.T) {
	h := newHarness(t)
	h.seedUser(1, "operator", "admin", "active", true, time.Now().UTC())
	h.login(1)

	batch := h.data(h.request(http.MethodPut, "/console/api/config/batch/", map[string]any{
		"items": []any{
			map[string]any{"key": "site.brand_tagline", "value": "差分标语"},
			map[string]any{"key": "risk.task_limit_per_window", "value": "3"},
		},
	}))
	h.mustEqual(batch["updated"], float64(2))

	updated := h.data(h.request(http.MethodGet, "/console/api/config/site.brand_tagline/", nil))
	h.mustEqual(updated["value"], "差分标语")
	risk := h.data(h.request(http.MethodGet, "/console/api/config/risk.task_limit_per_window/", nil))
	h.mustEqual(risk["value"], "3")

	empty := h.request(http.MethodPut, "/console/api/config/batch/", map[string]any{"items": []any{}})
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("empty batch status %d, want 400", empty.Code)
	}
	code, message := h.failure(empty)
	h.mustEqual(code, 400001)
	h.mustEqual(message, "缺少 items 数组")

	notAllowed := h.request(http.MethodPut, "/console/api/config/", map[string]any{})
	if notAllowed.Code != http.StatusMethodNotAllowed {
		t.Fatalf("collection PUT status %d, want 405", notAllowed.Code)
	}
	h.mustEqual(notAllowed.Header().Get("Allow"), "GET, HEAD, OPTIONS")
	if contentType := notAllowed.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("405 content type %q", contentType)
	}
	body := strings.ToLower(notAllowed.Body.String())
	if strings.Contains(body, "csrf") || strings.Contains(body, "403") {
		t.Fatalf("405 body must not look like a CSRF rejection: %q", body)
	}

	options := h.request(http.MethodOptions, "/console/api/config/", nil)
	if options.Code != http.StatusOK {
		t.Fatalf("collection OPTIONS status %d, want 200", options.Code)
	}
	h.mustEqual(options.Header().Get("Allow"), "GET, HEAD, OPTIONS")

	deleted := h.request(http.MethodDelete, "/console/api/config/", nil)
	if deleted.Code != http.StatusMethodNotAllowed {
		t.Fatalf("collection DELETE status %d, want 405", deleted.Code)
	}
	h.mustEqual(deleted.Header().Get("Allow"), "GET, HEAD, OPTIONS")
}

func TestChannelCreateDuplicateAndDeleteBranches(t *testing.T) {
	h := newHarness(t)
	h.seedUser(1, "operator", "admin", "active", true, time.Now().UTC())
	h.login(1)

	payload := map[string]any{
		"name": "差分渠道", "channel_code": "diff-channel", "channel_type": "image",
		"provider_name": "Diff", "base_url": "https://images.example.com/v1",
		"api_path": "/images/generations", "model_name": "diff-model",
		"timeout_seconds": 42, "max_retries": 2, "priority": 5, "api_key": "diff-secret-key",
	}
	recorder := h.request(http.MethodPost, "/console/api/image-channels/", payload)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", recorder.Code, recorder.Body.String())
	}
	created := h.data(recorder)
	if _, echoed := created["api_key"]; echoed {
		t.Fatalf("api_key must never be echoed: %v", created)
	}
	h.mustEqual(created["channel_code"], "diff-channel")
	h.mustEqual(created["max_retries"], float64(2))
	h.mustEqual(created["timeout_seconds"], float64(42))
	h.mustEqual(created["is_active"], true)
	channelID := int64(created["id"].(float64))

	var stored string
	if err := h.db.QueryRow(`SELECT api_key_encrypted FROM generator_imagechannel WHERE id = ?`, channelID).Scan(&stored); err != nil {
		t.Fatalf("load channel key: %v", err)
	}
	if stored == "diff-secret-key" || stored == "" {
		t.Fatalf("api key is not encrypted: %q", stored)
	}
	plain, err := h.cipher.Decrypt(stored)
	if err != nil {
		t.Fatalf("decrypt channel key: %v", err)
	}
	h.mustEqual(plain, "diff-secret-key")

	duplicate := h.request(http.MethodPost, "/console/api/image-channels/", payload)
	if duplicate.Code != http.StatusBadRequest {
		t.Fatalf("duplicate status %d, want 400", duplicate.Code)
	}
	code, message := h.failure(duplicate)
	h.mustEqual(code, 400002)
	h.mustEqual(message, "渠道编码已存在")

	missing := h.request(http.MethodPost, "/console/api/image-channels/", map[string]any{"name": "缺字段"})
	code, message = h.failure(missing)
	h.mustEqual(code, 400001)
	h.mustEqual(message, "缺少 channel_code")

	requireField := func(field string) {
		t.Helper()
		body := map[string]any{"name": "n", "channel_code": "c-" + field, "base_url": "https://x.example.com", "model_name": "m"}
		delete(body, field)
		if field == "name" {
			body["name"] = ""
		}
		response := h.request(http.MethodPost, "/console/api/image-channels/", body)
		gotCode, gotMessage := h.failure(response)
		h.mustEqual(gotCode, 400001)
		h.mustEqual(gotMessage, "缺少 "+field)
	}
	for _, field := range []string{"name", "base_url", "model_name"} {
		requireField(field)
	}

	invalidType := h.request(http.MethodPost, "/console/api/image-channels/", map[string]any{
		"name": "n", "channel_code": "bad-type", "base_url": "https://x.example.com",
		"model_name": "m", "channel_type": "video",
	})
	code, message = h.failure(invalidType)
	h.mustEqual(code, 400001)
	h.mustEqual(message, "不支持的模型类型")

	updated := h.data(h.request(http.MethodPut, "/console/api/image-channels/"+strconv.FormatInt(channelID, 10)+"/", map[string]any{
		"name": "差分渠道改名", "is_active": false, "max_retries": 4, "api_key": "rotated-key",
	}))
	if _, echoed := updated["api_key"]; echoed {
		t.Fatalf("api_key must never be echoed: %v", updated)
	}
	h.mustEqual(updated["name"], "差分渠道改名")
	h.mustEqual(updated["is_active"], false)
	h.mustEqual(updated["max_retries"], float64(4))
	h.mustEqual(updated["channel_code"], "diff-channel")

	h.seedTask(taskSeed{id: 1, taskNo: failedTaskNo, userID: 1, channelID: channelID, status: "succeeded", prompt: "p", createdAt: time.Now().UTC()})
	guarded := h.request(http.MethodDelete, "/console/api/image-channels/"+strconv.FormatInt(channelID, 10)+"/", nil)
	if guarded.Code != http.StatusBadRequest {
		t.Fatalf("delete with tasks status %d, want 400", guarded.Code)
	}
	code, message = h.failure(guarded)
	h.mustEqual(code, 400003)
	h.mustEqual(message, "该渠道有关联任务，无法删除，请先禁用")

	h.seedChannel(9, "free-channel", "可删渠道", true)
	deleted := h.data(h.request(http.MethodDelete, "/console/api/image-channels/9/", nil))
	h.mustEqual(deleted["deleted"], true)
	gone := h.request(http.MethodGet, "/console/api/image-channels/9/", nil)
	if gone.Code != http.StatusNotFound {
		t.Fatalf("deleted channel status %d, want 404", gone.Code)
	}
	missingChannel := h.request(http.MethodGet, "/console/api/image-channels/999999/", nil)
	code, message = h.failure(missingChannel)
	h.mustEqual(code, 404000)
	h.mustEqual(message, "渠道不存在")

	listed := h.dataItems(h.request(http.MethodGet, "/console/api/image-channels/", nil))
	if len(listed) != 1 {
		t.Fatalf("channel list length %d", len(listed))
	}
}

func TestQuotaAdjustValidationAndSuccess(t *testing.T) {
	h := newHarness(t)
	h.seedUser(1, "operator", "admin", "active", true, time.Now().UTC())
	h.seedUser(2, "creator", "user", "active", false, time.Now().UTC())
	h.login(1)

	rejected := func(body any, wantCode int, wantMessage string) {
		t.Helper()
		recorder := h.request(http.MethodPost, "/console/api/users/2/quota/", body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400 (%s)", recorder.Code, recorder.Body.String())
		}
		code, message := h.failure(recorder)
		h.mustEqual(code, wantCode)
		h.mustEqual(message, wantMessage)
	}
	rejected(map[string]any{}, 400001, "至少传入 daily_quota 或 available_quota")
	rejected(map[string]any{"available_quota": -5}, 400002, "当前剩余次数不能小于 0")
	rejected(map[string]any{"daily_quota": -1}, 400002, "每日免费次数不能小于 0")

	var accounts int
	if err := h.db.QueryRow(`SELECT COUNT(1) FROM billing_quotaaccount WHERE user_id = 2`).Scan(&accounts); err != nil {
		t.Fatalf("count quota accounts: %v", err)
	}
	h.mustEqual(accounts, 0)

	unknown := h.request(http.MethodPost, "/console/api/users/999999/quota/", map[string]any{"daily_quota": 1})
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown user status %d, want 404", unknown.Code)
	}
	code, message := h.failure(unknown)
	h.mustEqual(code, 404000)
	h.mustEqual(message, "用户不存在")

	account := h.data(h.request(http.MethodPost, "/console/api/users/2/quota/", map[string]any{
		"daily_quota": 9, "available_quota": 7, "remark": "差分调整",
	}))
	expectedKeys := map[string]bool{
		"user_id": true, "daily_quota": true, "available_quota": true,
		"frozen_quota": true, "total_generated_count": true, "last_reset_date": true,
	}
	if len(account) != len(expectedKeys) {
		t.Fatalf("quota payload has unexpected keys: %v", account)
	}
	for key := range expectedKeys {
		if _, ok := account[key]; !ok {
			t.Fatalf("quota payload is missing %s: %v", key, account)
		}
	}
	h.mustEqual(account["user_id"], float64(2))
	h.mustEqual(account["daily_quota"], float64(9))
	h.mustEqual(account["available_quota"], float64(7))
	h.mustEqual(account["frozen_quota"], float64(0))
	h.mustEqual(account["total_generated_count"], float64(0))
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	h.mustEqual(account["last_reset_date"], time.Now().In(location).Format("2006-01-02"))

	var daily, available int
	if err := h.db.QueryRow(`SELECT daily_quota, available_quota FROM billing_quotaaccount WHERE user_id = 2`).Scan(&daily, &available); err != nil {
		t.Fatalf("load quota account: %v", err)
	}
	h.mustEqual(daily, 9)
	h.mustEqual(available, 7)

	if len(h.quota.remarks) != 3 {
		t.Fatalf("admin adjust calls %d, want 3", len(h.quota.remarks))
	}
	h.mustEqual(h.quota.remarks[0], "[管理员:operator] 后台修改免费次数")
	h.mustEqual(h.quota.remarks[1], "[管理员:operator] 后台修改免费次数")
	h.mustEqual(h.quota.remarks[2], "[管理员:operator] 差分调整")
}

func TestTaskListFilters(t *testing.T) {
	h := newHarness(t)
	now := time.Now().UTC()
	h.seedUser(1, "operator", "admin", "active", true, now)
	h.seedUser(2, "diff-user", "user", "active", false, now)
	h.seedChannel(1, "default", "默认渠道", true)
	h.login(1)
	h.seedTask(taskSeed{id: 1, taskNo: failedTaskNo, userID: 2, channelID: 1, status: "failed", prompt: "包含关键词", errorMessage: "boom", createdAt: now.Add(-time.Minute)})
	h.seedTask(taskSeed{id: 2, taskNo: "G20260101000000ABCDEF02", userID: 2, channelID: 1, status: "succeeded", prompt: "正常", createdAt: now})

	data := h.data(h.request(http.MethodGet, "/console/api/tasks/?status=failed&keyword=关键词", nil))
	items := data["items"].([]any)
	h.mustEqual(len(items), 1)
	first := items[0].(map[string]any)
	h.mustEqual(first["task_no"], failedTaskNo)
	h.mustEqual(first["status"], "failed")
	h.mustEqual(first["error_message"], "boom")
	response, ok := first["provider_response_json"].(map[string]any)
	if !ok || len(response) != 0 {
		t.Fatalf("provider_response_json is not an empty object: %v", first["provider_response_json"])
	}
	pagination := data["pagination"].(map[string]any)
	h.mustEqual(pagination["total"], float64(1))
	h.mustEqual(pagination["has_next"], false)
}

func TestChannelCreateDefaults(t *testing.T) {
	h := newHarness(t)
	h.seedUser(1, "operator", "admin", "active", true, time.Now().UTC())
	h.login(1)

	created := h.data(h.request(http.MethodPost, "/console/api/image-channels/", map[string]any{
		"name": "默认渠道", "channel_code": "defaults",
		"base_url": "https://images.example.com/v1", "model_name": "m",
	}))
	h.mustEqual(created["channel_type"], "image")
	h.mustEqual(created["provider_name"], "")
	h.mustEqual(created["api_path"], "/images/generations")
	h.mustEqual(created["edit_api_path"], "")
	h.mustEqual(created["timeout_seconds"], float64(60))
	h.mustEqual(created["max_retries"], float64(3))
	h.mustEqual(created["is_active"], true)
	h.mustEqual(created["priority"], float64(1))
	if _, echoed := created["provider_vendor"]; echoed {
		t.Fatalf("provider_vendor is not part of the console payload: %v", created)
	}
}

func TestConsoleSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	h.seedUser(1, "operator", "admin", "active", true, time.Now().UTC())

	anonymous := h.request(http.MethodGet, "/console/api/dashboard/", nil)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status %d, want 401", anonymous.Code)
	}
	h.mustEqual(anonymous.Header().Get("Cache-Control"), "")
	h.mustEqual(anonymous.Header().Get("X-Robots-Tag"), "")
	h.mustEqual(anonymous.Header().Get("X-Content-Type-Options"), "nosniff")

	h.login(1)
	recorder := h.request(http.MethodGet, "/console/api/dashboard/", nil)
	h.mustEqual(recorder.Header().Get("Cache-Control"), "no-store, no-cache, must-revalidate, max-age=0")
	h.mustEqual(recorder.Header().Get("Pragma"), "no-cache")
	h.mustEqual(recorder.Header().Get("X-Robots-Tag"), "noindex, nofollow")
	h.mustEqual(recorder.Header().Get("X-Content-Type-Options"), "nosniff")
	h.mustEqual(recorder.Header().Get("X-Frame-Options"), "DENY")
}

func (h *harness) assertKeys(payload map[string]any, keys ...string) {
	h.t.Helper()
	if len(payload) != len(keys) {
		h.t.Fatalf("payload has %d keys, want %d: %v", len(payload), len(keys), payload)
	}
	for _, key := range keys {
		if _, ok := payload[key]; !ok {
			h.t.Fatalf("payload is missing %s: %v", key, payload)
		}
	}
}

// TestSerializedShapes pins the field sets of api-contract §8 so a missing or
// extra field fails loudly.
func TestSerializedShapes(t *testing.T) {
	h := newHarness(t)
	now := time.Now().UTC()
	h.seedUser(1, "operator", "admin", "active", true, now)
	h.seedUser(2, "creator", "user", "active", false, now.Add(-time.Minute))
	h.seedChannel(1, "default", "默认渠道", true)
	h.login(1)
	h.seedTask(taskSeed{
		id: 1, taskNo: failedTaskNo, userID: 2, channelID: 1, status: "failed",
		prompt: "p", errorMessage: "boom", responseJSON: `{"failure": {"exception_type": "ValueError"}}`,
		createdAt: now, finishedAt: &now,
	})

	dashboard := h.data(h.request(http.MethodGet, "/console/api/dashboard/", nil))
	h.assertKeys(dashboard, "today", "totals", "latest_tasks", "latest_users", "recent_failures")
	h.assertKeys(dashboard["today"].(map[string]any), "new_users", "tasks", "failed_tasks", "succeeded_tasks")
	h.assertKeys(dashboard["totals"].(map[string]any), "users", "images", "tasks", "active_channels")

	users := h.data(h.request(http.MethodGet, "/console/api/users/", nil))
	user := users["items"].([]any)[0].(map[string]any)
	h.assertKeys(user, "id", "username", "email", "mobile", "role", "status", "is_staff",
		"date_joined", "last_login", "last_login_ip", "quota")
	h.assertKeys(user["quota"].(map[string]any), "daily_quota", "available_quota", "frozen_quota",
		"total_generated_count", "last_reset_date")

	detail := h.data(h.request(http.MethodGet, "/console/api/users/2/", nil))
	h.assertKeys(detail, "id", "username", "email", "mobile", "role", "status", "is_staff",
		"date_joined", "last_login", "last_login_ip", "quota", "profile")
	if _, ok := detail["profile"]; !ok {
		t.Fatal("profile key is missing")
	}

	tasks := h.data(h.request(http.MethodGet, "/console/api/tasks/", nil))
	task := tasks["items"].([]any)[0].(map[string]any)
	h.assertKeys(task, "id", "task_no", "user_id", "username", "status", "prompt", "style_code",
		"size_code", "quality_code", "channel_name", "quota_cost", "error_code", "error_message",
		"provider_request_id", "provider_response_json", "created_at", "finished_at")

	failure := dashboard["recent_failures"].([]any)[0].(map[string]any)
	h.assertKeys(failure, "id", "event_type", "source", "success", "remark", "task_no", "error_code",
		"error_message", "provider_request_id", "payload_json", "created_at")

	channel := h.dataItems(h.request(http.MethodGet, "/console/api/image-channels/", nil))[0].(map[string]any)
	h.assertKeys(channel, "id", "name", "channel_code", "channel_type", "provider_name", "base_url",
		"api_path", "edit_api_path", "model_name", "timeout_seconds", "max_retries", "is_active",
		"priority", "created_at", "updated_at")

	h.data(h.request(http.MethodPut, "/console/api/config/site.brand_name/", map[string]any{"value": "绘镜"}))
	configs := h.data(h.request(http.MethodGet, "/console/api/config/", nil))
	h.assertKeys(configs, "items", "categories")
	h.assertKeys(configs["categories"].([]any)[0].(map[string]any), "code", "label")
	if len(configs["items"].([]any)) == 0 {
		t.Fatal("seeded configuration is empty")
	}
	item := configs["items"].([]any)[0].(map[string]any)
	h.assertKeys(item, "id", "key", "value", "is_secret", "description", "category", "updated_at")

	pagination := tasks["pagination"].(map[string]any)
	h.assertKeys(pagination, "page", "per_page", "total", "total_pages", "has_next", "has_previous")
}
