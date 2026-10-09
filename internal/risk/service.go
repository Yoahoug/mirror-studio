// Package risk implements the reference's risk controls: IP access rules,
// sensitive-word screening, the login failure lockout and the per-user task
// rate limit. All thresholds resolve through siteconfig (database row, then
// environment, then the startup default) so the console's risk configuration
// takes effect, per review A-2.
package risk

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"mirror-studio/internal/config"
	"mirror-studio/internal/db"
	"mirror-studio/internal/siteconfig"
)

const (
	ipBlockedMessage  = "当前 IP 已被限制访问"
	ipCacheTTL        = 60 * time.Second
	sensitiveCacheTTL = 30 * time.Second
)

// Service exposes the risk controls other domains call.
type Service struct {
	db     *db.DB
	site   *siteconfig.Service
	cfg    *config.Config
	logger *slog.Logger

	counters *Counters

	ipMu    sync.RWMutex
	ipRules map[string]ipRule

	rulesMu       sync.RWMutex
	sensitiveList []sensitiveRule
	sensitiveAt   time.Time
}

type ipRule struct {
	blocked bool
	checked time.Time
}

type sensitiveRule struct {
	keyword string
	mode    string
}

// NewService wires a risk service onto the shared database and configuration.
func NewService(handle *db.DB, site *siteconfig.Service, cfg *config.Config, log *slog.Logger) *Service {
	return &Service{
		db:       handle,
		site:     site,
		cfg:      cfg,
		logger:   log,
		counters: NewCounters(),
		ipRules:  map[string]ipRule{},
	}
}

// Counters returns the shared counter set, which the console rate limiter
// reuses through Increment.
func (s *Service) Counters() *Counters { return s.counters }

// IPBlocked reports whether the middleware must reject the request.
func (s *Service) IPBlocked(ctx context.Context, ip string) (bool, string) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return false, ""
	}
	if blocked, ok := s.cachedIPRule(ip); ok {
		return blockedIP(blocked)
	}
	blocked, err := s.lookupIPRule(ctx, ip)
	if err != nil {
		s.logError("risk ip rule lookup failed", "ip", ip, "error", err)
		return false, ""
	}
	s.storeIPRule(ip, blocked)
	return blockedIP(blocked)
}

func blockedIP(blocked bool) (bool, string) {
	if blocked {
		return true, ipBlockedMessage
	}
	return false, ""
}

func (s *Service) cachedIPRule(ip string) (bool, bool) {
	s.ipMu.RLock()
	defer s.ipMu.RUnlock()
	entry, ok := s.ipRules[ip]
	if !ok || time.Since(entry.checked) >= ipCacheTTL {
		return false, false
	}
	return entry.blocked, true
}

func (s *Service) storeIPRule(ip string, blocked bool) {
	s.ipMu.Lock()
	defer s.ipMu.Unlock()
	s.ipRules[ip] = ipRule{blocked: blocked, checked: time.Now()}
}

func (s *Service) lookupIPRule(ctx context.Context, ip string) (bool, error) {
	var (
		isBlocked int
		expires   sql.NullString
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT is_blocked, expires_at FROM risk_ipaccessrule WHERE ip_address = ?`, ip,
	).Scan(&isBlocked, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if isBlocked != 1 {
		return false, nil
	}
	if !expires.Valid || strings.TrimSpace(expires.String) == "" {
		return true, nil
	}
	parsed, ok := db.ParseTime(expires.String)
	if !ok {
		s.logError("risk ip rule has an unparseable expiry", "ip", ip, "expires_at", expires.String)
		return true, nil
	}
	return parsed.After(time.Now().UTC()), nil
}

// EnsureLoginAllowed rejects a login attempt from a blocked IP or a locked
// (username, IP) pair.
func (s *Service) EnsureLoginAllowed(ctx context.Context, username, ip string) error {
	if blocked, message := s.IPBlocked(ctx, ip); blocked {
		return errors.New(message)
	}
	if s.counters.peek(loginKey(username, ip)) >= int64(s.loginFailLimit()) {
		return fmt.Errorf("登录失败次数过多，请 %d 分钟后再试", s.loginLockMinutes())
	}
	return nil
}

// Failures reports the failed attempts recorded for the (username, IP) pair.
func (s *Service) Failures(ctx context.Context, username, ip string) int64 {
	return s.counters.peek(loginKey(username, ip))
}

// RecordLoginFailure bumps the lockout counter and logs the risk event, the
// same way the reference does outside any transaction.
func (s *Service) RecordLoginFailure(ctx context.Context, username, ip string) int64 {
	attempts := s.counters.Increment(loginKey(username, ip), time.Duration(s.loginLockMinutes())*time.Minute)
	detail := fmt.Sprintf(`{"username": %s, "attempts": %d}`, pyQuote(username), attempts)
	if err := s.insertRiskEvent(ctx, "login_failure", ip, detail); err != nil {
		s.logError("risk event write failed", "event_type", "login_failure", "error", err)
	}
	return attempts
}

// ClearLoginFailures drops the counter after a successful login.
func (s *Service) ClearLoginFailures(ctx context.Context, username, ip string) {
	s.counters.reset(loginKey(username, ip))
}

// EnsurePromptSafe rejects an empty prompt and any prompt matching an active
// sensitive-word rule.
func (s *Service) EnsurePromptSafe(ctx context.Context, prompt string) error {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return errors.New("Prompt 不能为空")
	}
	rules, err := s.sensitiveRules(ctx)
	if err != nil {
		s.logError("sensitive word rules load failed", "error", err)
		return err
	}
	for _, rule := range rules {
		matched := trimmed == rule.keyword
		if rule.mode != "exact" {
			matched = strings.Contains(trimmed, rule.keyword)
		}
		if matched {
			return errors.New("Prompt 命中风控规则")
		}
	}
	return nil
}

// ConsumeTaskQuota counts one submission and rejects it once the window limit
// is exceeded; the caller maps the error to HTTP 403 code 400403.
func (s *Service) ConsumeTaskQuota(ctx context.Context, userID int64) error {
	window := time.Duration(s.taskWindowSeconds()) * time.Second
	if s.counters.Increment(taskKey(userID), window) > int64(s.taskLimitPerWindow()) {
		return errors.New("提交过于频繁，请稍后再试")
	}
	return nil
}

func (s *Service) sensitiveRules(ctx context.Context) ([]sensitiveRule, error) {
	s.rulesMu.RLock()
	if s.sensitiveList != nil && time.Since(s.sensitiveAt) < sensitiveCacheTTL {
		list := s.sensitiveList
		s.rulesMu.RUnlock()
		return list, nil
	}
	s.rulesMu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `SELECT keyword, match_mode FROM risk_sensitivewordrule WHERE is_active = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []sensitiveRule{}
	for rows.Next() {
		var rule sensitiveRule
		if err := rows.Scan(&rule.keyword, &rule.mode); err != nil {
			return nil, err
		}
		list = append(list, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	s.rulesMu.Lock()
	s.sensitiveList = list
	s.sensitiveAt = time.Now()
	s.rulesMu.Unlock()
	return list, nil
}

func (s *Service) insertRiskEvent(ctx context.Context, eventType, ip, detail string) error {
	now := db.FormatTime(time.Now())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO risk_riskevent (created_at, updated_at, event_type, request_ip, detail_json, user_id)
		 VALUES (?, ?, ?, ?, ?, NULL)`,
		now, now, eventType, db.NullableString(ip), detail,
	)
	return err
}

func (s *Service) loginFailLimit() int {
	return s.site.Int("risk.login_fail_limit", s.cfg.RiskLoginFailLimit)
}

func (s *Service) loginLockMinutes() int {
	return s.site.Int("risk.login_lock_minutes", s.cfg.RiskLoginLockMinutes)
}

func (s *Service) taskWindowSeconds() int {
	return s.site.Int("risk.task_window_seconds", int(s.cfg.RiskTaskWindow/time.Second))
}

func (s *Service) taskLimitPerWindow() int {
	return s.site.Int("risk.task_limit_per_window", s.cfg.RiskTaskLimit)
}

func loginKey(username, ip string) string {
	return "risk:login:" + username + ":" + ip
}

func taskKey(userID int64) string {
	return "risk:task:" + strconv.FormatInt(userID, 10)
}

func (s *Service) logError(message string, args ...any) {
	if s.logger != nil {
		s.logger.Error(message, args...)
	}
}
