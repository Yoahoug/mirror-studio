// Package quota owns the free-quota account and its ledger. Every mutation
// runs inside a BEGIN IMMEDIATE transaction so concurrent submissions cannot
// over-issue, which is the rewrite's answer to the reference's missing
// select_for_update on SQLite (review P1-14).
package quota

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"mirror-studio/internal/config"
	"mirror-studio/internal/db"
	"mirror-studio/internal/siteconfig"
)

// Account mirrors a billing_quotaaccount row.
type Account struct {
	ID                  int64
	UserID              int64
	DailyQuota          int
	AvailableQuota      int
	FrozenQuota         int
	TotalGeneratedCount int
	LastResetDate       string
}

// Service reads and mutates quota accounts.
type Service struct {
	db       *db.DB
	cfg      *config.Config
	site     *siteconfig.Service
	logger   *slog.Logger
	location *time.Location
}

// NewService wires a quota service onto the shared database and configuration.
func NewService(handle *db.DB, cfg *config.Config, site *siteconfig.Service, log *slog.Logger) *Service {
	return &Service{db: handle, cfg: cfg, site: site, logger: log, location: resolveLocation(cfg, log)}
}

// Summary returns the five quota fields every account endpoint exposes.
func (s *Service) Summary(ctx context.Context, userID int64) (map[string]any, error) {
	account, err := s.Ensure(ctx, userID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"daily_quota":           account.DailyQuota,
		"available_quota":       account.AvailableQuota,
		"frozen_quota":          account.FrozenQuota,
		"total_generated_count": account.TotalGeneratedCount,
		"last_reset_date":       account.LastResetDate,
	}, nil
}

// Ensure loads the account, creating it when missing and applying the daily
// reset when the stored date is stale.
func (s *Service) Ensure(ctx context.Context, userID int64) (*Account, error) {
	defaultDaily := s.defaultDailyQuota()
	var account *Account
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		loaded, err := s.accountInTx(tx, userID, defaultDaily)
		if err != nil {
			return err
		}
		account = loaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

// Refresh is Ensure: the reference refreshes on every account read.
func (s *Service) Refresh(ctx context.Context, userID int64) (*Account, error) {
	return s.Ensure(ctx, userID)
}

// Freeze moves amount from the available balance into the frozen balance for a
// submission, failing with the reference's message when the balance is short.
func (s *Service) Freeze(ctx context.Context, userID int64, amount int, taskNo string) (*Account, error) {
	defaultDaily := s.defaultDailyQuota()
	var account *Account
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		loaded, err := s.accountInTx(tx, userID, defaultDaily)
		if err != nil {
			return err
		}
		if loaded.AvailableQuota < amount {
			return errors.New("今日免费次数已用完")
		}
		beforeAvailable := loaded.AvailableQuota
		beforeFrozen := loaded.FrozenQuota
		loaded.AvailableQuota -= amount
		loaded.FrozenQuota += amount
		if _, err := tx.Exec(
			`UPDATE billing_quotaaccount SET available_quota = ?, frozen_quota = ?, updated_at = ? WHERE id = ?`,
			loaded.AvailableQuota, loaded.FrozenQuota, db.FormatTime(time.Now()), loaded.ID,
		); err != nil {
			return err
		}
		if err := insertLedger(tx, userID, ledgerRow{
			changeType:      "freeze",
			direction:       "out",
			amount:          amount,
			beforeAvailable: beforeAvailable,
			afterAvailable:  loaded.AvailableQuota,
			beforeFrozen:    beforeFrozen,
			afterFrozen:     loaded.FrozenQuota,
			relatedTaskNo:   taskNo,
			remark:          "任务提交冻结次数",
		}); err != nil {
			return err
		}
		account = loaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

// Consume settles a frozen amount once a task succeeded.
func (s *Service) Consume(ctx context.Context, userID int64, amount int, taskNo string) error {
	defaultDaily := s.defaultDailyQuota()
	return s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		account, err := s.accountInTx(tx, userID, defaultDaily)
		if err != nil {
			return err
		}
		if account.FrozenQuota < amount {
			return errors.New("冻结次数不足")
		}
		beforeAvailable := account.AvailableQuota
		beforeFrozen := account.FrozenQuota
		account.FrozenQuota -= amount
		account.TotalGeneratedCount += amount
		if _, err := tx.Exec(
			`UPDATE billing_quotaaccount SET frozen_quota = ?, total_generated_count = ?, updated_at = ? WHERE id = ?`,
			account.FrozenQuota, account.TotalGeneratedCount, db.FormatTime(time.Now()), account.ID,
		); err != nil {
			return err
		}
		return insertLedger(tx, userID, ledgerRow{
			changeType:      "consume",
			direction:       "out",
			amount:          amount,
			beforeAvailable: beforeAvailable,
			afterAvailable:  account.AvailableQuota,
			beforeFrozen:    beforeFrozen,
			afterFrozen:     account.FrozenQuota,
			relatedTaskNo:   taskNo,
			remark:          "任务成功扣减次数",
		})
	})
}

// Release returns a frozen amount after a task failed or was canceled. It
// clamps to the frozen balance and writes nothing when there is none left,
// matching the reference.
func (s *Service) Release(ctx context.Context, userID int64, amount int, taskNo string) error {
	return s.ReleaseWithRemark(ctx, userID, amount, taskNo, "任务失败退回")
}

// ReleaseWithRemark is Release with the caller's own ledger remark: the
// reference's cancel and delete paths write 用户取消任务 and 用户删除运行中任务.
// It exists as a separate method because §11's Release signature has no remark
// parameter.
func (s *Service) ReleaseWithRemark(ctx context.Context, userID int64, amount int, taskNo, remark string) error {
	defaultDaily := s.defaultDailyQuota()
	return s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		account, err := s.accountInTx(tx, userID, defaultDaily)
		if err != nil {
			return err
		}
		if account.FrozenQuota < amount {
			amount = account.FrozenQuota
		}
		if amount <= 0 {
			return nil
		}
		beforeAvailable := account.AvailableQuota
		beforeFrozen := account.FrozenQuota
		account.FrozenQuota -= amount
		account.AvailableQuota += amount
		if _, err := tx.Exec(
			`UPDATE billing_quotaaccount SET available_quota = ?, frozen_quota = ?, updated_at = ? WHERE id = ?`,
			account.AvailableQuota, account.FrozenQuota, db.FormatTime(time.Now()), account.ID,
		); err != nil {
			return err
		}
		return insertLedger(tx, userID, ledgerRow{
			changeType:      "unfreeze",
			direction:       "in",
			amount:          amount,
			beforeAvailable: beforeAvailable,
			afterAvailable:  account.AvailableQuota,
			beforeFrozen:    beforeFrozen,
			afterFrozen:     account.FrozenQuota,
			relatedTaskNo:   taskNo,
			remark:          remark,
		})
	})
}

// AdminAdjust applies the console's quota edit. Both values are absolute, and
// the ledger keeps the reference's row order and remarks.
func (s *Service) AdminAdjust(ctx context.Context, userID int64, dailyQuota, availableQuota *int, remark string) (*Account, error) {
	defaultDaily := s.defaultDailyQuota()
	var account *Account
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		loaded, err := s.accountInTx(tx, userID, defaultDaily)
		if err != nil {
			return err
		}
		beforeAvailable := loaded.AvailableQuota
		beforeFrozen := loaded.FrozenQuota
		changed := false

		if dailyQuota != nil {
			if *dailyQuota < 0 {
				return errors.New("每日免费次数不能小于 0")
			}
			ledgerRemark := remark
			if ledgerRemark == "" {
				ledgerRemark = fmt.Sprintf("每日免费次数调整为 %d", *dailyQuota)
			}
			loaded.DailyQuota = *dailyQuota
			changed = true
			if err := insertLedger(tx, userID, ledgerRow{
				changeType:      "daily_quota_update",
				direction:       "none",
				amount:          *dailyQuota,
				beforeAvailable: beforeAvailable,
				afterAvailable:  loaded.AvailableQuota,
				beforeFrozen:    beforeFrozen,
				afterFrozen:     loaded.FrozenQuota,
				remark:          ledgerRemark,
			}); err != nil {
				return err
			}
		}

		if availableQuota != nil {
			if *availableQuota < 0 {
				return errors.New("当前剩余次数不能小于 0")
			}
			delta := *availableQuota - loaded.AvailableQuota
			loaded.AvailableQuota = *availableQuota
			changed = true
			if delta != 0 {
				direction := "out"
				ledgerRemark := remark
				if delta > 0 {
					direction = "in"
				}
				if ledgerRemark == "" {
					ledgerRemark = "管理员直接设置当前剩余次数"
				}
				if err := insertLedger(tx, userID, ledgerRow{
					changeType:      "admin_adjust",
					direction:       direction,
					amount:          abs(delta),
					beforeAvailable: beforeAvailable,
					afterAvailable:  loaded.AvailableQuota,
					beforeFrozen:    beforeFrozen,
					afterFrozen:     loaded.FrozenQuota,
					remark:          ledgerRemark,
				}); err != nil {
					return err
				}
			}
		}

		if changed {
			if _, err := tx.Exec(
				`UPDATE billing_quotaaccount SET daily_quota = ?, available_quota = ?, updated_at = ? WHERE id = ?`,
				loaded.DailyQuota, loaded.AvailableQuota, db.FormatTime(time.Now()), loaded.ID,
			); err != nil {
				return err
			}
		}
		account = loaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

// PendingTaskLimit caps queued plus running tasks per user.
func (s *Service) PendingTaskLimit(ctx context.Context) int {
	return s.site.Int("site.max_pending_tasks", s.cfg.MaxPendingTasks)
}

type ledgerRow struct {
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

func (s *Service) accountInTx(tx *db.Tx, userID int64, defaultDaily int) (*Account, error) {
	account, err := selectAccount(tx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return s.createAccount(tx, userID, defaultDaily)
	}
	if err != nil {
		return nil, err
	}
	if account.DailyQuota < 0 {
		if _, err := tx.Exec(
			`UPDATE billing_quotaaccount SET daily_quota = 0, updated_at = ? WHERE id = ?`,
			db.FormatTime(time.Now()), account.ID,
		); err != nil {
			return nil, err
		}
		account.DailyQuota = 0
	}
	return s.refreshInTx(tx, account)
}

func selectAccount(tx *db.Tx, userID int64) (*Account, error) {
	var account Account
	// last_reset_date is a DATE column, so the driver hands back a time.Time; the
	// contract (and the reference's `date.isoformat()`) is the bare calendar day.
	var lastReset time.Time
	err := tx.QueryRow(
		`SELECT id, user_id, daily_quota, available_quota, frozen_quota, total_generated_count, last_reset_date
		 FROM billing_quotaaccount WHERE user_id = ?`, userID,
	).Scan(
		&account.ID, &account.UserID, &account.DailyQuota, &account.AvailableQuota,
		&account.FrozenQuota, &account.TotalGeneratedCount, &lastReset,
	)
	if err != nil {
		return nil, err
	}
	account.LastResetDate = db.FormatDate(lastReset)
	return &account, nil
}

func (s *Service) createAccount(tx *db.Tx, userID int64, defaultDaily int) (*Account, error) {
	now := db.FormatTime(time.Now())
	today := s.localDate()
	result, err := tx.Exec(
		`INSERT INTO billing_quotaaccount
		 (created_at, updated_at, daily_quota, available_quota, frozen_quota, total_generated_count, user_id, last_reset_date)
		 VALUES (?, ?, ?, ?, 0, 0, ?, ?)`,
		now, now, defaultDaily, defaultDaily, userID, today,
	)
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Account{
		ID:             id,
		UserID:         userID,
		DailyQuota:     defaultDaily,
		AvailableQuota: defaultDaily,
		LastResetDate:  today,
	}, nil
}

func (s *Service) refreshInTx(tx *db.Tx, account *Account) (*Account, error) {
	today := s.localDate()
	if account.LastResetDate >= today {
		return account, nil
	}
	beforeAvailable := account.AvailableQuota
	beforeFrozen := account.FrozenQuota
	account.AvailableQuota = account.DailyQuota
	account.LastResetDate = today
	if _, err := tx.Exec(
		`UPDATE billing_quotaaccount SET available_quota = ?, last_reset_date = ?, updated_at = ? WHERE id = ?`,
		account.AvailableQuota, account.LastResetDate, db.FormatTime(time.Now()), account.ID,
	); err != nil {
		return nil, err
	}
	if err := insertLedger(tx, account.UserID, ledgerRow{
		changeType:      "reset",
		direction:       "none",
		amount:          account.DailyQuota,
		beforeAvailable: beforeAvailable,
		afterAvailable:  account.AvailableQuota,
		beforeFrozen:    beforeFrozen,
		afterFrozen:     account.FrozenQuota,
		remark:          fmt.Sprintf("每日免费次数刷新为 %d", account.DailyQuota),
	}); err != nil {
		return nil, err
	}
	return account, nil
}

func insertLedger(tx *db.Tx, userID int64, row ledgerRow) error {
	now := db.FormatTime(time.Now())
	for attempt := 0; ; attempt++ {
		_, err := tx.Exec(
			`INSERT INTO billing_quotaledger
			 (created_at, updated_at, ledger_no, change_type, direction, amount,
			  before_available, after_available, before_frozen, after_frozen, related_task_no, remark, user_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			now, now, generateLedgerNo(), row.changeType, row.direction, row.amount,
			row.beforeAvailable, row.afterAvailable, row.beforeFrozen, row.afterFrozen,
			row.relatedTaskNo, row.remark, userID,
		)
		if err == nil {
			return nil
		}
		if attempt == 0 && isUniqueViolation(err) {
			continue
		}
		return err
	}
}

func generateLedgerNo() string {
	prefix := "L" + time.Now().UTC().Format("20060102150405")
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return prefix + fmt.Sprintf("%08X", uint32(time.Now().UnixNano()))
	}
	return prefix + strings.ToUpper(hex.EncodeToString(suffix))
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *Service) defaultDailyQuota() int {
	value := s.site.Int("site.default_daily_free_quota", s.cfg.DefaultDailyFreeQuota)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Service) localDate() string {
	return time.Now().In(s.location).Format("2006-01-02")
}

func resolveLocation(cfg *config.Config, log *slog.Logger) *time.Location {
	if cfg != nil {
		if name := strings.TrimSpace(cfg.TimeZone); name != "" {
			if location, err := time.LoadLocation(name); err == nil {
				return location
			} else if log != nil {
				log.Warn("unknown TIME_ZONE, falling back to local time", "time_zone", name, "error", err)
			}
		}
	}
	return time.Local
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
