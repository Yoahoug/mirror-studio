// Package console implements the operator console API served under
// /console/api/. The httpapi middleware chain already enforces the operator
// check, the per-account rate limit and the console security headers, so this
// package owns routing, payload validation, read queries and serialization only.
package console

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"mirror-studio/internal/auth"
	"mirror-studio/internal/config"
	"mirror-studio/internal/crypt"
	"mirror-studio/internal/db"
	"mirror-studio/internal/generator"
	"mirror-studio/internal/media"
	"mirror-studio/internal/quota"
	"mirror-studio/internal/siteconfig"
)

// UserService is the slice of internal/auth the console calls. It stays narrow
// so the console does not take a compile dependency on the whole auth domain.
type UserService interface {
	ByID(ctx context.Context, id int64) (*auth.User, error)
}

// QuotaService is the slice of internal/quota the console calls: account reads
// and the administrator adjustment, both owned by the quota domain.
type QuotaService interface {
	Ensure(ctx context.Context, userID int64) (*quota.Account, error)
	AdminAdjust(ctx context.Context, userID int64, dailyQuota, availableQuota *int, remark string) (*quota.Account, error)
}

// Deps wires the console domain to the shared infrastructure and to the
// domains that own the rows it administrates. Generator and Media are part of
// the wiring contract even though the console renders their rows with its own
// read queries: neither domain exposes a console-shaped list API.
type Deps struct {
	DB        *db.DB
	Config    *config.Config
	Site      *siteconfig.Service
	Location  *time.Location
	Auth      UserService
	Quota     QuotaService
	Generator *generator.Service
	Media     *media.Service
	Logger    *slog.Logger
}

// Service is the console business layer.
type Service struct {
	db     *db.DB
	site   *siteconfig.Service
	auth   UserService
	quota  QuotaService
	log    *slog.Logger
	cipher *crypt.FieldCipher
	loc    *time.Location
}

// NewService builds the console service. Day boundaries follow the deployment's
// TIME_ZONE because the reference takes them from Django's localdate().
func NewService(deps Deps) *Service {
	location := deps.Location
	if location == nil {
		location = time.UTC
		if deps.Config != nil && strings.TrimSpace(deps.Config.TimeZone) != "" {
			if loaded, err := time.LoadLocation(deps.Config.TimeZone); err == nil {
				location = loaded
			}
		}
	}
	var cipher *crypt.FieldCipher
	if deps.Site != nil {
		cipher = deps.Site.Cipher
	}
	return &Service{
		db:     deps.DB,
		site:   deps.Site,
		auth:   deps.Auth,
		quota:  deps.Quota,
		log:    deps.Logger,
		cipher: cipher,
		loc:    location,
	}
}

// NewHandlers builds the console HTTP layer.
func (s *Service) NewHandlers() *Handlers { return &Handlers{svc: s} }

func (s *Service) now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func (s *Service) localDate() string { return time.Now().In(s.loc).Format("2006-01-02") }

// todayRange returns the UTC storage bounds of the current local day.
func (s *Service) todayRange() (string, string) {
	now := time.Now().In(s.loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.loc)
	return db.FormatTime(start), db.FormatTime(start.AddDate(0, 0, 1))
}

func (s *Service) userExists(ctx context.Context, id int64) (bool, error) {
	if s.auth == nil {
		return false, errors.New("auth service is not wired")
	}
	if _, err := s.auth.ByID(ctx, id); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Service) encryptSecret(value string) (string, error) {
	if s.cipher == nil {
		return "", errors.New("FIELD_CRYPT_KEY is not configured")
	}
	return s.cipher.Encrypt(value)
}

func (s *Service) logError(message string, err error) {
	if s.log != nil {
		s.log.Error(message, "error", err)
	}
}
