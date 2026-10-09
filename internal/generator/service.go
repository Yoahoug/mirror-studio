package generator

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"mirror-studio/internal/audit"
	"mirror-studio/internal/config"
	"mirror-studio/internal/crypt"
	"mirror-studio/internal/db"
	"mirror-studio/internal/media"
	"mirror-studio/internal/quota"
	"mirror-studio/internal/risk"
	"mirror-studio/internal/siteconfig"
)

// SubmitInput is one generation task submission after payload parsing.
type SubmitInput struct {
	UserID                int64
	ClientIP              string
	Prompt                string
	NegativePrompt        string
	StyleCode             string
	SizeCode              string
	QualityCode           string
	GenerationMode        string
	ReferenceImageDataURL string
	ReferenceImageName    string
	ReferenceUploadToken  string
	TopicID               *int64
	ClientRequestID       string
}

// Deps are the collaborator handles the service needs. main.go injects them.
type Deps struct {
	DB       *db.DB
	Config   *config.Config
	Site     *siteconfig.Service
	Location *time.Location
	Risk     *risk.Service
	Quota    *quota.Service
	Media    *media.Service
	Audit    *audit.Service
	Cipher   *crypt.FieldCipher
	// Log and Logger are both accepted: the architecture document spells the
	// field Log, the composition root passes Logger.
	Log    *slog.Logger
	Logger *slog.Logger
}

// Service owns the generation task state machine, channel dispatch, topics and
// the merged workspace history.
type Service struct {
	db       *db.DB
	cfg      *config.Config
	site     *siteconfig.Service
	location *time.Location
	risk     *risk.Service
	quota    *quota.Service
	media    *media.Service
	audit    *audit.Service
	cipher   *crypt.FieldCipher
	log      *slog.Logger

	novel NovelHistoryProvider

	workerMu     sync.Mutex
	workerCancel context.CancelFunc
	workerWG     sync.WaitGroup
}

// NewService builds the generator service.
func NewService(deps Deps) *Service {
	logger := deps.Log
	if logger == nil {
		logger = deps.Logger
	}
	if logger == nil {
		logger = slog.Default()
	}
	location := deps.Location
	if location == nil {
		location = time.UTC
	}
	return &Service{
		db:       deps.DB,
		cfg:      deps.Config,
		site:     deps.Site,
		location: location,
		risk:     deps.Risk,
		quota:    deps.Quota,
		media:    deps.Media,
		audit:    deps.Audit,
		cipher:   deps.Cipher,
		log:      logger,
	}
}

// SetNovelHistory wires the novel-visualisation side of the merged workspace
// history. It is a setter because internal/novel depends on this package.
func (s *Service) SetNovelHistory(provider NovelHistoryProvider) { s.novel = provider }

// ── submission ──

// Submit creates a generation task. The order of checks mirrors
// apps/generator/services.py submit_generation_task exactly: validation,
// idempotency, prompt safety, submission rate limit, daily and pending caps,
// channel resolution, quota freeze, then the row.
func (s *Service) Submit(ctx context.Context, in SubmitInput) (*Task, error) {
	prompt := strings.TrimSpace(in.Prompt)
	negativePrompt := strings.TrimSpace(in.NegativePrompt)
	clientRequestID := trimRunes(strings.TrimSpace(in.ClientRequestID), 64)
	styleCode := in.StyleCode
	if styleCode == "" {
		styleCode = "general"
	}
	sizeCode, err := normalizeSizeCode(in.SizeCode)
	if err != nil {
		return nil, err
	}
	qualityCode, err := normalizeQualityCode(in.QualityCode)
	if err != nil {
		return nil, err
	}
	generationMode := in.GenerationMode
	if generationMode == "" {
		generationMode = ModeTextToImage
	}
	referenceImageName := trimRunes(strings.TrimSpace(in.ReferenceImageName), maxReferenceNameRunes)
	uploadToken := strings.TrimSpace(in.ReferenceUploadToken)

	if len([]rune(prompt)) > maxPromptRunes {
		return nil, validationFailure("Prompt 过长")
	}
	if len([]rune(negativePrompt)) > maxNegativePromptRunes {
		return nil, validationFailure("反向提示词过长")
	}
	if !allowedStyles[styleCode] {
		return nil, validationFailure("不支持的风格")
	}
	if !allowedGenerationModes[generationMode] {
		return nil, validationFailure("不支持的生成模式")
	}

	var (
		referenceContent      []byte
		referenceMimeType     string
		referenceSourceBucket string
		referenceSourceKey    string
	)
	if uploadToken != "" {
		payload, err := resolveReferenceUploadToken(s.cfg.SecretKey, uploadToken, in.UserID)
		if err != nil {
			return nil, err
		}
		referenceMimeType = stringValue(payload["reference_image_mime_type"])
		if name := stringValue(payload["reference_image_name"]); name != "" {
			referenceImageName = name
		}
		referenceSourceBucket = stringValue(payload["reference_bucket_name"])
		referenceSourceKey = stringValue(payload["reference_object_key"])
	} else {
		content, mimeType, name, err := s.normalizeReferenceImage(in.ReferenceImageDataURL, in.ReferenceImageName)
		if err != nil {
			return nil, err
		}
		referenceContent, referenceMimeType, referenceImageName = content, mimeType, name
	}
	if generationMode == ModeImageToImage && len(referenceContent) == 0 && referenceSourceKey == "" {
		return nil, validationFailure(imageToImageNeedsPhoto)
	}
	if generationMode == ModeTextToImage {
		referenceContent, referenceMimeType, referenceImageName = nil, "", ""
		referenceSourceBucket, referenceSourceKey = "", ""
	}
	if referenceSourceKey != "" {
		content, err := s.media.ReadObject(ctx, referenceSourceBucket, referenceSourceKey)
		if err != nil {
			s.log.Warn("参考图上传凭证指向的对象不可读", "error", err)
			return nil, validationFailure("参考图上传凭证已失效，请重新上传")
		}
		content, mimeType, name, err := s.validateReferenceImageContent(content, referenceMimeType, referenceImageName)
		if err != nil {
			return nil, err
		}
		referenceContent, referenceMimeType, referenceImageName = content, mimeType, name
	}

	if _, err := s.quota.Ensure(ctx, in.UserID); err != nil {
		return nil, err
	}
	if clientRequestID != "" {
		existing, err := s.taskByClientRequestID(ctx, in.UserID, clientRequestID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return existing, nil
		}
	}
	// The reference calls ensure_prompt_safe / ensure_task_rate_limit inside its
	// atomic submit block, so the risk events those functions write are rolled
	// back together with the rejection — only login_failure rows ever persist.
	// The rewrite keeps that placement and writes nothing here; the *For
	// variants exist for callers that run outside a transaction.
	if err := s.risk.EnsurePromptSafe(ctx, prompt); err != nil {
		return nil, permissionFailure(err.Error())
	}
	if err := s.risk.ConsumeTaskQuota(ctx, in.UserID); err != nil {
		return nil, permissionFailure(err.Error())
	}

	dayStart, dayEnd := s.localDayRange()
	dailyCount, err := s.countDailyTasks(ctx, in.UserID, dayStart, dayEnd)
	if err != nil {
		return nil, err
	}
	if dailyCount >= s.dailyTaskLimit() {
		return nil, validationFailure("已达到今日任务上限")
	}
	pendingCount, err := s.countPendingTasks(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	if pendingCount >= s.pendingTaskLimit(ctx) {
		return nil, validationFailure("未完成任务过多，请稍后再试")
	}

	channel, err := s.defaultImageChannel(ctx)
	if err != nil {
		return nil, err
	}
	if channel == nil {
		return nil, validationFailure("未配置可用生图渠道")
	}

	topic, err := s.resolveTopic(ctx, in.UserID, in.TopicID, prompt)
	if err != nil {
		return nil, err
	}
	taskNo := generateTaskNo("G", time.Now())
	if _, err := s.quota.Freeze(ctx, in.UserID, 1, taskNo); err != nil {
		return nil, validationFailure(err.Error())
	}

	task := &Task{
		UserID:             in.UserID,
		TopicID:            topicPointer(topic),
		ChannelID:          channel.ID,
		TaskNo:             taskNo,
		ProgressPercent:    8,
		GenerationMode:     generationMode,
		Prompt:             prompt,
		NegativePrompt:     negativePrompt,
		StyleCode:          styleCode,
		SizeCode:           sizeCode,
		QualityCode:        qualityCode,
		ReferenceImageName: referenceImageName,
		QuotaCost:          1,
		ClientRequestID:    clientRequestID,
		ClientIP:           in.ClientIP,
		Channel:            channel,
	}
	if _, err := s.insertTask(ctx, task); err != nil {
		if isUniqueViolation(err) && clientRequestID != "" {
			s.log.Warn("并发提交命中幂等键，释放本次冻结", "task_no", taskNo, "client_request_id", clientRequestID)
			if releaseErr := s.quota.Release(ctx, in.UserID, 1, taskNo); releaseErr != nil {
				s.log.Error("释放重复提交冻结失败", "task_no", taskNo, "error", releaseErr)
			}
			existing, lookupErr := s.taskByClientRequestID(ctx, in.UserID, clientRequestID)
			if lookupErr == nil && existing != nil {
				return existing, nil
			}
		} else {
			// The reference's submit runs inside one transaction, so any failure
			// after the freeze rolls the freeze back with it; the release here is
			// the rewrite's equivalent for the row insert that failed.
			s.log.Error("任务写入失败，释放本次冻结", "task_no", taskNo, "error", err)
			if releaseErr := s.quota.Release(ctx, in.UserID, 1, taskNo); releaseErr != nil {
				s.log.Error("释放失败提交的冻结失败", "task_no", taskNo, "error", releaseErr)
			}
		}
		return nil, err
	}

	if len(referenceContent) > 0 {
		meta, err := s.media.StoreReferenceImage(ctx, in.UserID, taskNo, referenceContent, referenceMimeType)
		if err != nil {
			s.log.Error("参考图存储失败", "task_no", taskNo, "error", err)
			return nil, err
		}
		if err := s.updateTaskReference(ctx, task, meta); err != nil {
			s.log.Error("参考图元数据写入失败", "task_no", taskNo, "error", err)
			return nil, err
		}
	}
	if referenceSourceKey != "" {
		s.deleteStoredObject(ctx, referenceSourceBucket, referenceSourceKey)
	}
	if err := s.touchTopic(ctx, topic.ID, nil); err != nil {
		return nil, err
	}
	s.log.Info("生图任务已入队，等待 worker 拉取", "task_no", task.TaskNo)
	return task, nil
}

func topicPointer(topic *Topic) *int64 {
	if topic == nil {
		return nil
	}
	value := topic.ID
	return &value
}

// resolveTopic reuses the topic the client picked or creates one titled after
// the prompt.
func (s *Service) resolveTopic(ctx context.Context, userID int64, topicID *int64, prompt string) (*Topic, error) {
	if topicID != nil && *topicID != 0 {
		topic, err := s.topicByID(ctx, userID, *topicID)
		if err != nil {
			return nil, err
		}
		if topic == nil {
			return nil, validationFailure(topicMissingMessage)
		}
		return topic, nil
	}
	return s.insertTopic(ctx, userID, buildTopicTitle(prompt))
}

// buildTopicTitle mirrors build_topic_title: the first 24 characters of the
// stripped prompt, or the default title when that is empty.
func buildTopicTitle(prompt string) string {
	title := trimRunes(strings.TrimSpace(prompt), 24)
	if title == "" {
		return defaultTaskTitle
	}
	return title
}

func (s *Service) dailyTaskLimit() int {
	return s.site.Int("site.daily_user_task_limit", s.cfg.DailyUserTaskLimit)
}

func (s *Service) pendingTaskLimit(ctx context.Context) int {
	if s.quota == nil {
		return s.cfg.MaxPendingTasks
	}
	return s.quota.PendingTaskLimit(ctx)
}

func (s *Service) localDayRange() (time.Time, time.Time) {
	now := time.Now().In(s.location)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.location)
	return start.UTC(), start.AddDate(0, 0, 1).UTC()
}

func generateTaskNo(prefix string, now time.Time) string {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		copy(buffer, []byte{0, 0, 0, 1})
	}
	return fmt.Sprintf("%s%s%s", prefix, now.UTC().Format("20060102150405"), strings.ToUpper(hex.EncodeToString(buffer)))
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED")
}

// ── reference images ──

var referenceImageDataURLPattern = regexp.MustCompile(`(?i)^data:(image/(png|jpeg|jpg|webp));base64,([A-Za-z0-9+/=\s]+)$`)

// normalizeReferenceImage decodes an inline reference data URL. An empty data
// URL yields empty values, which the caller turns into 请先上传参考图.
func (s *Service) normalizeReferenceImage(dataURL, name string) ([]byte, string, string, error) {
	raw := strings.TrimSpace(dataURL)
	if raw == "" {
		return nil, "", "", nil
	}
	match := referenceImageDataURLPattern.FindStringSubmatch(raw)
	if match == nil {
		return nil, "", "", validationFailure("参考图仅支持 PNG、JPG、JPEG、WEBP")
	}
	mimeType := strings.ReplaceAll(strings.ToLower(match[1]), "jpg", "jpeg")
	encoded := strings.Join(strings.Fields(match[3]), "")
	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", "", validationFailure("参考图数据格式错误")
	}
	return s.validateReferenceImageContent(content, mimeType, name)
}

// validateReferenceImageContent checks the MIME type, the configured size cap
// and that the bytes really are an image.
func (s *Service) validateReferenceImageContent(content []byte, mimeType, name string) ([]byte, string, string, error) {
	normalizedMime := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(mimeType)), "jpg", "jpeg")
	rawName := referenceBaseName(strings.TrimSpace(name))
	switch normalizedMime {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return nil, "", "", validationFailure("参考图仅支持 PNG、JPG、JPEG、WEBP")
	}
	if int64(len(content)) > s.cfg.MaxReferenceImageBytes {
		return nil, "", "", validationFailure("参考图过大，请压缩后再试")
	}
	if _, _, ok := decodeAndValidateImage(content, normalizedMime); !ok {
		return nil, "", "", validationFailure("参考图无法识别，请重新上传")
	}
	return content, normalizedMime, rawName, nil
}

func referenceBaseName(name string) string {
	if name == "" {
		name = "reference-image"
	}
	trimmed := strings.TrimRight(name, "/")
	if index := strings.LastIndex(trimmed, "/"); index >= 0 {
		trimmed = trimmed[index+1:]
	}
	if trimmed == "" {
		trimmed = "reference-image"
	}
	return trimRunes(trimmed, maxReferenceNameRunes)
}

// loadTaskReferenceImage resolves the reference bytes for a dispatch, either
// from the object store or from an inline data URL.
func (s *Service) loadTaskReferenceImage(ctx context.Context, task *Task) ([]byte, string, error) {
	if task.ReferenceObjectKey != "" {
		content, err := s.media.ReadObject(ctx, task.ReferenceBucketName, task.ReferenceObjectKey)
		if err != nil {
			return nil, "", err
		}
		mimeType := strings.ToLower(strings.TrimSpace(task.ReferenceImageMimeType))
		if mimeType == "" {
			mimeType = "image/png"
		}
		return content, mimeType, nil
	}
	content, mimeType := decodeReferenceDataURL(task.ReferenceImageDataURL)
	if len(content) == 0 {
		return nil, "", fmt.Errorf("参考图解析失败")
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
	return content, mimeType, nil
}

// referenceMetaKeys is the insertion order StoreReferenceImage's meta map takes
// in the reference implementation; it keeps the signed token stable.
var referenceMetaKeys = []string{
	"reference_storage_provider",
	"reference_bucket_name",
	"reference_object_key",
	"reference_image_mime_type",
	"reference_image_size",
	"reference_image_width",
	"reference_image_height",
	"reference_image_sha256",
}

func createReferenceUploadToken(secretKey string, userID int64, referenceName string, meta map[string]string) string {
	payload := newOrderedObject()
	payload.set("user_id", userID)
	payload.set("reference_image_name", referenceName)
	for _, key := range referenceMetaKeys {
		if value, ok := meta[key]; ok {
			payload.set(key, value)
		}
	}
	remaining := []string{}
	for key := range meta {
		known := false
		for _, candidate := range referenceMetaKeys {
			if candidate == key {
				known = true
				break
			}
		}
		if !known {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	for _, key := range remaining {
		payload.set(key, meta[key])
	}
	signer := crypt.NewTimestampSigner(secretKey, referenceUploadTokenSalt)
	return signer.Sign(encodeCompactJSON(payload))
}

func resolveReferenceUploadToken(secretKey, token string, userID int64) (map[string]any, error) {
	signer := crypt.NewTimestampSigner(secretKey, referenceUploadTokenSalt)
	unsigned, err := signer.Unsign(token, referenceUploadTokenMaxAge)
	if err != nil {
		return nil, validationFailure("参考图上传凭证已失效，请重新上传")
	}
	// crypt.TimestampSigner.Unsign keeps the trailing ":<timestamp>" segment.
	payload := unsigned
	if index := strings.LastIndex(unsigned, ":"); index >= 0 {
		payload = unsigned[:index]
	}
	data := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return nil, validationFailure("参考图上传凭证已失效，请重新上传")
	}
	if intValue(data["user_id"]) != userID {
		return nil, permissionFailure("参考图上传凭证无效")
	}
	return data, nil
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return fmt.Sprintf("%v", typed)
	case bool:
		return fmt.Sprintf("%v", typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func intValue(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		return int64(atoi(typed))
	default:
		return 0
	}
}

// ── lifecycle ──

// TaskByNo returns the caller's task with its images attached. A task that does
// not exist, belongs to another user or is soft-deleted yields (nil, nil) so the
// handler can answer with the reference's HTML 404.
func (s *Service) TaskByNo(ctx context.Context, userID int64, taskNo string) (*Task, error) {
	task, err := s.taskForUser(ctx, userID, taskNo)
	if err != nil || task == nil {
		return nil, err
	}
	if err := s.attachImages(ctx, []*Task{task}); err != nil {
		return nil, err
	}
	channel, err := s.channelByIDTxIn(ctx, task.ChannelID)
	if err != nil {
		return nil, err
	}
	task.Channel = channel
	return task, nil
}

func (s *Service) channelByIDTxIn(ctx context.Context, id int64) (*Channel, error) {
	channel, err := scanChannel(s.db.QueryRow(`SELECT `+channelColumns+` FROM generator_imagechannel WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return channel, nil
}

// Cancel cancels a queued or running task and releases its frozen quota. The
// release happens inside cancelTask's transaction with the reference's
// 用户取消任务 remark.
func (s *Service) Cancel(ctx context.Context, userID int64, taskNo string) (*Task, error) {
	task, err := s.cancelTask(ctx, userID, taskNo)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, missingTask()
	}
	task.Images = nil
	return task, nil
}

// DeleteHistory soft-deletes a task, cancels it when it has not finished and
// marks its available images deleted. The release happens inside
// deleteTaskHistory's transaction with the reference's 用户删除运行中任务 remark.
func (s *Service) DeleteHistory(ctx context.Context, userID int64, taskNo string) error {
	task, err := s.deleteTaskHistory(ctx, userID, taskNo)
	if err != nil {
		return err
	}
	if task == nil {
		return missingTask()
	}
	if task.ReferenceObjectKey != "" {
		s.deleteStoredObject(ctx, task.ReferenceBucketName, task.ReferenceObjectKey)
	}
	return nil
}

// objectDeleter is the accessor internal/media needs to expose; until it does,
// deleting a stored object is skipped and logged.
type objectDeleter interface {
	DeleteObject(ctx context.Context, bucket, key string) error
}

func (s *Service) deleteStoredObject(ctx context.Context, bucket, key string) {
	if key == "" {
		return
	}
	deleter, ok := any(s.media).(objectDeleter)
	if !ok {
		s.log.Warn("media service exposes no object delete; object left for the media cleanup loop",
			"object_key", key)
		return
	}
	if err := deleter.DeleteObject(ctx, bucket, key); err != nil {
		s.log.Warn("存储对象删除失败", "object_key", key, "error", err)
	}
}

// reconcileTaskRuntimeState rescues a running task whose lease expired: it is
// requeued while the stall window is still open and failed once it is not.
func (s *Service) reconcileTaskRuntimeState(ctx context.Context, task *Task) *Task {
	if task == nil || task.Status != StatusRunning {
		return task
	}
	referenceTime := task.StartedAt
	if referenceTime == nil {
		if !task.CreatedAt.IsZero() {
			referenceTime = &task.CreatedAt
		} else if !task.SubmittedAt.IsZero() {
			referenceTime = &task.SubmittedAt
		}
	}
	if referenceTime == nil {
		return task
	}
	if err := s.attachChannel(ctx, task); err != nil {
		s.log.Warn("任务渠道加载失败", "task_no", task.TaskNo, "error", err)
		return task
	}
	now := time.Now().UTC()
	stalledTimeout := time.Duration(s.stalledTaskTimeoutSeconds(task)) * time.Second
	if task.LeaseExpiresAt != nil && !task.LeaseExpiresAt.After(now) {
		if now.Sub(*referenceTime) < stalledTimeout {
			requeued, err := s.requeueAfterWorkerLoss(ctx, task.TaskNo)
			if err != nil {
				s.log.Warn("任务重排失败", "task_no", task.TaskNo, "error", err)
				return task
			}
			if requeued != nil {
				requeued.Images = task.Images
				return requeued
			}
			return task
		}
	}
	if now.Sub(*referenceTime) < stalledTimeout {
		return task
	}
	reconciled, err := s.failTask(ctx, task, "task_stalled", "任务执行中断，已自动结束，请重新提交。", "", stalledRawResponse(referenceTime))
	if err != nil {
		s.log.Warn("卡住任务标记失败", "task_no", task.TaskNo, "error", err)
		return task
	}
	reconciled.Images = task.Images
	return reconciled
}

func stalledRawResponse(referenceTime *time.Time) any {
	raw := newOrderedObject()
	raw.set("stalled", true)
	raw.set("previous_status", StatusRunning)
	raw.set("reference_time", referenceTime.UTC().Format("2006-01-02T15:04:05.999999+00:00"))
	return raw
}

func (s *Service) attachChannel(ctx context.Context, task *Task) error {
	if task.Channel != nil {
		return nil
	}
	channel, err := s.channelByIDTxIn(ctx, task.ChannelID)
	if err != nil {
		return err
	}
	task.Channel = channel
	return nil
}

// failTask writes the terminal failure state and releases the frozen quota
// exactly once, inside the same transaction (setTaskFailure).
func (s *Service) failTask(ctx context.Context, task *Task, errorCode, errorMessage, providerRequestID string, rawResponse any) (*Task, error) {
	updated, err := s.setTaskFailure(ctx, task.TaskNo, errorCode, errorMessage, providerRequestID, rawResponse)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return task, nil
	}
	if s.audit != nil {
		payload := map[string]any{
			"task_no":             updated.TaskNo,
			"error_code":          errorCode,
			"error_message":       errorMessage,
			"provider_request_id": providerRequestID,
			"raw_response":        plainJSON(rawResponse),
		}
		if err := s.audit.LogCallbackEvent(ctx, "generation_failed", "generator", payload, false, trimRunes(errorMessage, 255)); err != nil {
			s.log.Warn("回调事件写入失败", "task_no", updated.TaskNo, "error", err)
		}
	}
	return updated, nil
}

// taskLeaseTimeoutSeconds is max(stalled timeout, 180).
func (s *Service) taskLeaseTimeoutSeconds(task *Task) int {
	timeout := s.stalledTaskTimeoutSeconds(task)
	if timeout < minLeaseSeconds {
		return minLeaseSeconds
	}
	return timeout
}

// stalledTaskTimeoutSeconds is the reference's stall window: the channel timeout
// plus a margin, raised further for image-to-image edits.
func (s *Service) stalledTaskTimeoutSeconds(task *Task) int {
	channelTimeout := defaultTimeoutSecs
	if task.Channel != nil && task.Channel.TimeoutSeconds > 0 {
		channelTimeout = task.Channel.TimeoutSeconds
	}
	running := channelTimeout + 120
	if running < minLeaseSeconds {
		running = minLeaseSeconds
	}
	if task.GenerationMode == ModeImageToImage {
		editSeconds := int(s.cfg.OpenAIImageEditTimeout / time.Second)
		if editSeconds <= 0 {
			editSeconds = 420
		}
		if running < editSeconds+60 {
			running = editSeconds + 60
		}
	}
	return running
}

// ── channel defaults ──

// EnsureDefaultChannels seeds the default image channel and, when an LLM key is
// configured, the default LLM channel. It runs on every boot and is idempotent.
func (s *Service) EnsureDefaultChannels(ctx context.Context) error {
	if err := s.ensureChannel(ctx, s.cfg.DefaultChannelCode, ChannelTypeImage); err != nil {
		return err
	}
	if strings.TrimSpace(s.cfg.ProviderAPIKey) != "" {
		if err := s.setChannelAPIKey(ctx, s.cfg.DefaultChannelCode, s.cfg.ProviderAPIKey); err != nil {
			return err
		}
	}
	if strings.TrimSpace(s.cfg.LLMAPIKey) == "" {
		return nil
	}
	if err := s.ensureChannel(ctx, s.cfg.LLMChannelCode, ChannelTypeLLM); err != nil {
		return err
	}
	return s.setChannelAPIKey(ctx, s.cfg.LLMChannelCode, s.cfg.LLMAPIKey)
}

func (s *Service) ensureChannel(ctx context.Context, channelCode, channelType string) error {
	vendor := VendorOpenAI
	if channelType == ChannelTypeImage && strings.ToLower(strings.TrimSpace(s.cfg.ProviderVendorCode)) == VendorSiliconFlow {
		vendor = VendorSiliconFlow
	}
	name := s.cfg.ProviderName
	providerName := s.cfg.ProviderVendor
	baseURL := s.cfg.ProviderBaseURL
	apiPath := s.cfg.ProviderAPIPath
	modelName := s.cfg.ProviderModelName
	timeout := s.cfg.ProviderTimeoutSeconds
	maxRetries := s.cfg.ProviderMaxRetries
	priority := 1
	editAPIPath := ""
	if channelType == ChannelTypeLLM {
		name = s.cfg.LLMProviderName
		providerName = s.cfg.LLMProviderVendor
		baseURL = s.cfg.LLMBaseURL
		apiPath = s.cfg.LLMAPIPath
		modelName = s.cfg.LLMModelName
		timeout = s.cfg.LLMTimeoutSeconds
		maxRetries = s.cfg.LLMMaxRetries
		priority = s.cfg.LLMPriority
		vendor = VendorOpenAI
	}
	now := db.FormatTime(time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO generator_imagechannel (
			created_at, updated_at, name, channel_code, base_url, api_path, api_key_encrypted, model_name,
			timeout_seconds, max_retries, is_active, priority, channel_type, provider_name, edit_api_path, provider_vendor
		) VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel_code) DO UPDATE SET
			name = excluded.name, base_url = excluded.base_url, api_path = excluded.api_path,
			model_name = excluded.model_name, timeout_seconds = excluded.timeout_seconds,
			max_retries = excluded.max_retries, is_active = excluded.is_active,
			priority = excluded.priority, channel_type = excluded.channel_type,
			provider_name = excluded.provider_name, edit_api_path = excluded.edit_api_path,
			provider_vendor = excluded.provider_vendor, updated_at = excluded.updated_at`,
		now, now, name, channelCode, baseURL, apiPath, modelName, timeout, maxRetries,
		db.Bool(true), priority, channelType, providerName, editAPIPath, vendor,
	)
	if err != nil {
		return err
	}
	s.log.Info("默认渠道已就绪", "channel_code", channelCode, "channel_type", channelType)
	return nil
}

func (s *Service) setChannelAPIKey(ctx context.Context, channelCode, apiKey string) error {
	encrypted, err := s.cipher.Encrypt(apiKey)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE generator_imagechannel SET api_key_encrypted = ?, updated_at = ? WHERE channel_code = ?`,
		encrypted, db.FormatTime(time.Now().UTC()), channelCode)
	return err
}

// ResolveChannel returns an active channel. With a code it returns that exact
// channel; without one it returns the highest-priority active LLM channel,
// which is what the novel visualisation and chat callers need.
func (s *Service) ResolveChannel(ctx context.Context, code string) (*Channel, error) {
	if strings.TrimSpace(code) == "" {
		return s.defaultLLMChannel(ctx)
	}
	return s.channelByCode(ctx, strings.TrimSpace(code))
}

// ── serialization ──

// SerializeTopic renders one topic for /api/generate/topics.
func (s *Service) SerializeTopic(topic *Topic) map[string]any {
	var lastTaskAt any
	if !topic.LastTaskAt.IsZero() {
		lastTaskAt = isoTime(topic.LastTaskAt)
	}
	var coverImageID any
	if topic.CoverImageID != nil {
		coverImageID = *topic.CoverImageID
	}
	return map[string]any{
		"id":             topic.ID,
		"title":          topic.Title,
		"cover_image_id": coverImageID,
		"last_task_at":   lastTaskAt,
	}
}

// SerializeTask renders one task exactly as api-contract §5.3 lists it. Image
// URLs are appended only when includeURLs is set and the image is available.
func (s *Service) SerializeTask(task *Task, userID int64, includeURLs bool) map[string]any {
	images := []any{}
	for index := range task.Images {
		image := task.Images[index]
		item := map[string]any{
			"id":         image.ID,
			"status":     image.Status,
			"width":      image.Width,
			"height":     image.Height,
			"expires_at": isoTimeOrNil(image.ExpiresAt),
		}
		if includeURLs && userID != 0 && image.Status == imageStatusAvailable {
			previewURL, err := s.media.IssueDownloadURL(context.Background(), &image, userID, true, true)
			if err != nil {
				s.log.Warn("预览下载链接签发失败", "image_id", image.ID, "error", err)
			} else {
				item["preview_url"] = previewURL
			}
			downloadURL, err := s.media.IssueDownloadURL(context.Background(), &image, userID, false, false)
			if err != nil {
				s.log.Warn("下载链接签发失败", "image_id", image.ID, "error", err)
			} else {
				item["download_url"] = downloadURL
			}
		}
		images = append(images, item)
	}
	var topicID any
	if task.TopicID != nil {
		topicID = *task.TopicID
	}
	errorMessage := ""
	if task.ErrorMessage != "" {
		errorMessage = buildPublicErrorMessage(task)
	}
	return map[string]any{
		"task_no":                  task.TaskNo,
		"status":                   task.Status,
		"progress_percent":         task.ProgressPercent,
		"generation_mode":          task.GenerationMode,
		"prompt":                   task.Prompt,
		"negative_prompt":          task.NegativePrompt,
		"style_code":               task.StyleCode,
		"size_code":                task.SizeCode,
		"quality_code":             task.QualityCode,
		"reference_image_name":     task.ReferenceImageName,
		"reference_image_attached": task.ReferenceAttached(),
		"reference_image_data_url": "",
		"quota_cost":               task.QuotaCost,
		"error_code":               task.ErrorCode,
		"error_message":            errorMessage,
		"topic_id":                 topicID,
		"submitted_at":             isoTimeOrNil(task.SubmittedAt),
		"started_at":               isoTimePtr(task.StartedAt),
		"finished_at":              isoTimePtr(task.FinishedAt),
		"images":                   images,
	}
}

func isoTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.999999+00:00")
}

func isoTimeOrNil(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return isoTime(value)
}

func isoTimePtr(value *time.Time) any {
	if value == nil {
		return nil
	}
	return isoTime(*value)
}

// ── workspace history ──

// HistoryPage merges generation and novel-visualisation history, newest first,
// ordered by (created_at, id) with the generation side winning a full tie.
func (s *Service) HistoryPage(ctx context.Context, userID int64, keyword string, page, pageSize int) (map[string]any, error) {
	limit := page * pageSize
	generation, err := s.listTasks(ctx, userID, nil, keyword, limit, 0)
	if err != nil {
		return nil, err
	}
	if err := s.attachImages(ctx, generation); err != nil {
		return nil, err
	}
	candidates := make([]historyCandidate, 0, len(generation))
	for _, task := range generation {
		candidates = append(candidates, historyCandidate{
			createdAt: task.CreatedAt, id: task.ID, kind: "generation", task: task,
		})
	}
	novelTotal := 0
	if s.novel != nil {
		total, err := s.novel.HistoryTotal(ctx, userID, keyword)
		if err != nil {
			return nil, err
		}
		novelTotal = total
		items, err := s.novel.HistoryCandidates(ctx, userID, keyword, limit)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			serialized, _ := item["__serialized"].(map[string]any)
			if serialized == nil {
				continue
			}
			candidates = append(candidates, historyCandidate{
				createdAt:  candidateTime(item["created_at"]),
				id:         intValue(item["id"]),
				kind:       "novelviz",
				serialized: serialized,
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if !left.createdAt.Equal(right.createdAt) {
			return left.createdAt.After(right.createdAt)
		}
		return left.id > right.id
	})

	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	items := []any{}
	for index := offset; index < len(candidates) && index < offset+pageSize; index++ {
		candidate := candidates[index]
		if candidate.kind == "novelviz" {
			items = append(items, candidate.serialized)
			continue
		}
		items = append(items, s.SerializeTask(s.reconcileTaskRuntimeState(ctx, candidate.task), userID, true))
	}
	generationTotal, err := s.countTasks(ctx, userID, nil, keyword)
	if err != nil {
		return nil, err
	}
	total := generationTotal + novelTotal
	return map[string]any{
		"items": items,
		"pagination": map[string]any{
			"page":         page,
			"page_size":    pageSize,
			"total":        total,
			"total_pages":  pageCount(total, pageSize),
			"has_next":     page*pageSize < total,
			"has_previous": page > 1,
		},
	}, nil
}

func candidateTime(value any) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC()
	case string:
		if parsed, ok := db.ParseTime(typed); ok {
			return parsed
		}
		if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func pageCount(total, pageSize int) int {
	if pageSize <= 0 {
		return 1
	}
	if total == 0 {
		return 1
	}
	return (total + pageSize - 1) / pageSize
}
