package risk

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mirror-studio/internal/config"
	"mirror-studio/internal/db"
	"mirror-studio/internal/siteconfig"
)

func newTestService(t *testing.T, cfg *config.Config) (*Service, *db.DB) {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "risk.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if cfg == nil {
		cfg = &config.Config{
			RiskLoginFailLimit:   5,
			RiskLoginLockMinutes: 15,
			RiskTaskWindow:       60 * time.Second,
			RiskTaskLimit:        10,
		}
	}
	site := siteconfig.New(handle, nil, nil)
	return NewService(handle, site, cfg, nil), handle
}

func insertIPRule(t *testing.T, handle *db.DB, ip string, blocked bool, expires any) {
	t.Helper()
	now := db.FormatTime(time.Now())
	if _, err := handle.Exec(
		`INSERT INTO risk_ipaccessrule (created_at, updated_at, ip_address, is_blocked, reason, expires_at)
		 VALUES (?, ?, ?, ?, '', ?)`,
		now, now, ip, db.Bool(blocked), expires,
	); err != nil {
		t.Fatalf("insert ip rule %s: %v", ip, err)
	}
}

func insertSensitiveWord(t *testing.T, handle *db.DB, keyword, mode string, active bool) {
	t.Helper()
	now := db.FormatTime(time.Now())
	if _, err := handle.Exec(
		`INSERT INTO risk_sensitivewordrule (created_at, updated_at, keyword, match_mode, is_active, remark)
		 VALUES (?, ?, ?, ?, ?, '')`,
		now, now, keyword, mode, db.Bool(active),
	); err != nil {
		t.Fatalf("insert sensitive word %s: %v", keyword, err)
	}
}

func insertConfig(t *testing.T, handle *db.DB, key, value string) {
	t.Helper()
	now := db.FormatTime(time.Now())
	if _, err := handle.Exec(
		`INSERT INTO core_siteconfig (created_at, updated_at, key, value_encrypted, is_secret, description, category)
		 VALUES (?, ?, ?, ?, 0, '', 'risk')`,
		now, now, key, value,
	); err != nil {
		t.Fatalf("insert siteconfig %s: %v", key, err)
	}
}

func countRiskEvents(t *testing.T, handle *db.DB) int {
	t.Helper()
	var count int
	if err := handle.QueryRow(`SELECT COUNT(1) FROM risk_riskevent`).Scan(&count); err != nil {
		t.Fatalf("count risk events: %v", err)
	}
	return count
}

func TestLoginLockoutThresholdAndWindow(t *testing.T) {
	service, handle := newTestService(t, &config.Config{
		RiskLoginFailLimit:   2,
		RiskLoginLockMinutes: 15,
		RiskTaskWindow:       time.Minute,
		RiskTaskLimit:        10,
	})
	ctx := context.Background()
	const username, ip = "diff-user", "198.51.100.8"

	if err := service.EnsureLoginAllowed(ctx, username, ip); err != nil {
		t.Fatalf("first attempt must pass: %v", err)
	}
	if got := service.Failures(ctx, username, ip); got != 0 {
		t.Fatalf("failures before any failure = %d, want 0", got)
	}
	if got := service.RecordLoginFailure(ctx, username, ip); got != 1 {
		t.Fatalf("first recorded failure = %d, want 1", got)
	}
	if err := service.EnsureLoginAllowed(ctx, username, ip); err != nil {
		t.Fatalf("one failure below the threshold of 2 must pass: %v", err)
	}
	if got := service.RecordLoginFailure(ctx, username, ip); got != 2 {
		t.Fatalf("second recorded failure = %d, want 2", got)
	}
	err := service.EnsureLoginAllowed(ctx, username, ip)
	if err == nil || err.Error() != "登录失败次数过多，请 15 分钟后再试" {
		t.Fatalf("locked error = %v, want 登录失败次数过多，请 15 分钟后再试", err)
	}
	if got := service.Failures(ctx, username, ip); got != 2 {
		t.Fatalf("failures while locked = %d, want 2", got)
	}
	if err := service.EnsureLoginAllowed(ctx, username, "203.0.113.1"); err != nil {
		t.Fatalf("another IP must not inherit the lockout: %v", err)
	}

	service.ClearLoginFailures(ctx, username, ip)
	if err := service.EnsureLoginAllowed(ctx, username, ip); err != nil {
		t.Fatalf("cleared failures must unlock: %v", err)
	}
	// The reference writes an event only when a failure is recorded, never on
	// the blocked-check path.
	if got := countRiskEvents(t, handle); got != 2 {
		t.Fatalf("risk events = %d, want 2 (one per recorded failure)", got)
	}
}

func TestLockoutWindowExpires(t *testing.T) {
	service, _ := newTestService(t, &config.Config{
		RiskLoginFailLimit:   1,
		RiskLoginLockMinutes: 0,
		RiskTaskWindow:       time.Minute,
		RiskTaskLimit:        10,
	})
	ctx := context.Background()
	service.RecordLoginFailure(ctx, "diff-user", "198.51.100.8")
	if err := service.EnsureLoginAllowed(ctx, "diff-user", "198.51.100.8"); err != nil {
		t.Fatalf("zero-minute window must expire immediately: %v", err)
	}
}

func TestCountersWindow(t *testing.T) {
	counters := NewCounters()
	const window = 40 * time.Millisecond
	if got := counters.Increment("risk:task:1", window); got != 1 {
		t.Fatalf("first increment = %d, want 1", got)
	}
	if got := counters.Increment("risk:task:1", window); got != 2 {
		t.Fatalf("second increment = %d, want 2", got)
	}
	if got := counters.peek("risk:task:1"); got != 2 {
		t.Fatalf("peek = %d, want 2", got)
	}
	time.Sleep(60 * time.Millisecond)
	if got := counters.peek("risk:task:1"); got != 0 {
		t.Fatalf("peek after expiry = %d, want 0", got)
	}
	if got := counters.Increment("risk:task:1", window); got != 1 {
		t.Fatalf("increment after expiry = %d, want 1", got)
	}
	counters.reset("risk:task:1")
	if got := counters.peek("risk:task:1"); got != 0 {
		t.Fatalf("peek after reset = %d, want 0", got)
	}
}

func TestIPBlockedRules(t *testing.T) {
	service, handle := newTestService(t, nil)
	ctx := context.Background()
	past := db.FormatTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	future := db.FormatTime(time.Now().Add(time.Hour))

	insertIPRule(t, handle, "203.0.113.9", true, nil)
	insertIPRule(t, handle, "203.0.113.10", true, past)
	insertIPRule(t, handle, "203.0.113.11", true, future)
	insertIPRule(t, handle, "203.0.113.12", false, nil)

	blocked, message := service.IPBlocked(ctx, "203.0.113.9")
	if !blocked || message != "当前 IP 已被限制访问" {
		t.Fatalf("permanent block = (%v, %q), want (true, 当前 IP 已被限制访问)", blocked, message)
	}
	if blocked, _ := service.IPBlocked(ctx, "203.0.113.10"); blocked {
		t.Fatal("expired rule must not block")
	}
	if blocked, _ := service.IPBlocked(ctx, "203.0.113.11"); !blocked {
		t.Fatal("unexpired rule must block")
	}
	if blocked, _ := service.IPBlocked(ctx, "203.0.113.12"); blocked {
		t.Fatal("is_blocked=0 must not block")
	}
	if blocked, _ := service.IPBlocked(ctx, "203.0.113.99"); blocked {
		t.Fatal("unknown ip must not block")
	}
	if blocked, _ := service.IPBlocked(ctx, ""); blocked {
		t.Fatal("empty ip must not block")
	}

	// The 60s in-process cache keeps the middleware off the database.
	if _, err := handle.Exec(`DELETE FROM risk_ipaccessrule WHERE ip_address = '203.0.113.9'`); err != nil {
		t.Fatalf("delete rule: %v", err)
	}
	if blocked, _ := service.IPBlocked(ctx, "203.0.113.9"); !blocked {
		t.Fatal("cached verdict must survive the rule disappearing")
	}
}

func TestEnsurePromptSafe(t *testing.T) {
	service, handle := newTestService(t, nil)
	ctx := context.Background()
	insertSensitiveWord(t, handle, "禁止词A", "contains", true)
	insertSensitiveWord(t, handle, "exactblock", "exact", true)
	insertSensitiveWord(t, handle, "停用词", "contains", false)

	if err := service.EnsurePromptSafe(ctx, "包含禁止词A的内容"); err == nil || err.Error() != "Prompt 命中风控规则" {
		t.Fatalf("contains rule error = %v, want Prompt 命中风控规则", err)
	}
	if err := service.EnsurePromptSafe(ctx, "exactblock"); err == nil || err.Error() != "Prompt 命中风控规则" {
		t.Fatalf("exact rule error = %v, want Prompt 命中风控规则", err)
	}
	if err := service.EnsurePromptSafe(ctx, "前缀exactblock后缀"); err != nil {
		t.Fatalf("exact rule must not match a substring: %v", err)
	}
	if err := service.EnsurePromptSafe(ctx, "停用词"); err != nil {
		t.Fatalf("inactive rule must not match: %v", err)
	}
	if err := service.EnsurePromptSafe(ctx, "   "); err == nil || err.Error() != "Prompt 不能为空" {
		t.Fatalf("blank prompt error = %v, want Prompt 不能为空", err)
	}
	if err := service.EnsurePromptSafe(ctx, ""); err == nil || err.Error() != "Prompt 不能为空" {
		t.Fatalf("empty prompt error = %v, want Prompt 不能为空", err)
	}
	if err := service.EnsurePromptSafe(ctx, "完全正常的提示词"); err != nil {
		t.Fatalf("clean prompt: %v", err)
	}

	// Rules are cached for 30s, so a later insert is not visible yet.
	insertSensitiveWord(t, handle, "新增词", "contains", true)
	if err := service.EnsurePromptSafe(ctx, "包含新增词"); err != nil {
		t.Fatalf("rule inserted after the cache refresh must not match yet: %v", err)
	}
	// The reference writes prompt_blocked inside the generator's transaction,
	// which rolls back with the rejection, so no event row survives.
	if got := countRiskEvents(t, handle); got != 0 {
		t.Fatalf("prompt rejection wrote %d risk events, want 0", got)
	}
}

func TestTaskRateLimit(t *testing.T) {
	service, _ := newTestService(t, &config.Config{
		RiskLoginFailLimit:   5,
		RiskLoginLockMinutes: 15,
		RiskTaskWindow:       60 * time.Second,
		RiskTaskLimit:        2,
	})
	ctx := context.Background()

	if err := service.ConsumeTaskQuota(ctx, 1); err != nil {
		t.Fatalf("first submission: %v", err)
	}
	if err := service.ConsumeTaskQuota(ctx, 1); err != nil {
		t.Fatalf("second submission: %v", err)
	}
	err := service.ConsumeTaskQuota(ctx, 1)
	if err == nil || err.Error() != "提交过于频繁，请稍后再试" {
		t.Fatalf("over-limit error = %v, want 提交过于频繁，请稍后再试", err)
	}
	if err := service.ConsumeTaskQuota(ctx, 1); err == nil {
		t.Fatal("staying over the limit must keep failing")
	}
	if err := service.ConsumeTaskQuota(ctx, 2); err != nil {
		t.Fatalf("another user has its own window: %v", err)
	}
}

func TestRecordLoginFailureWritesEvent(t *testing.T) {
	service, handle := newTestService(t, nil)
	ctx := context.Background()

	if got := service.RecordLoginFailure(ctx, "diff-user", "198.51.100.8"); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
	var (
		eventType string
		requestIP string
		detail    string
		userID    sql.NullInt64
	)
	if err := handle.QueryRow(
		`SELECT event_type, request_ip, detail_json, user_id FROM risk_riskevent ORDER BY id LIMIT 1`,
	).Scan(&eventType, &requestIP, &detail, &userID); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if eventType != "login_failure" || requestIP != "198.51.100.8" {
		t.Fatalf("event = (%s, %s), want (login_failure, 198.51.100.8)", eventType, requestIP)
	}
	if detail != `{"username": "diff-user", "attempts": 1}` {
		t.Fatalf("detail_json = %s", detail)
	}
	if userID.Valid {
		t.Fatalf("login failures must keep user_id NULL, got %d", userID.Int64)
	}

	service.RecordLoginFailure(ctx, "张三", "")
	var nullIP sql.NullString
	if err := handle.QueryRow(
		`SELECT detail_json, request_ip FROM risk_riskevent ORDER BY id DESC LIMIT 1`,
	).Scan(&detail, &nullIP); err != nil {
		t.Fatalf("read second event: %v", err)
	}
	if detail != `{"username": "\u5f20\u4e09", "attempts": 1}` {
		t.Fatalf("detail_json = %s, want python ensure_ascii escaping", detail)
	}
	if nullIP.Valid {
		t.Fatalf("empty ip must store NULL, got %q", nullIP.String)
	}
}

func TestSiteConfigOverridesRiskThresholds(t *testing.T) {
	handle, err := db.Open(filepath.Join(t.TempDir(), "risk.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	insertConfig(t, handle, "risk.login_fail_limit", "1")
	insertConfig(t, handle, "risk.login_lock_minutes", "7")
	insertConfig(t, handle, "risk.task_limit_per_window", "1")
	insertConfig(t, handle, "risk.task_window_seconds", "30")

	service := NewService(handle, siteconfig.New(handle, nil, nil), &config.Config{
		RiskLoginFailLimit:   5,
		RiskLoginLockMinutes: 15,
		RiskTaskWindow:       60 * time.Second,
		RiskTaskLimit:        10,
	}, nil)
	ctx := context.Background()

	service.RecordLoginFailure(ctx, "diff-user", "198.51.100.7")
	err = service.EnsureLoginAllowed(ctx, "diff-user", "198.51.100.7")
	if err == nil || !strings.Contains(err.Error(), "请 7 分钟后再试") {
		t.Fatalf("lockout message must use the configured minutes: %v", err)
	}
	if err := service.ConsumeTaskQuota(ctx, 3); err != nil {
		t.Fatalf("first task inside the configured limit: %v", err)
	}
	if err := service.ConsumeTaskQuota(ctx, 3); err == nil {
		t.Fatal("configured task limit of 1 must reject the second submission")
	}
}
