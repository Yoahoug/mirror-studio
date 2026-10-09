package quota

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"mirror-studio/internal/config"
	"mirror-studio/internal/db"
	"mirror-studio/internal/siteconfig"
)

func newTestService(t *testing.T, cfg *config.Config) (*Service, *db.DB) {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "quota.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if cfg == nil {
		cfg = &config.Config{
			DefaultDailyFreeQuota: 20,
			MaxPendingTasks:       3,
			TimeZone:              "UTC",
		}
	}
	site := siteconfig.New(handle, nil, nil)
	return NewService(handle, cfg, site, nil), handle
}

func seedUser(t *testing.T, handle *db.DB, username string) int64 {
	t.Helper()
	result, err := handle.Exec(
		`INSERT INTO users_user (password, is_superuser, username, first_name, last_name, is_staff, is_active, date_joined, role, status)
		 VALUES ('x', 0, ?, '', '', 0, 1, ?, 'user', 'active')`,
		username, db.FormatTime(time.Now()),
	)
	if err != nil {
		t.Fatalf("seed user %s: %v", username, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("user id: %v", err)
	}
	return id
}

func insertConfigRow(t *testing.T, handle *db.DB, key, value string) {
	t.Helper()
	now := db.FormatTime(time.Now())
	if _, err := handle.Exec(
		`INSERT INTO core_siteconfig (created_at, updated_at, key, value_encrypted, is_secret, description, category)
		 VALUES (?, ?, ?, ?, 0, '', 'site')`,
		now, now, key, value,
	); err != nil {
		t.Fatalf("insert siteconfig %s: %v", key, err)
	}
}

type accountSnapshot struct {
	daily     int
	available int
	frozen    int
	total     int
	resetDate string
}

func readAccount(t *testing.T, handle *db.DB, userID int64) accountSnapshot {
	t.Helper()
	var snapshot accountSnapshot
	if err := handle.QueryRow(
		`SELECT daily_quota, available_quota, frozen_quota, total_generated_count, last_reset_date
		 FROM billing_quotaaccount WHERE user_id = ?`, userID,
	).Scan(&snapshot.daily, &snapshot.available, &snapshot.frozen, &snapshot.total, &snapshot.resetDate); err != nil {
		t.Fatalf("read account: %v", err)
	}
	return snapshot
}

type ledgerSnapshot struct {
	ledgerNo        string
	changeType      string
	direction       string
	amount          int
	beforeAvailable int
	afterAvailable  int
	beforeFrozen    int
	afterFrozen     int
	relatedTaskNo   string
	remark          string
}

func readLedger(t *testing.T, handle *db.DB, userID int64) []ledgerSnapshot {
	t.Helper()
	rows, err := handle.Query(
		`SELECT ledger_no, change_type, direction, amount, before_available, after_available,
		        before_frozen, after_frozen, related_task_no, remark
		 FROM billing_quotaledger WHERE user_id = ? ORDER BY id`, userID,
	)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	defer rows.Close()
	entries := []ledgerSnapshot{}
	for rows.Next() {
		var entry ledgerSnapshot
		if err := rows.Scan(
			&entry.ledgerNo, &entry.changeType, &entry.direction, &entry.amount,
			&entry.beforeAvailable, &entry.afterAvailable, &entry.beforeFrozen, &entry.afterFrozen,
			&entry.relatedTaskNo, &entry.remark,
		); err != nil {
			t.Fatalf("scan ledger: %v", err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func intPtr(value int) *int { return &value }

func TestEnsureCreatesAccountWithDefaults(t *testing.T) {
	service, handle := newTestService(t, nil)
	userID := seedUser(t, handle, "creator")
	ctx := context.Background()

	summary, err := service.Summary(ctx, userID)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if len(summary) != 5 {
		t.Fatalf("summary keys = %v, want exactly the five quota fields", summary)
	}
	if summary["daily_quota"] != 20 || summary["available_quota"] != 20 ||
		summary["frozen_quota"] != 0 || summary["total_generated_count"] != 0 {
		t.Fatalf("summary = %v, want 20/20/0/0", summary)
	}
	today := time.Now().In(time.UTC).Format("2006-01-02")
	if summary["last_reset_date"] != today {
		t.Fatalf("last_reset_date = %v, want %s", summary["last_reset_date"], today)
	}
	if entries := readLedger(t, handle, userID); len(entries) != 0 {
		t.Fatalf("account creation must not write a ledger row, got %d", len(entries))
	}

	again, err := service.Ensure(ctx, userID)
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if again.ID == 0 || again.UserID != userID {
		t.Fatalf("ensure returned %+v", again)
	}
	var accounts int
	if err := handle.QueryRow(`SELECT COUNT(1) FROM billing_quotaaccount WHERE user_id = ?`, userID).Scan(&accounts); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if accounts != 1 {
		t.Fatalf("accounts = %d, want 1", accounts)
	}
}

func TestDefaultDailyQuotaComesFromSiteConfig(t *testing.T) {
	handle, err := db.Open(filepath.Join(t.TempDir(), "quota.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	insertConfigRow(t, handle, "site.default_daily_free_quota", "25")
	userID := seedUser(t, handle, "creator")

	service := NewService(handle, &config.Config{DefaultDailyFreeQuota: 20, MaxPendingTasks: 3, TimeZone: "UTC"}, siteconfig.New(handle, nil, nil), nil)
	if _, err := service.Ensure(context.Background(), userID); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	snapshot := readAccount(t, handle, userID)
	if snapshot.daily != 25 || snapshot.available != 25 {
		t.Fatalf("account = %+v, want the site config quota of 25", snapshot)
	}
}

func TestDefaultDailyQuotaIsClampedAtZero(t *testing.T) {
	handle, err := db.Open(filepath.Join(t.TempDir(), "quota.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	insertConfigRow(t, handle, "site.default_daily_free_quota", "-5")
	userID := seedUser(t, handle, "creator")

	service := NewService(handle, &config.Config{DefaultDailyFreeQuota: 20, MaxPendingTasks: 3, TimeZone: "UTC"}, siteconfig.New(handle, nil, nil), nil)
	if _, err := service.Ensure(context.Background(), userID); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	snapshot := readAccount(t, handle, userID)
	if snapshot.daily != 0 || snapshot.available != 0 {
		t.Fatalf("account = %+v, want a clamped 0/0", snapshot)
	}
}

func TestRefreshResetsOnNewDay(t *testing.T) {
	service, handle := newTestService(t, nil)
	userID := seedUser(t, handle, "creator")
	ctx := context.Background()
	if _, err := service.Ensure(ctx, userID); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	yesterday := time.Now().In(time.UTC).AddDate(0, 0, -1).Format("2006-01-02")
	if _, err := handle.Exec(
		`UPDATE billing_quotaaccount SET available_quota = 3, frozen_quota = 1, last_reset_date = ? WHERE user_id = ?`,
		yesterday, userID,
	); err != nil {
		t.Fatalf("stage stale account: %v", err)
	}

	account, err := service.Refresh(ctx, userID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	today := time.Now().In(time.UTC).Format("2006-01-02")
	if account.AvailableQuota != 20 || account.DailyQuota != 20 || account.LastResetDate != today {
		t.Fatalf("refreshed account = %+v, want available 20 on %s", account, today)
	}
	if account.FrozenQuota != 1 {
		t.Fatalf("frozen quota = %d, the reference refresh leaves it untouched", account.FrozenQuota)
	}
	entries := readLedger(t, handle, userID)
	if len(entries) != 1 {
		t.Fatalf("ledger rows = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.changeType != "reset" || entry.direction != "none" || entry.amount != 20 {
		t.Fatalf("reset entry = %+v, want reset/none/20", entry)
	}
	if entry.beforeAvailable != 3 || entry.afterAvailable != 20 || entry.beforeFrozen != 1 || entry.afterFrozen != 1 {
		t.Fatalf("reset balances = %+v", entry)
	}
	if entry.remark != "每日免费次数刷新为 20" || entry.relatedTaskNo != "" {
		t.Fatalf("reset remark = %q, want 每日免费次数刷新为 20", entry.remark)
	}

	if _, err := service.Refresh(ctx, userID); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if entries := readLedger(t, handle, userID); len(entries) != 1 {
		t.Fatalf("same-day refresh must not write another row, got %d", len(entries))
	}
}

// 来源: apps/generator/tests.py::GeneratorFlowTests::test_generate_success_consumes_frozen_quota_and_persists_image
func TestFreezeConsumeFromOneAvailable(t *testing.T) {
	service, handle := newTestService(t, nil)
	userID := seedUser(t, handle, "creator")
	ctx := context.Background()
	if _, err := service.Ensure(ctx, userID); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := handle.Exec(`UPDATE billing_quotaaccount SET available_quota = 1 WHERE user_id = ?`, userID); err != nil {
		t.Fatalf("stage account: %v", err)
	}

	account, err := service.Freeze(ctx, userID, 1, "G1")
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if account.AvailableQuota != 0 || account.FrozenQuota != 1 {
		t.Fatalf("after freeze = %+v, want 0 available and 1 frozen", account)
	}
	if err := service.Consume(ctx, userID, 1, "G1"); err != nil {
		t.Fatalf("consume: %v", err)
	}
	snapshot := readAccount(t, handle, userID)
	if snapshot.available != 0 || snapshot.frozen != 0 || snapshot.total != 1 {
		t.Fatalf("after consume = %+v, want 0/0/1", snapshot)
	}

	entries := readLedger(t, handle, userID)
	if len(entries) != 2 {
		t.Fatalf("ledger rows = %d, want 2", len(entries))
	}
	freeze := entries[0]
	if freeze.changeType != "freeze" || freeze.direction != "out" || freeze.amount != 1 ||
		freeze.beforeAvailable != 1 || freeze.afterAvailable != 0 ||
		freeze.beforeFrozen != 0 || freeze.afterFrozen != 1 ||
		freeze.relatedTaskNo != "G1" || freeze.remark != "任务提交冻结次数" {
		t.Fatalf("freeze entry = %+v", freeze)
	}
	consume := entries[1]
	if consume.changeType != "consume" || consume.direction != "out" || consume.amount != 1 ||
		consume.beforeAvailable != 0 || consume.afterAvailable != 0 ||
		consume.beforeFrozen != 1 || consume.afterFrozen != 0 ||
		consume.relatedTaskNo != "G1" || consume.remark != "任务成功扣减次数" {
		t.Fatalf("consume entry = %+v", consume)
	}
}

func TestFreezeFailsWhenAvailableIsExhausted(t *testing.T) {
	service, handle := newTestService(t, nil)
	userID := seedUser(t, handle, "creator")
	ctx := context.Background()
	if _, err := service.Ensure(ctx, userID); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := handle.Exec(`UPDATE billing_quotaaccount SET available_quota = 0 WHERE user_id = ?`, userID); err != nil {
		t.Fatalf("stage account: %v", err)
	}

	_, err := service.Freeze(ctx, userID, 1, "G1")
	if err == nil || err.Error() != "今日免费次数已用完" {
		t.Fatalf("freeze error = %v, want 今日免费次数已用完", err)
	}
	snapshot := readAccount(t, handle, userID)
	if snapshot.available != 0 || snapshot.frozen != 0 {
		t.Fatalf("failed freeze changed the account: %+v", snapshot)
	}
	if entries := readLedger(t, handle, userID); len(entries) != 0 {
		t.Fatalf("failed freeze wrote %d ledger rows", len(entries))
	}
}

// 来源: apps/generator/tests.py::GeneratorFlowTests::test_generate_failure_releases_frozen_quota
func TestReleaseReturnsFrozenQuota(t *testing.T) {
	service, handle := newTestService(t, nil)
	userID := seedUser(t, handle, "creator")
	ctx := context.Background()
	if _, err := service.Ensure(ctx, userID); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := handle.Exec(`UPDATE billing_quotaaccount SET available_quota = 1 WHERE user_id = ?`, userID); err != nil {
		t.Fatalf("stage account: %v", err)
	}

	if _, err := service.Freeze(ctx, userID, 1, "G1"); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if err := service.Release(ctx, userID, 1, "G1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	snapshot := readAccount(t, handle, userID)
	if snapshot.available != 1 || snapshot.frozen != 0 {
		t.Fatalf("after release = %+v, want a restored balance of 1", snapshot)
	}
	entries := readLedger(t, handle, userID)
	unfreeze := entries[len(entries)-1]
	if unfreeze.changeType != "unfreeze" || unfreeze.direction != "in" || unfreeze.amount != 1 ||
		unfreeze.beforeFrozen != 1 || unfreeze.afterFrozen != 0 || unfreeze.remark != "任务失败退回" {
		t.Fatalf("unfreeze entry = %+v", unfreeze)
	}

	// Releasing more than is frozen clamps to the frozen balance; with nothing
	// frozen it writes no row at all.
	if err := service.Release(ctx, userID, 5, "G2"); err != nil {
		t.Fatalf("release with nothing frozen: %v", err)
	}
	if snapshot := readAccount(t, handle, userID); snapshot.available != 1 || snapshot.frozen != 0 {
		t.Fatalf("release with nothing frozen changed the account: %+v", snapshot)
	}
	if entries := readLedger(t, handle, userID); len(entries) != 2 {
		t.Fatalf("ledger rows = %d, want 2 (release with no frozen balance is a no-op)", len(entries))
	}

	// The cancel and delete paths carry their own remark in the reference.
	if _, err := service.Freeze(ctx, userID, 1, "G3"); err != nil {
		t.Fatalf("freeze G3: %v", err)
	}
	if err := service.ReleaseWithRemark(ctx, userID, 1, "G3", "用户取消任务"); err != nil {
		t.Fatalf("release with remark: %v", err)
	}
	entries = readLedger(t, handle, userID)
	if entry := entries[len(entries)-1]; entry.changeType != "unfreeze" || entry.remark != "用户取消任务" {
		t.Fatalf("cancel unfreeze entry = %+v", entry)
	}
	if snapshot := readAccount(t, handle, userID); snapshot.available != 1 || snapshot.frozen != 0 {
		t.Fatalf("balance after cancel release = %+v, want 1 available", snapshot)
	}
}

// 来源: go-architecture §10 并发用例（额度冻结不得超发）
func TestConcurrentFreezeNeverOverIssues(t *testing.T) {
	service, handle := newTestService(t, nil)
	userID := seedUser(t, handle, "creator")
	ctx := context.Background()
	if _, err := service.Ensure(ctx, userID); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	const available = 15
	if _, err := handle.Exec(`UPDATE billing_quotaaccount SET available_quota = ? WHERE user_id = ?`, available, userID); err != nil {
		t.Fatalf("stage account: %v", err)
	}

	const attempts = available + 1
	var (
		wait     sync.WaitGroup
		mu       sync.Mutex
		failures []string
	)
	for index := 0; index < attempts; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, err := service.Freeze(ctx, userID, 1, fmt.Sprintf("G%d", index))
			if err != nil {
				mu.Lock()
				failures = append(failures, err.Error())
				mu.Unlock()
			}
		}(index)
	}
	wait.Wait()

	if len(failures) != attempts-available {
		t.Fatalf("failures = %d (%v), want exactly %d", len(failures), failures, attempts-available)
	}
	for _, failure := range failures {
		if failure != "今日免费次数已用完" {
			t.Fatalf("unexpected freeze failure: %s", failure)
		}
	}
	snapshot := readAccount(t, handle, userID)
	if snapshot.available != 0 || snapshot.frozen != available {
		t.Fatalf("account after concurrent freeze = %+v, want 0 available and %d frozen", snapshot, available)
	}
	entries := readLedger(t, handle, userID)
	if len(entries) != available {
		t.Fatalf("freeze ledger rows = %d, want %d", len(entries), available)
	}
	for _, entry := range entries {
		if entry.availableBeforeOrAfterNegative() {
			t.Fatalf("ledger entry went negative: %+v", entry)
		}
	}
}

func (entry ledgerSnapshot) availableBeforeOrAfterNegative() bool {
	return entry.beforeAvailable < 0 || entry.afterAvailable < 0
}

func TestAdminAdjustValidation(t *testing.T) {
	service, handle := newTestService(t, nil)
	userID := seedUser(t, handle, "creator")
	ctx := context.Background()
	if _, err := service.Ensure(ctx, userID); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	if _, err := service.AdminAdjust(ctx, userID, nil, intPtr(-1), "后台修改免费次数"); err == nil || err.Error() != "当前剩余次数不能小于 0" {
		t.Fatalf("negative available error = %v, want 当前剩余次数不能小于 0", err)
	}
	if _, err := service.AdminAdjust(ctx, userID, intPtr(-1), nil, "后台修改免费次数"); err == nil || err.Error() != "每日免费次数不能小于 0" {
		t.Fatalf("negative daily error = %v, want 每日免费次数不能小于 0", err)
	}
	snapshot := readAccount(t, handle, userID)
	if snapshot.daily != 20 || snapshot.available != 20 {
		t.Fatalf("rejected adjust changed the account: %+v", snapshot)
	}
	if entries := readLedger(t, handle, userID); len(entries) != 0 {
		t.Fatalf("rejected adjust wrote %d ledger rows", len(entries))
	}
}

func TestAdminAdjustAppliesValues(t *testing.T) {
	service, handle := newTestService(t, nil)
	userID := seedUser(t, handle, "creator")
	ctx := context.Background()
	if _, err := service.Ensure(ctx, userID); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	account, err := service.AdminAdjust(ctx, userID, intPtr(9), intPtr(7), "")
	if err != nil {
		t.Fatalf("adjust: %v", err)
	}
	if account.DailyQuota != 9 || account.AvailableQuota != 7 {
		t.Fatalf("adjusted account = %+v, want daily 9 and available 7", account)
	}
	entries := readLedger(t, handle, userID)
	if len(entries) != 2 {
		t.Fatalf("ledger rows = %d, want 2", len(entries))
	}
	daily := entries[0]
	if daily.changeType != "daily_quota_update" || daily.direction != "none" || daily.amount != 9 ||
		daily.beforeAvailable != 20 || daily.afterAvailable != 20 || daily.remark != "每日免费次数调整为 9" {
		t.Fatalf("daily_quota_update entry = %+v", daily)
	}
	adjust := entries[1]
	if adjust.changeType != "admin_adjust" || adjust.direction != "out" || adjust.amount != 13 ||
		adjust.beforeAvailable != 20 || adjust.afterAvailable != 7 || adjust.remark != "管理员直接设置当前剩余次数" {
		t.Fatalf("admin_adjust entry = %+v", adjust)
	}

	if _, err := service.AdminAdjust(ctx, userID, nil, intPtr(10), "[管理员:admin] 后台修改免费次数"); err != nil {
		t.Fatalf("second adjust: %v", err)
	}
	entries = readLedger(t, handle, userID)
	increase := entries[len(entries)-1]
	if increase.changeType != "admin_adjust" || increase.direction != "in" || increase.amount != 3 ||
		increase.remark != "[管理员:admin] 后台修改免费次数" {
		t.Fatalf("increase entry = %+v", increase)
	}

	if _, err := service.AdminAdjust(ctx, userID, nil, nil, ""); err != nil {
		t.Fatalf("empty adjust: %v", err)
	}
	if got := len(readLedger(t, handle, userID)); got != 3 {
		t.Fatalf("empty adjust wrote a ledger row, count = %d", got)
	}
}

func TestGenerateLedgerNoFormat(t *testing.T) {
	pattern := regexp.MustCompile(`^L\d{14}[0-9A-F]{8}$`)
	seen := map[string]bool{}
	for index := 0; index < 64; index++ {
		ledgerNo := generateLedgerNo()
		if !pattern.MatchString(ledgerNo) {
			t.Fatalf("ledger number %q does not match L<14 digits><8 uppercase hex>", ledgerNo)
		}
		seen[ledgerNo] = true
	}
	if len(seen) != 64 {
		t.Fatalf("ledger numbers collided: %d unique of 64", len(seen))
	}
}

func TestPendingTaskLimit(t *testing.T) {
	service, _ := newTestService(t, nil)
	if got := service.PendingTaskLimit(context.Background()); got != 3 {
		t.Fatalf("pending limit = %d, want the config value 3", got)
	}

	handle, err := db.Open(filepath.Join(t.TempDir(), "quota.sqlite3"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { handle.Close() })
	if err := handle.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	insertConfigRow(t, handle, "site.max_pending_tasks", "5")
	configured := NewService(handle, &config.Config{DefaultDailyFreeQuota: 20, MaxPendingTasks: 3, TimeZone: "UTC"}, siteconfig.New(handle, nil, nil), nil)
	if got := configured.PendingTaskLimit(context.Background()); got != 5 {
		t.Fatalf("pending limit = %d, want the site config value 5", got)
	}
}

func TestSummaryMatchesConsoleShape(t *testing.T) {
	service, handle := newTestService(t, nil)
	userID := seedUser(t, handle, "creator")
	summary, err := service.Summary(context.Background(), userID)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	for _, key := range []string{"daily_quota", "available_quota", "frozen_quota", "total_generated_count", "last_reset_date"} {
		if _, ok := summary[key]; !ok {
			t.Fatalf("summary is missing %s", key)
		}
	}
	if _, ok := summary["total_consumed_quota"]; ok {
		t.Fatalf("summary exposes the reference's property alias: %v", summary)
	}
}
