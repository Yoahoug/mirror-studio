// Package bootstrap performs the idempotent startup seed: configuration rows,
// the bootstrap administrator and the default channels. It runs between
// migrations and the listener, so the service never serves a half-seeded
// database.
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"mirror-studio/internal/config"
	"mirror-studio/internal/siteconfig"
)

// SiteConfigDefaults builds the first-boot configuration values. Release
// metadata comes from CHANGELOG.md so no version string is hardcoded twice.
func SiteConfigDefaults(cfg *config.Config) map[string]string {
	return map[string]string{
		"site.name":                           cfg.SiteName,
		"site.brand_name":                     cfg.BrandName,
		"site.brand_english_name":             cfg.BrandEnglishName,
		"site.brand_tagline":                  cfg.BrandTagline,
		"site.register_enabled":               siteconfig.EncodeBool(cfg.RegisterEnabled),
		"site.login_captcha_enabled":          siteconfig.EncodeBool(cfg.LoginCaptchaEnabled),
		"site.image_retention_days":           itoa(cfg.ImageRetentionDays),
		"site.daily_user_task_limit":          itoa(cfg.DailyUserTaskLimit),
		"site.max_pending_tasks":              itoa(cfg.MaxPendingTasks),
		"site.default_channel_code":           cfg.DefaultChannelCode,
		"site.default_daily_free_quota":       itoa(cfg.DefaultDailyFreeQuota),
		"site.workspace_release_version":      cfg.Release.Version,
		"site.workspace_release_title":        cfg.Release.Title,
		"site.workspace_release_notes":        cfg.Release.Notes,
		"site.workspace_release_published_at": cfg.Release.PublishedAt,
		"email.verification_enabled":          siteconfig.EncodeBool(cfg.EmailVerification),
		"email.verification_code_length":      itoa(cfg.EmailCodeLength),
		"email.verification_ttl_seconds":      itoa(int(cfg.EmailCodeTTL.Seconds())),
		"email.verification_resend_seconds":   itoa(int(cfg.EmailResendInterval.Seconds())),
		"email.daily_limit_per_email":         itoa(cfg.EmailDailyPerEmail),
		"email.daily_limit_per_ip":            itoa(cfg.EmailDailyPerIP),
		"risk.login_fail_limit":               itoa(cfg.RiskLoginFailLimit),
		"risk.login_lock_minutes":             itoa(cfg.RiskLoginLockMinutes),
		"risk.task_window_seconds":            itoa(int(cfg.RiskTaskWindow.Seconds())),
		"risk.task_limit_per_window":          itoa(cfg.RiskTaskLimit),
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	buffer := make([]byte, 0, 12)
	for value > 0 {
		buffer = append([]byte{byte('0' + value%10)}, buffer...)
		value /= 10
	}
	if negative {
		return "-" + string(buffer)
	}
	return string(buffer)
}

// AdminSeeder creates the bootstrap administrator.
type AdminSeeder interface {
	EnsureAdminUser(ctx context.Context, username, password, email string) (bool, error)
}

// ChannelSeeder creates the default image and LLM channels.
type ChannelSeeder interface {
	EnsureDefaultChannels(ctx context.Context) error
}

// Options are the collaborators the seed touches.
type Options struct {
	Config  *config.Config
	Site    *siteconfig.Service
	Admin   AdminSeeder
	Channel ChannelSeeder
	Logger  *slog.Logger
}

// Run seeds configuration, the administrator and the channels. Every step is
// idempotent; a failure aborts startup with the reason printed.
func Run(ctx context.Context, options Options) error {
	created, err := options.Site.SeedDefaults(SiteConfigDefaults(options.Config))
	if err != nil {
		return fmt.Errorf("seed site configuration: %w", err)
	}
	if options.Logger != nil {
		options.Logger.Info("site configuration seeded", "created", created)
	}

	if options.Admin != nil {
		adminCreated, err := options.Admin.EnsureAdminUser(
			ctx, options.Config.AdminUsername, options.Config.AdminPassword, options.Config.AdminEmail,
		)
		if err != nil {
			return fmt.Errorf("seed administrator: %w", err)
		}
		if options.Logger != nil && options.Config.AdminUsername != "" {
			options.Logger.Info("administrator ready",
				"username", options.Config.AdminUsername, "created", adminCreated)
		}
	}

	if options.Channel != nil {
		if err := options.Channel.EnsureDefaultChannels(ctx); err != nil {
			return fmt.Errorf("seed default channels: %w", err)
		}
	}
	return nil
}
