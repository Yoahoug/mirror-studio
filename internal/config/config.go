// Package config is the only place in the service that reads environment
// variables. Every value is parsed and validated at startup; a missing or
// invalid required value aborts the process before the listener is opened.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the resolved process configuration.
type Config struct {
	Debug             bool
	ListenAddr        string
	PublicOrigin      string
	SecretKey         string
	FieldCryptKey     string
	TrustProxyHost    bool
	TrustForwardedFor bool
	// SecureSSLRedirect mirrors SECURE_SSL_REDIRECT: only honoured outside
	// DEBUG, redirecting plain-HTTP requests to the https origin.
	SecureSSLRedirect bool
	// SecureProxySSLHeaderValue is the X-Forwarded-Proto value that counts as
	// "already secure" behind a proxy (SECURE_PROXY_SSL_HEADER's second half).
	SecureProxySSLHeaderValue string

	TimeZone string

	// Site identity
	SiteName         string
	BrandName        string
	BrandEnglishName string
	BrandTagline     string
	WorkspaceViteURL string
	ConsoleViteURL   string

	// Accounts
	RegisterEnabled     bool
	LoginCaptchaEnabled bool
	EmailVerification   bool
	EmailCodeLength     int
	EmailCodeTTL        time.Duration
	EmailResendInterval time.Duration
	EmailDailyPerEmail  int
	EmailDailyPerIP     int

	EmailHost     string
	EmailPort     int
	EmailUser     string
	EmailPassword string
	EmailFrom     string
	EmailUseTLS   bool
	EmailUseSSL   bool
	EmailTimeout  time.Duration

	// Quota
	DefaultDailyFreeQuota int
	DailyUserTaskLimit    int
	MaxPendingTasks       int

	// Images
	ImageRetentionDays        int
	SoftDeleteRetentionDays   int
	ImagePreviewMaxDimension  int
	MaxReferenceImageBytes    int64
	MaxRemoteImageBytes       int64
	RemoteImageAllowedHosts   []string
	ResumableUploadChunkSize  int64
	ResumableUploadSessionTTL time.Duration

	// Risk
	RiskLoginFailLimit   int
	RiskLoginLockMinutes int
	RiskTaskWindow       time.Duration
	RiskTaskLimit        int

	// Generation
	MockProviderEnabled    bool
	WorkerPollInterval     time.Duration
	WorkerConcurrency      int
	DefaultChannelCode     string
	ProviderName           string
	ProviderVendor         string
	ProviderVendorCode     string
	ProviderBaseURL        string
	ProviderAPIPath        string
	ProviderModelName      string
	ProviderAPIKey         string
	ProviderTimeoutSeconds int
	ProviderMaxRetries     int
	OpenAIImageEditTimeout time.Duration

	LLMChannelCode    string
	LLMProviderName   string
	LLMProviderVendor string
	LLMBaseURL        string
	LLMAPIPath        string
	LLMModelName      string
	LLMAPIKey         string
	LLMTimeoutSeconds int
	LLMMaxRetries     int
	LLMPriority       int

	// Novel visualisation
	NovelvizAPIBaseURL       string
	NovelvizTextModel        string
	NovelvizTextAPIPath      string
	NovelvizTextAPIKey       string
	NovelvizImageModel       string
	NovelvizImageAPIPath     string
	NovelvizImageAPIKey      string
	NovelvizImageSize        string
	NovelvizImageQuality     string
	NovelvizBookDir          string
	NovelvizMaxEpubBytes     int64
	NovelvizMaxScenes        int
	NovelvizImageConcurrency int
	NovelvizImageMaxRetries  int
	NovelvizImageTimeout     time.Duration
	NovelvizTextMaxRetries   int
	NovelvizTextReadTimeout  time.Duration
	NovelvizSSEPollInterval  time.Duration
	NovelvizStallRequeue     time.Duration

	// Console
	ConsoleAPIRateLimit int

	// Internal seed
	AdminUsername string
	AdminPassword string
	AdminEmail    string

	// Storage
	DatabasePath string
	MediaRoot    string

	// Release info derived from CHANGELOG.md
	Release ReleaseInfo
}

// ReleaseInfo is the in-app "更新说明" payload. CHANGELOG.md is the single
// source; nothing here is hardcoded per version.
type ReleaseInfo struct {
	Version     string `json:"version"`
	Title       string `json:"title"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"published_at"`
}

func envStr(name, def string) string {
	if value, ok := os.LookupEnv(name); ok {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return def
}

func envRaw(name string) string { return os.Getenv(name) }

func envBool(name string, def bool) (bool, error) {
	if value, ok := os.LookupEnv(name); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on":
			return true, nil
		case "0", "false", "no", "off", "":
			return false, nil
		default:
			return def, fmt.Errorf("%s must be a boolean (1/0/true/false), got %q", name, value)
		}
	}
	return def, nil
}

func envInt(name string, def int) (int, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return def, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return def, fmt.Errorf("%s must be an integer, got %q", name, value)
	}
	return parsed, nil
}

func envList(name string, def []string) []string {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return def
	}
	items := []string{}
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	if len(items) == 0 {
		return def
	}
	return items
}

// Load resolves the configuration from the environment and validates it.
// Any error returned here must abort startup: the caller prints it and exits
// non-zero rather than serving a partially configured service.
func Load() (*Config, error) {
	// Switches the rewrite deleted outright must fail loudly when a deployment
	// still sets them: silently ignoring ALLOW_INSECURE_PROD would reopen the
	// hole the guard exists for, and a stale CONSOLE_HMAC_ENABLED would look
	// like it restored a control that no longer exists.
	for _, name := range []string{
		"ALLOW_INSECURE_PROD", "CONSOLE_HMAC_ENABLED",
		"GENERATION_TASKS_RUN_INLINE", "NOVELVIZ_RUN_INLINE",
	} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return nil, fmt.Errorf("%s was removed in 0.0.1 and must not be set; delete it from the environment", name)
		}
	}
	if strings.TrimSpace(os.Getenv("MINIO_ENDPOINT")) != "" {
		return nil, fmt.Errorf("MINIO_ENDPOINT was removed in 0.0.1: the storage backend is the local filesystem under MEDIA_ROOT")
	}

	cfg := &Config{}
	var err error

	cfg.ListenAddr = envStr("APP_LISTEN", "")
	cfg.TimeZone = envStr("TIME_ZONE", "Asia/Shanghai")
	cfg.MediaRoot = envStr("MEDIA_ROOT", "media")
	cfg.NovelvizBookDir = envStr("NOVELVIZ_BOOK_DIR", "novel")

	if cfg.Debug, err = envBool("DEBUG", false); err != nil {
		return nil, err
	}

	// ── required secrets ──
	cfg.SecretKey = envRaw("SECRET_KEY")
	if strings.TrimSpace(cfg.SecretKey) == "" || cfg.SecretKey == "replace-me" {
		return nil, fmt.Errorf("SECRET_KEY is required and must not be the placeholder value")
	}
	if len(cfg.SecretKey) < 32 {
		return nil, fmt.Errorf("SECRET_KEY must be at least 32 characters, got %d", len(cfg.SecretKey))
	}

	// Field encryption protects channel API keys. An empty key used to mean
	// "store plaintext"; that is a security defect, so the key is mandatory.
	cfg.FieldCryptKey = strings.TrimSpace(envRaw("FIELD_CRYPT_KEY"))
	if cfg.FieldCryptKey == "" {
		return nil, fmt.Errorf("FIELD_CRYPT_KEY is required: channel API keys must be encrypted at rest")
	}
	if len(cfg.FieldCryptKey) < 8 {
		return nil, fmt.Errorf("FIELD_CRYPT_KEY must be at least 8 characters, got %d", len(cfg.FieldCryptKey))
	}

	appPort, err := envInt("APP_PORT", 8000)
	if err != nil {
		return nil, err
	}
	appPort = clampRange(appPort, 1, 65535)

	origin := strings.TrimSpace(envRaw("PUBLIC_ORIGIN"))
	if origin == "" {
		origin = strings.TrimSpace(envRaw("SITE_DOMAIN"))
	}
	cfg.PublicOrigin = normalizeOrigin(origin)
	if cfg.PublicOrigin == "" {
		if !cfg.Debug {
			return nil, fmt.Errorf("PUBLIC_ORIGIN must be an absolute http(s) origin; configure it before serving")
		}
		cfg.PublicOrigin = fmt.Sprintf("http://127.0.0.1:%d", appPort)
	}

	if cfg.TrustProxyHost, err = envBool("USE_X_FORWARDED_HOST", !cfg.Debug); err != nil {
		return nil, err
	}
	if cfg.TrustForwardedFor, err = envBool("TRUST_X_FORWARDED_FOR", !cfg.Debug); err != nil {
		return nil, err
	}
	// SECURE_SSL_REDIRECT ports the reference's production setting: plain-HTTP
	// requests are permanently redirected to the https origin. The proxy header
	// decides what "already https" means, mirroring SECURE_PROXY_SSL_HEADER.
	if cfg.SecureSSLRedirect, err = envBool("SECURE_SSL_REDIRECT", !cfg.Debug); err != nil {
		return nil, err
	}
	cfg.SecureProxySSLHeaderValue = envStr("SECURE_PROXY_SSL_HEADER_VALUE", "https")

	cfg.SiteName = envStr("SITE_NAME", "绘镜 Studio")
	cfg.BrandName = envStr("BRAND_NAME", "绘镜")
	cfg.BrandEnglishName = envStr("BRAND_ENGLISH_NAME", "MIRROR STUDIO")
	cfg.BrandTagline = envStr("BRAND_TAGLINE", "把灵感写成图像，把作品留在自己的空间里")

	if cfg.Debug {
		cfg.WorkspaceViteURL = strings.TrimSpace(envStr("WORKSPACE_VITE_DEV_URL", "http://127.0.0.1:3000"))
		cfg.ConsoleViteURL = strings.TrimSpace(envStr("CONSOLE_VITE_DEV_URL", "http://127.0.0.1:3001"))
	}

	if cfg.RegisterEnabled, err = envBool("REGISTER_ENABLED", true); err != nil {
		return nil, err
	}
	if cfg.LoginCaptchaEnabled, err = envBool("LOGIN_CAPTCHA_ENABLED", true); err != nil {
		return nil, err
	}
	if cfg.EmailVerification, err = envBool("EMAIL_VERIFICATION_ENABLED", true); err != nil {
		return nil, err
	}
	if cfg.EmailCodeLength, err = envInt("EMAIL_VERIFICATION_CODE_LENGTH", 6); err != nil {
		return nil, err
	}
	if cfg.EmailCodeTTL, err = envSeconds("EMAIL_VERIFICATION_TTL_SECONDS", 600); err != nil {
		return nil, err
	}
	if cfg.EmailResendInterval, err = envSeconds("EMAIL_VERIFICATION_RESEND_SECONDS", 60); err != nil {
		return nil, err
	}
	if cfg.EmailDailyPerEmail, err = envInt("EMAIL_VERIFICATION_DAILY_LIMIT_PER_EMAIL", 10); err != nil {
		return nil, err
	}
	if cfg.EmailDailyPerIP, err = envInt("EMAIL_VERIFICATION_DAILY_LIMIT_PER_IP", 30); err != nil {
		return nil, err
	}
	cfg.EmailHost = envStr("EMAIL_HOST", "")
	cfg.EmailUser = envStr("EMAIL_HOST_USER", "")
	cfg.EmailPassword = envRaw("EMAIL_HOST_PASSWORD")
	if cfg.EmailPort, err = envInt("EMAIL_PORT", 465); err != nil {
		return nil, err
	}
	if cfg.EmailUseTLS, err = envBool("EMAIL_USE_TLS", false); err != nil {
		return nil, err
	}
	if cfg.EmailUseSSL, err = envBool("EMAIL_USE_SSL", true); err != nil {
		return nil, err
	}
	if cfg.EmailTimeout, err = envSeconds("EMAIL_TIMEOUT", 15); err != nil {
		return nil, err
	}
	cfg.EmailFrom = envStr("DEFAULT_FROM_EMAIL", defaultFrom(cfg))

	if cfg.DefaultDailyFreeQuota, err = envInt("DEFAULT_DAILY_FREE_QUOTA", 3); err != nil {
		return nil, err
	}
	if cfg.DailyUserTaskLimit, err = envInt("DAILY_USER_TASK_LIMIT", 50); err != nil {
		return nil, err
	}
	if cfg.MaxPendingTasks, err = envInt("MAX_PENDING_TASKS", 3); err != nil {
		return nil, err
	}

	if cfg.ImageRetentionDays, err = envInt("IMAGE_RETENTION_DAYS", 30); err != nil {
		return nil, err
	}
	if cfg.SoftDeleteRetentionDays, err = envInt("IMAGE_HISTORY_SOFT_DELETE_RETENTION_DAYS", 7); err != nil {
		return nil, err
	}
	if cfg.ImagePreviewMaxDimension, err = envInt("IMAGE_PREVIEW_MAX_DIMENSION", 768); err != nil {
		return nil, err
	}
	if cfg.MaxReferenceImageBytes, err = envBytes("MAX_REFERENCE_IMAGE_BYTES", 10*1024*1024); err != nil {
		return nil, err
	}
	if cfg.MaxRemoteImageBytes, err = envBytes("MAX_REMOTE_IMAGE_BYTES", 25*1024*1024); err != nil {
		return nil, err
	}
	cfg.RemoteImageAllowedHosts = envList("REMOTE_IMAGE_ALLOWED_HOSTS", []string{"s3.siliconflow.cn"})
	if cfg.ResumableUploadChunkSize, err = envBytes("RESUMABLE_UPLOAD_CHUNK_SIZE", 8*1024*1024); err != nil {
		return nil, err
	}
	if cfg.ResumableUploadSessionTTL, err = envSeconds("RESUMABLE_UPLOAD_SESSION_TTL_SECONDS", 24*60*60); err != nil {
		return nil, err
	}

	if cfg.RiskLoginFailLimit, err = envInt("RISK_LOGIN_FAIL_LIMIT", 5); err != nil {
		return nil, err
	}
	if cfg.RiskLoginLockMinutes, err = envInt("RISK_LOGIN_LOCK_MINUTES", 15); err != nil {
		return nil, err
	}
	if cfg.RiskTaskWindow, err = envSeconds("RISK_TASK_WINDOW_SECONDS", 60); err != nil {
		return nil, err
	}
	if cfg.RiskTaskLimit, err = envInt("RISK_TASK_LIMIT_PER_WINDOW", 10); err != nil {
		return nil, err
	}

	// The reference defaults the mock provider on in development and refuses to
	// boot production with it enabled (prod.py). The rewrite keeps one default:
	// off, so a deployment that forgets the variable serves real traffic, and
	// the same production guard the reference enforces.
	if cfg.MockProviderEnabled, err = envBool("MOCK_PROVIDER_ENABLED", false); err != nil {
		return nil, err
	}
	if !cfg.Debug && cfg.MockProviderEnabled {
		return nil, fmt.Errorf("MOCK_PROVIDER_ENABLED is enabled: the mock image provider must not serve production traffic")
	}
	if cfg.WorkerPollInterval, err = envSeconds("GENERATION_WORKER_POLL_SECONDS", 2); err != nil {
		return nil, err
	}
	if cfg.WorkerConcurrency, err = envInt("GENERATION_WORKER_CONCURRENCY", 4); err != nil {
		return nil, err
	}
	cfg.WorkerConcurrency = clampRange(cfg.WorkerConcurrency, 1, 32)

	cfg.DefaultChannelCode = envStr("DEFAULT_CHANNEL_CODE", "default")
	cfg.ProviderName = envStr("PROVIDER_NAME", "默认图片渠道")
	cfg.ProviderVendor = envStr("PROVIDER_VENDOR", "OpenAI-Compatible")
	cfg.ProviderVendorCode = strings.ToLower(envStr("PROVIDER_VENDOR_CODE", "openai"))
	cfg.ProviderBaseURL = envStr("PROVIDER_BASE_URL", "http://127.0.0.1:8080")
	cfg.ProviderAPIPath = envStr("PROVIDER_API_PATH", "/v1/images/generations")
	cfg.ProviderModelName = envStr("PROVIDER_MODEL_NAME", "gpt-image-2")
	cfg.ProviderAPIKey = envRaw("PROVIDER_API_KEY")
	if cfg.ProviderTimeoutSeconds, err = envInt("PROVIDER_TIMEOUT_SECONDS", 300); err != nil {
		return nil, err
	}
	if cfg.ProviderMaxRetries, err = envInt("PROVIDER_MAX_RETRIES", 1); err != nil {
		return nil, err
	}
	if cfg.OpenAIImageEditTimeout, err = envSeconds("OPENAI_IMAGE_EDIT_TIMEOUT_SECONDS", 420); err != nil {
		return nil, err
	}

	cfg.LLMChannelCode = envStr("LLM_CHANNEL_CODE", "default-llm")
	cfg.LLMProviderName = envStr("LLM_PROVIDER_NAME", "默认 LLM 渠道")
	cfg.LLMProviderVendor = envStr("LLM_PROVIDER_VENDOR", "OpenAI-Compatible")
	cfg.LLMBaseURL = envStr("LLM_BASE_URL", envStr("NOVELVIZ_API_BASE_URL", cfg.ProviderBaseURL))
	cfg.LLMAPIPath = envStr("LLM_API_PATH", "/v1/chat/completions")
	cfg.LLMModelName = envStr("LLM_MODEL_NAME", envStr("NOVELVIZ_TEXT_MODEL", "gpt-5.4"))
	cfg.LLMAPIKey = envRaw("LLM_API_KEY")
	if cfg.LLMTimeoutSeconds, err = envInt("LLM_TIMEOUT_SECONDS", 600); err != nil {
		return nil, err
	}
	if cfg.LLMMaxRetries, err = envInt("LLM_MAX_RETRIES", 3); err != nil {
		return nil, err
	}
	if cfg.LLMPriority, err = envInt("LLM_PRIORITY", 1); err != nil {
		return nil, err
	}

	// ── novel visualisation ──
	cfg.NovelvizAPIBaseURL = envStr("NOVELVIZ_API_BASE_URL", cfg.ProviderBaseURL)
	cfg.NovelvizTextModel = envStr("NOVELVIZ_TEXT_MODEL", cfg.LLMModelName)
	cfg.NovelvizTextAPIPath = envStr("NOVELVIZ_TEXT_API_PATH", "/chat/completions")
	cfg.NovelvizTextAPIKey = envStr("NOVELVIZ_TEXT_API_KEY", cfg.LLMAPIKey)
	cfg.NovelvizImageModel = envStr("NOVELVIZ_IMAGE_MODEL", cfg.ProviderModelName)
	cfg.NovelvizImageAPIPath = envStr("NOVELVIZ_IMAGE_API_PATH", "/images/generations")
	cfg.NovelvizImageAPIKey = envStr("NOVELVIZ_IMAGE_API_KEY", cfg.ProviderAPIKey)
	cfg.NovelvizImageSize = envStr("NOVELVIZ_IMAGE_SIZE", "1024x1024")
	cfg.NovelvizImageQuality = envStr("NOVELVIZ_IMAGE_QUALITY", "high")
	if cfg.NovelvizMaxEpubBytes, err = envBytes("NOVELVIZ_MAX_EPUB_UPLOAD_BYTES", 80*1024*1024); err != nil {
		return nil, err
	}
	if cfg.NovelvizMaxScenes, err = envInt("NOVELVIZ_MAX_SCENES", 10); err != nil {
		return nil, err
	}
	concurrency, err := envInt("NOVELVIZ_IMAGE_CONCURRENCY", 3)
	if err != nil {
		return nil, err
	}
	cfg.NovelvizImageConcurrency = clampRange(concurrency, 1, 8)
	if cfg.NovelvizImageMaxRetries, err = envInt("NOVELVIZ_IMAGE_MAX_RETRIES", 2); err != nil {
		return nil, err
	}
	if cfg.NovelvizImageTimeout, err = envSeconds("NOVELVIZ_IMAGE_TIMEOUT_SECONDS", 60); err != nil {
		return nil, err
	}
	if cfg.NovelvizTextMaxRetries, err = envInt("NOVELVIZ_TEXT_MAX_RETRIES", 2); err != nil {
		return nil, err
	}
	if cfg.NovelvizTextReadTimeout, err = envSeconds("NOVELVIZ_TEXT_READ_TIMEOUT_SECONDS", 60); err != nil {
		return nil, err
	}
	if cfg.NovelvizSSEPollInterval, err = envSeconds("NOVELVIZ_SSE_POLL_SECONDS", 1); err != nil {
		return nil, err
	}
	if cfg.NovelvizSSEPollInterval < 250*time.Millisecond {
		cfg.NovelvizSSEPollInterval = 250 * time.Millisecond
	}
	if cfg.NovelvizStallRequeue, err = envSeconds("NOVELVIZ_STALL_REQUEUE_SECONDS", 60); err != nil {
		return nil, err
	}

	if cfg.ConsoleAPIRateLimit, err = envInt("CONSOLE_API_RATE_LIMIT", 60); err != nil {
		return nil, err
	}

	cfg.AdminUsername = envStr("ADMIN_USERNAME", "")
	cfg.AdminPassword = envRaw("ADMIN_PASSWORD")
	cfg.AdminEmail = envStr("ADMIN_EMAIL", "admin@example.com")

	cfg.DatabasePath, err = databasePathFromURL(envStr("DATABASE_URL", "sqlite:///db.sqlite3"))
	if err != nil {
		return nil, err
	}

	cfg.Release = ParseChangelog("CHANGELOG.md", envStr("APP_RELEASE_FALLBACK_VERSION", "0.0.1"))
	return cfg, nil
}

func defaultFrom(cfg *Config) string {
	if cfg.EmailUser != "" {
		return cfg.EmailUser
	}
	return "noreply@example.com"
}

func envSeconds(name string, def int) (time.Duration, error) {
	value, err := envInt(name, def)
	if err != nil {
		return 0, err
	}
	return time.Duration(value) * time.Second, nil
}

func envBytes(name string, def int64) (int64, error) {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return def, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return def, fmt.Errorf("%s must be an integer byte count, got %q", name, value)
	}
	if parsed <= 0 {
		return def, fmt.Errorf("%s must be positive, got %d", name, parsed)
	}
	return parsed, nil
}

func clampRange(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// normalizeOrigin keeps only scheme://host[:port], dropping any path, the same
// way the reference implementation does before it stores PUBLIC_ORIGIN.
func normalizeOrigin(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return ""
	}
	scheme, rest, ok := strings.Cut(trimmed, "://")
	if !ok || (scheme != "http" && scheme != "https") || rest == "" {
		return ""
	}
	authority := rest
	if slash := strings.IndexByte(authority, '/'); slash >= 0 {
		authority = authority[:slash]
	}
	if authority == "" {
		return ""
	}
	return scheme + "://" + authority
}

// databasePathFromURL accepts the same sqlite URLs as the reference
// configuration (sqlite:///<path>, sqlite://<path>, or a bare path).
func databasePathFromURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("DATABASE_URL must not be empty")
	}
	if strings.HasPrefix(value, "sqlite:") {
		// Matches dj_database_url: everything after "sqlite://" is the path with
		// exactly one leading slash removed, so "sqlite:///db.sqlite3" is
		// relative and "sqlite:////srv/db.sqlite3" is absolute.
		value = strings.TrimPrefix(value, "sqlite:")
		value = strings.TrimPrefix(value, "//")
		value = strings.TrimPrefix(value, "/")
		if value == "" {
			return "", fmt.Errorf("DATABASE_URL must name a sqlite database file")
		}
		return value, nil
	}
	if strings.Contains(value, "://") {
		scheme, _, _ := strings.Cut(value, "://")
		return "", fmt.Errorf("DATABASE_URL scheme %q is not supported; 0.0.1 runs on sqlite only", scheme)
	}
	return value, nil
}
