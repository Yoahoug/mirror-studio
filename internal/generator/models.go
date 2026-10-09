package generator

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"mirror-studio/internal/crypt"
	"mirror-studio/internal/media"
)

// Task statuses, generation modes and channel types are part of the frozen
// contract and must keep these exact spellings.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
)

const (
	ModeTextToImage  = "text_to_image"
	ModeImageToImage = "image_to_image"
)

const (
	ChannelTypeImage = "image"
	ChannelTypeLLM   = "llm"
)

const (
	VendorOpenAI      = "openai"
	VendorSiliconFlow = "siliconflow"
)

const (
	imageStatusAvailable = "available"
	imageStatusDeleted   = "deleted"
)

const (
	publicFailureMessage   = "生成失败，请稍后重试。若多次失败请联系管理员排查上游渠道。"
	canceledPublicMessage  = "任务已取消"
	missingTaskMessage     = "任务不存在"
	topicMissingMessage    = "主题不存在"
	defaultTaskTitle       = "新的创作"
	imageToImageNeedsPhoto = "图生图模式需要上传参考图"
)

const (
	referenceUploadTokenSalt    = "generator.reference-upload"
	referenceUploadTokenMaxAge  = 1800 * time.Second
	referenceUploadTaskNoPrefix = "R"
)

const (
	minImagePixels      = 655360
	maxImageEdge        = 2048
	maxImageAspectRatio = 3
	minLeaseSeconds     = 180
	defaultTimeoutSecs  = 60
)

const (
	maxPromptRunes         = 4000
	maxNegativePromptRunes = 2000
	maxTaskNoLength        = 32
	maxReferenceNameRunes  = 255
)

var sizeCodePattern = regexp.MustCompile(`(?i)^([0-9]{2,5})x([0-9]{2,5})$`)

var allowedStyles = map[string]bool{
	"general":     true,
	"cinematic":   true,
	"anime":       true,
	"photography": true,
}

var allowedQualities = map[string]bool{
	"auto":   true,
	"low":    true,
	"medium": true,
	"high":   true,
}

var allowedGenerationModes = map[string]bool{
	ModeTextToImage:  true,
	ModeImageToImage: true,
}

// Channel is one generator_imagechannel row. APIKeyEncrypted is never serialised
// and never logged; the plaintext key is decrypted on demand with the field
// cipher.
type Channel struct {
	ID              int64
	Name            string
	ChannelCode     string
	ChannelType     string
	ProviderVendor  string
	ProviderName    string
	BaseURL         string
	APIPath         string
	EditAPIPath     string
	APIKeyEncrypted string
	ModelName       string
	TimeoutSeconds  int
	MaxRetries      int
	IsActive        bool
	Priority        int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// APIKey decrypts the stored key. It is called only where the key is needed for
// an upstream request.
func (c *Channel) APIKey(cipher *crypt.FieldCipher) (string, error) {
	if strings.TrimSpace(c.APIKeyEncrypted) == "" {
		return "", nil
	}
	if cipher == nil {
		return "", fmt.Errorf("field cipher is not configured")
	}
	return cipher.Decrypt(c.APIKeyEncrypted)
}

// MarshalJSON omits the ciphertext so a channel can never leak its key through
// an accidental JSON response.
func (c Channel) MarshalJSON() ([]byte, error) {
	type projection struct {
		ID             int64  `json:"id"`
		Name           string `json:"name"`
		ChannelCode    string `json:"channel_code"`
		ChannelType    string `json:"channel_type"`
		ProviderVendor string `json:"provider_vendor"`
		ProviderName   string `json:"provider_name"`
		BaseURL        string `json:"base_url"`
		APIPath        string `json:"api_path"`
		EditAPIPath    string `json:"edit_api_path"`
		ModelName      string `json:"model_name"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		MaxRetries     int    `json:"max_retries"`
		IsActive       bool   `json:"is_active"`
		Priority       int    `json:"priority"`
	}
	return json.Marshal(projection{
		ID: c.ID, Name: c.Name, ChannelCode: c.ChannelCode, ChannelType: c.ChannelType,
		ProviderVendor: c.ProviderVendor, ProviderName: c.ProviderName, BaseURL: c.BaseURL,
		APIPath: c.APIPath, EditAPIPath: c.EditAPIPath, ModelName: c.ModelName,
		TimeoutSeconds: c.TimeoutSeconds, MaxRetries: c.MaxRetries, IsActive: c.IsActive, Priority: c.Priority,
	})
}

// Topic is one generator_generationtopic row.
type Topic struct {
	ID           int64
	UserID       int64
	Title        string
	CoverImageID *int64
	LastTaskAt   time.Time
	IsArchived   bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Task is one generator_generatetask row plus its generated images.
type Task struct {
	ID        int64
	UserID    int64
	TopicID   *int64
	ChannelID int64

	TaskNo             string
	Status             string
	ProgressPercent    int
	GenerationMode     string
	Prompt             string
	NegativePrompt     string
	StyleCode          string
	SizeCode           string
	QualityCode        string
	ReferenceImageName string

	ReferenceImageDataURL    string
	ReferenceStorageProvider string
	ReferenceBucketName      string
	ReferenceObjectKey       string
	ReferenceImageMimeType   string
	ReferenceImageSize       int
	ReferenceImageWidth      int
	ReferenceImageHeight     int
	ReferenceImageSHA256     string

	QuotaCost            int
	ProviderRequestID    string
	ProviderResponseJSON string
	ErrorCode            string
	ErrorMessage         string
	ExecutionAttempts    int

	SubmittedAt    time.Time
	CreatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	LeaseExpiresAt *time.Time

	ClientRequestID string
	ClientIP        string
	QuotaFinalized  bool
	IsDeleted       bool
	DeletedAt       *time.Time

	// Images are loaded alongside the row for serialization.
	Images []media.Image
	// Channel is loaded for the worker and for lease timeout computation.
	Channel *Channel
}

// ReferenceAttached mirrors `bool(reference_object_key or reference_image_data_url)`.
func (t *Task) ReferenceAttached() bool {
	return t.ReferenceObjectKey != "" || t.ReferenceImageDataURL != ""
}

// normalizeSizeCode applies the contract's size rules and returns the canonical
// `WxH` spelling. An empty value takes the default, while a whitespace-only one
// does not — exactly like `(size_code or "1024x1024").strip().lower()`.
func normalizeSizeCode(sizeCode string) (string, error) {
	raw := sizeCode
	if raw == "" {
		raw = "1024x1024"
	}
	normalized := strings.ToLower(strings.TrimSpace(raw))
	match := sizeCodePattern.FindStringSubmatch(normalized)
	if match == nil {
		return "", validationFailure("不支持的尺寸")
	}
	width := atoi(match[1])
	height := atoi(match[2])
	if width%16 != 0 || height%16 != 0 {
		return "", validationFailure("尺寸宽高必须是 16px 的倍数")
	}
	if width > maxImageEdge || height > maxImageEdge {
		return "", validationFailure("尺寸最大边不能超过 2048px")
	}
	shortEdge := width
	if height < shortEdge {
		shortEdge = height
	}
	longEdge := height
	if width > longEdge {
		longEdge = width
	}
	if shortEdge <= 0 || float64(longEdge)/float64(shortEdge) > maxImageAspectRatio {
		return "", validationFailure("尺寸长短边比例不能超过 3:1")
	}
	if width*height < minImagePixels {
		return "", validationFailure("尺寸总像素过小，请提高分辨率")
	}
	return fmt.Sprintf("%dx%d", width, height), nil
}

func normalizeQualityCode(qualityCode string) (string, error) {
	raw := qualityCode
	if raw == "" {
		raw = "auto"
	}
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if !allowedQualities[normalized] {
		return "", validationFailure("不支持的质量")
	}
	return normalized, nil
}

// buildPublicErrorMessage is the user-visible normalization: the raw upstream
// message never reaches the workspace.
func buildPublicErrorMessage(task *Task) string {
	switch task.Status {
	case StatusFailed:
		return publicFailureMessage
	case StatusCanceled:
		return canceledPublicMessage
	}
	return task.ErrorMessage
}

func clampProgress(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func atoi(value string) int {
	result := 0
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return result
		}
		result = result*10 + int(value[index]-'0')
	}
	return result
}

// trimRunes mirrors python slicing on a string: it cuts by code point.
func trimRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// ── cross-domain error kinds ──

type errorKind int

const (
	kindPayload errorKind = iota
	kindValidation
	kindPermission
	kindMissing
	kindInternal
)

type kindedError struct {
	kind    errorKind
	message string
}

func (e *kindedError) Error() string { return e.message }

func payloadFailure(message string) error { return &kindedError{kind: kindPayload, message: message} }
func validationFailure(message string) error {
	return &kindedError{kind: kindValidation, message: message}
}
func permissionFailure(message string) error {
	return &kindedError{kind: kindPermission, message: message}
}
func missingFailure(message string) error { return &kindedError{kind: kindMissing, message: message} }

func errorKindOf(err error) errorKind {
	if kinded, ok := err.(*kindedError); ok {
		return kinded.kind
	}
	return kindInternal
}

// missingTask reports a task that does not exist, belongs to another user, or
// is already soft-deleted. The detail endpoint turns it into an HTML 404, the
// delete and cancel endpoints into 400101.
func missingTask() error { return missingFailure(missingTaskMessage) }

// historyCandidate is one row of the merged workspace history.
type historyCandidate struct {
	createdAt  time.Time
	id         int64
	kind       string
	task       *Task
	serialized map[string]any
}

// NovelHistoryProvider is the novel-visualisation side of the merged workspace
// history. internal/novel depends on this package, so the collaborator cannot
// be referenced by type here; *novel.Service satisfies it with:
//
//	HistoryTotal(ctx, userID, keyword)               // count of the user's novel tasks, book_filename icontains keyword when keyword != ""
//	HistoryCandidates(ctx, userID, keyword, limit)   // newest `limit` of the same queryset
//
// Each candidate must carry:
//
//	"created_at"   the row's creation time (string or time.Time)
//	"id"           the novel task primary key
//	"__serialized" the fully serialized payload (`_serialize_novel_history_task`)
//
// The merge here sorts by (created_at, id) descending with the generation side
// first on a full tie, exactly like the reference.
type NovelHistoryProvider interface {
	HistoryCandidates(ctx context.Context, userID int64, keyword string, limit int) ([]map[string]any, error)
	HistoryTotal(ctx context.Context, userID int64, keyword string) (int, error)
}
