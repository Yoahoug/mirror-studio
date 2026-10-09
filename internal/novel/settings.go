package novel

import (
	"os"
	"strconv"
	"strings"
	"time"

	"mirror-studio/internal/config"
)

// settings is the resolved novel-visualisation configuration. Most values come
// from internal/config; the event-log knobs and the upload directory have no
// counterpart in config.Config (0.0.1 scopes config there), so they are read
// here with the reference defaults: NOVELVIZ_EVENT_LOG_LIMIT (120),
// NOVELVIZ_EVENT_PAYLOAD_MAX_CHARS (120000), NOVELVIZ_EVENT_FIELD_MAX_CHARS
// (4000) and NOVELVIZ_UPLOAD_DIR (novelviz/uploads).
type settings struct {
	bookDir      string
	uploadDir    string
	mediaRoot    string
	maxEPUBBytes int64

	apiBaseURL   string
	textAPIPath  string
	textModel    string
	textAPIKey   string
	textRetries  int
	textTimeout  time.Duration
	imageAPIPath string
	imageModel   string
	imageAPIKey  string
	imageSize    string
	imageQuality string
	imageTimeout time.Duration
	imageRetries int
	concurrency  int

	maxScenes           int
	ssePoll             time.Duration
	stallRequeue        time.Duration
	previewMaxDimension int

	eventLogLimit   int
	payloadMaxChars int
	fieldMaxChars   int
}

func loadSettings(cfg *config.Config) settings {
	resolved := settings{
		bookDir:      cfg.NovelvizBookDir,
		uploadDir:    envString("NOVELVIZ_UPLOAD_DIR", "novelviz/uploads"),
		mediaRoot:    cfg.MediaRoot,
		maxEPUBBytes: cfg.NovelvizMaxEpubBytes,

		apiBaseURL:   cfg.NovelvizAPIBaseURL,
		textAPIPath:  cfg.NovelvizTextAPIPath,
		textModel:    cfg.NovelvizTextModel,
		textAPIKey:   cfg.NovelvizTextAPIKey,
		textRetries:  cfg.NovelvizTextMaxRetries,
		textTimeout:  cfg.NovelvizTextReadTimeout,
		imageAPIPath: cfg.NovelvizImageAPIPath,
		imageModel:   cfg.NovelvizImageModel,
		imageAPIKey:  cfg.NovelvizImageAPIKey,
		imageSize:    cfg.NovelvizImageSize,
		imageQuality: cfg.NovelvizImageQuality,
		imageTimeout: cfg.NovelvizImageTimeout,
		imageRetries: cfg.NovelvizImageMaxRetries,
		concurrency:  cfg.NovelvizImageConcurrency,

		maxScenes:           cfg.NovelvizMaxScenes,
		ssePoll:             cfg.NovelvizSSEPollInterval,
		stallRequeue:        cfg.NovelvizStallRequeue,
		previewMaxDimension: cfg.ImagePreviewMaxDimension,

		eventLogLimit:   envIntDefault("NOVELVIZ_EVENT_LOG_LIMIT", 120),
		payloadMaxChars: envIntDefault("NOVELVIZ_EVENT_PAYLOAD_MAX_CHARS", 120_000),
		fieldMaxChars:   envIntDefault("NOVELVIZ_EVENT_FIELD_MAX_CHARS", 4_000),
	}
	if resolved.maxScenes < 1 {
		resolved.maxScenes = 1
	}
	if resolved.imageRetries < 1 {
		resolved.imageRetries = 1
	}
	if resolved.textRetries < 1 {
		resolved.textRetries = 1
	}
	if resolved.concurrency < 1 {
		resolved.concurrency = 1
	}
	if resolved.concurrency > 8 {
		resolved.concurrency = 8
	}
	if resolved.ssePoll < 250*time.Millisecond {
		resolved.ssePoll = 250 * time.Millisecond
	}
	if resolved.stallRequeue < 30*time.Second {
		resolved.stallRequeue = 30 * time.Second
	}
	if resolved.previewMaxDimension < 64 {
		resolved.previewMaxDimension = 64
	}
	if resolved.eventLogLimit < 1 {
		resolved.eventLogLimit = 1
	}
	if resolved.payloadMaxChars < 1000 {
		resolved.payloadMaxChars = 1000
	}
	if resolved.fieldMaxChars < 200 {
		resolved.fieldMaxChars = 200
	}
	return resolved
}

func envString(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envIntDefault(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// joinProviderURL mirrors services._join_provider_url.
func joinProviderURL(baseURL, apiPath string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(apiPath, "/")
}
