package console

import (
	"context"
	"errors"

	"mirror-studio/internal/quota"
)

var errQuotaNotWired = errors.New("quota service is not wired")

// ensureQuota delegates account creation and the daily roll-over to the quota
// domain, then normalises the DATE column the sqlite driver reports as an
// RFC3339 timestamp.
func (s *Service) ensureQuota(ctx context.Context, userID int64) (*quota.Account, error) {
	if s.quota == nil {
		return nil, errQuotaNotWired
	}
	account, err := s.quota.Ensure(ctx, userID)
	if err != nil {
		return nil, err
	}
	account.LastResetDate = dateOnly(account.LastResetDate)
	return account, nil
}

// adminAdjustQuota delegates the administrator edit to the quota domain.
func (s *Service) adminAdjustQuota(ctx context.Context, userID int64, dailyQuota, availableQuota *int, remark string) (*quota.Account, error) {
	if s.quota == nil {
		return nil, errQuotaNotWired
	}
	account, err := s.quota.AdminAdjust(ctx, userID, dailyQuota, availableQuota, remark)
	if err != nil {
		return nil, err
	}
	account.LastResetDate = dateOnly(account.LastResetDate)
	return account, nil
}

// quotaValidationErrors are the messages the quota domain rejects an adjustment
// with; both map to 400/400002.
var quotaValidationErrors = map[string]bool{
	"每日免费次数不能小于 0": true,
	"当前剩余次数不能小于 0": true,
}
