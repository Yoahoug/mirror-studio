package novel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mirror-studio/internal/config"
	"mirror-studio/internal/db"
	"mirror-studio/internal/siteconfig"
)

// Deps wires the novel domain to the rest of the service. Generator is the
// generator domain's LLM channel resolver (generator.Service.ResolveChannel
// behind a small adapter in main.go); Media is accepted for the frozen
// dependency shape and is not used because scene files live under MEDIA_ROOT,
// exactly where the reference puts them.
type Deps struct {
	DB        *db.DB
	Config    *config.Config
	Site      *siteconfig.Service
	Generator ChannelResolver
	Media     any
	Log       *slog.Logger
}

// Service owns the novel visualisation domain: EPUB uploads, the task worker,
// scene analysis, scene images and the SSE event stream.
type Service struct {
	db        *db.DB
	cfg       *config.Config
	site      *siteconfig.Service
	generator ChannelResolver
	media     any
	log       *slog.Logger
	settings  settings

	textClient  *http.Client
	imageClient *http.Client

	hub  *eventHub
	wake chan struct{}
	stop chan struct{}

	stopOnce sync.Once
	wg       sync.WaitGroup
	slots    chan struct{}

	now func() time.Time
}

func NewService(deps Deps) *Service {
	logger := deps.Log
	if logger == nil {
		logger = slog.Default()
	}
	textClient, imageClient := newProviderClients()
	service := &Service{
		db:          deps.DB,
		cfg:         deps.Config,
		site:        deps.Site,
		generator:   deps.Generator,
		media:       deps.Media,
		log:         logger,
		textClient:  textClient,
		imageClient: imageClient,
		wake:        make(chan struct{}, 1),
		stop:        make(chan struct{}),
		slots:       make(chan struct{}, workerTaskSlots),
		now:         func() time.Time { return time.Now().UTC() },
	}
	service.settings = loadSettings(deps.Config)
	service.hub = newEventHub(service.settings.eventLogLimit)
	// The SSE pulse runs for the lifetime of the service so stream handlers make
	// progress even when no task worker was started.
	service.wg.Add(1)
	go func() {
		defer service.wg.Done()
		service.hub.pulse(service.settings.ssePoll, service.stop)
	}()
	return service
}

// imageKeyError mirrors the reference's "未配置小说生图模型 Key" branch: the
// create endpoint reports it as 500/400501, the retry endpoint as 400100.
type imageKeyError struct{}

func (imageKeyError) Error() string { return "未配置小说生图模型 Key" }

const workerTaskSlots = 4

// StartWorker launches the dispatcher and the stall rescue loop.
func (s *Service) StartWorker(ctx context.Context) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.dispatchLoop(ctx)
	}()
}

// StopWorker stops the loops and waits for in-flight tasks to settle.
func (s *Service) StopWorker() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.wg.Wait()
}

// wakeWorker nudges the dispatcher after a task is queued.
func (s *Service) wakeWorker() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) dispatchLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
		s.rescueStalledTasks(ctx)
		s.drainQueue(ctx)
	}
}

func (s *Service) rescueStalledTasks(ctx context.Context) {
	ids, err := s.stalledTaskIDs(ctx, s.now())
	if err != nil {
		s.log.Warn("novelviz stalled scan failed", "error", err)
		return
	}
	for _, taskID := range ids {
		s.log.Warn("小说可视化任务疑似卡住，准备重新排队", "task_id", taskID)
		if err := s.requeueStalledTask(ctx, taskID, s.now()); err != nil {
			s.log.Warn("novelviz stalled requeue failed", "task_id", taskID, "error", err)
			continue
		}
		s.hub.invalidate(taskID)
		s.wakeWorker()
	}
}

func (s *Service) drainQueue(ctx context.Context) {
	for {
		select {
		case s.slots <- struct{}{}:
		case <-s.stop:
			return
		case <-ctx.Done():
			return
		}
		taskID, ok, err := s.claimQueuedTask(ctx, s.now())
		if err != nil {
			s.log.Error("novelviz task claim failed", "error", err)
			<-s.slots
			return
		}
		if !ok {
			<-s.slots
			return
		}
		s.hub.invalidate(taskID)
		s.wg.Add(1)
		go func(id int64) {
			defer s.wg.Done()
			defer func() { <-s.slots }()
			defer func() {
				if recovered := recover(); recovered != nil {
					s.log.Error("novelviz task panicked", "task_id", id, "panic", recovered)
					s.failTask(ctx, id, fmt.Sprintf("%v", recovered))
				}
			}()
			s.runTask(ctx, id)
		}(taskID)
	}
}

// ── task pipeline ──

// runTask mirrors services.run_novel_visualization_task.
func (s *Service) runTask(ctx context.Context, taskID int64) {
	task, err := s.loadTask(ctx, taskID)
	if err != nil {
		s.log.Warn("novelviz task row missing", "task_id", taskID, "error", err)
		s.hub.markMissing(taskID)
		return
	}
	isTargeted := task.isTargeted()
	startMessage := "小说可视化任务已启动"
	if isTargeted {
		startMessage = "指定剧情单图任务已启动"
	}
	if err := s.setTaskStatus(ctx, taskID, StatusRunning, intPointer(3), ""); err != nil {
		s.log.Warn("novelviz status write failed", "task_id", taskID, "error", err)
		return
	}
	s.appendEvent(ctx, taskID, "task_started", newPyObj().
		Set("message", startMessage).
		Set("progress_percent", 3))

	if !s.runImageOnlyRetry(ctx, task) {
		if err := s.runAnalysisPipeline(ctx, taskID, task, isTargeted); err != nil {
			s.failTask(ctx, taskID, err.Error())
			return
		}
	}
	s.finishTask(ctx, taskID)
}

func (s *Service) runAnalysisPipeline(ctx context.Context, taskID int64, task *Task, isTargeted bool) error {
	s.taskLog(ctx, taskID, "正在读取 EPUB："+task.BookFilename, 8)
	bookPath, err := s.resolveTaskBookPath(ctx, task)
	if err != nil {
		return err
	}
	novelText, err := extractEPUBText(bookPath)
	if err != nil {
		return err
	}
	llmConfig, err := s.resolveSceneLLMConfig(ctx, task.LLMChannelCode)
	if err != nil {
		return err
	}
	verb := "深度分析"
	if isTargeted {
		verb = "分析指定剧情"
	}
	textLength := len([]rune(novelText))
	analysisMessage := fmt.Sprintf("正文提取完成，共 %s 字，开始使用 %s %s", thousands(textLength), llmConfig.sourceName, verb)
	s.taskLog(ctx, taskID, analysisMessage, 18,
		eventField{Key: "text_length", Value: textLength},
		eventField{Key: "scene_mode", Value: task.SceneMode})

	targetPlot := ""
	if isTargeted {
		targetPlot = task.TargetPlot
	}
	scenes, err := s.requestScenePlan(ctx, novelText, task.LLMChannelCode, targetPlot)
	if err != nil {
		return err
	}
	if err := s.storeScenePlan(ctx, taskID, scenes); err != nil {
		return err
	}
	planMessage := fmt.Sprintf("已挑选 %d 个震撼场景，开始并发生成图片", len(scenes))
	if isTargeted {
		planMessage = "已定位指定剧情镜头，开始生成单张图片"
	}
	reloaded, err := s.loadTask(ctx, taskID)
	if err != nil {
		return err
	}
	s.appendEvent(ctx, taskID, "scene_plan_ready", newPyObj().
		Set("message", planMessage).
		Set("progress_percent", 36).
		Set("scenes", objectsAsAny(scenes)).
		Set("task", serializeTask(reloaded, false)))
	s.generateSceneImages(ctx, taskID)
	return nil
}

// storeScenePlan mirrors the transaction that replaces the scene rows.
func (s *Service) storeScenePlan(ctx context.Context, taskID int64, scenes []*pyObj) error {
	encoded, err := scenesJSON(scenes)
	if err != nil {
		return err
	}
	now := s.now()
	return s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(`UPDATE novelviz_novelvisualizationtask SET scenes_json = ?, progress_percent = 36, updated_at = ? WHERE id = ?`,
			encoded, db.FormatTime(now), taskID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM novelviz_novelsceneimage WHERE task_id = ?`, taskID); err != nil {
			return err
		}
		for index, scene := range scenes {
			title, chapter, significance, excerpt, prompt := sceneFromPlan(scene, index+1)
			if _, err := tx.Exec(`
				INSERT INTO novelviz_novelsceneimage (created_at, updated_at, scene_number, title, chapter,
					story_significance, excerpt, visual_prompt, status, error_message, image_path, image_url,
					preview_image_path, attempts, finished_at, task_id)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', '', '', 0, NULL, ?)`,
				db.FormatTime(now), db.FormatTime(now), index+1, title, chapter, significance, excerpt, prompt,
				SceneStatusPending, taskID); err != nil {
				return err
			}
		}
		return nil
	})
}

// runImageOnlyRetry mirrors services._run_image_only_retry.
func (s *Service) runImageOnlyRetry(ctx context.Context, task *Task) bool {
	if !s.prepareExistingSceneRetry(ctx, task) {
		return false
	}
	if task.Progress < 36 {
		if err := s.updateTaskProgress(ctx, task.ID, 36, s.now()); err != nil {
			s.log.Warn("novelviz progress write failed", "task_id", task.ID, "error", err)
		}
	}
	reloaded, err := s.loadTask(ctx, task.ID)
	if err != nil {
		return false
	}
	s.appendEvent(ctx, task.ID, "scene_plan_ready", newPyObj().
		Set("message", "已保留上次分析结果，开始重新生成失败图片").
		Set("progress_percent", 36).
		Set("scenes", objectsAsAny(task.Scenes)).
		Set("task", serializeTask(reloaded, false)))
	s.generateSceneImages(ctx, task.ID)
	return true
}

// prepareExistingSceneRetry mirrors services._prepare_existing_scene_retry.
func (s *Service) prepareExistingSceneRetry(ctx context.Context, task *Task) bool {
	if len(task.Scenes) == 0 {
		return false
	}
	existing := map[int]bool{}
	for _, image := range task.Images {
		existing[image.SceneNumber] = true
	}
	now := s.now()
	for index, scene := range task.Scenes {
		number := index + 1
		if existing[number] {
			continue
		}
		title, chapter, significance, excerpt, prompt := sceneFromPlan(scene, number)
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO novelviz_novelsceneimage (created_at, updated_at, scene_number, title, chapter,
				story_significance, excerpt, visual_prompt, status, error_message, image_path, image_url,
				preview_image_path, attempts, finished_at, task_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', '', '', 0, NULL, ?)`,
			db.FormatTime(now), db.FormatTime(now), number, title, chapter, significance, excerpt, prompt,
			SceneStatusPending, task.ID); err != nil {
			s.log.Warn("novelviz scene insert failed", "task_id", task.ID, "error", err)
			return false
		}
	}
	var remaining int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM novelviz_novelsceneimage WHERE task_id = ? AND status IN (?, ?, ?)`,
		task.ID, SceneStatusPending, SceneStatusRunning, SceneStatusFailed).Scan(&remaining); err != nil {
		return false
	}
	return remaining > 0
}

// finishTask mirrors the summary block of run_novel_visualization_task.
func (s *Service) finishTask(ctx context.Context, taskID int64) {
	succeeded, err := s.countScenesByStatus(ctx, taskID, SceneStatusSucceeded)
	if err != nil {
		s.log.Warn("novelviz succeeded count failed", "task_id", taskID, "error", err)
		return
	}
	failed, err := s.countScenesByStatus(ctx, taskID, SceneStatusFailed)
	if err != nil {
		s.log.Warn("novelviz failed count failed", "task_id", taskID, "error", err)
		return
	}
	// A successful run leaves error_message empty: the summary belongs to the
	// event payload only, which is what the reference stores.
	status := StatusFailed
	errorMessage := "全部场景图片生成失败"
	message := errorMessage
	if succeeded > 0 {
		status = StatusSucceeded
		errorMessage = ""
		message = fmt.Sprintf("任务完成：成功 %d 张，失败 %d 张", succeeded, failed)
	}
	if err := s.setTaskStatus(ctx, taskID, status, intPointer(100), errorMessage); err != nil {
		s.log.Warn("novelviz status write failed", "task_id", taskID, "error", err)
		return
	}
	eventType := "task_failed"
	if succeeded > 0 {
		eventType = "task_done"
	}
	payload := newPyObj().Set("message", message).Set("progress_percent", 100)
	if reloaded, err := s.loadTask(ctx, taskID); err == nil {
		payload.Set("task", serializeTask(reloaded, false))
	}
	s.appendEvent(ctx, taskID, eventType, payload)
}

// failTask mirrors the failure branch of run_novel_visualization_task.
func (s *Service) failTask(ctx context.Context, taskID int64, reason string) {
	message := truncateRunes(reason, 255)
	if message == "" {
		message = "小说可视化任务失败"
	}
	if err := s.setTaskStatus(ctx, taskID, StatusFailed, intPointer(100), message); err != nil {
		s.log.Warn("novelviz status write failed", "task_id", taskID, "error", err)
		return
	}
	payload := newPyObj().Set("message", message).Set("progress_percent", 100)
	if reloaded, err := s.loadTask(ctx, taskID); err == nil {
		payload.Set("task", serializeTask(reloaded, false))
	}
	s.appendEvent(ctx, taskID, "task_failed", payload)
}

// ── scene images ──

// generateSceneImages mirrors services._generate_scene_images with a buffered
// channel semaphore instead of asyncio (review P1-27).
func (s *Service) generateSceneImages(ctx context.Context, taskID int64) {
	concurrency := s.settings.concurrency
	semaphore := make(chan struct{}, concurrency)
	sceneIDs, err := s.pendingSceneIDs(ctx, taskID)
	if err != nil {
		s.log.Warn("novelviz scene load failed", "task_id", taskID, "error", err)
		return
	}
	// Claim and announce every scene in scene order before the concurrent fan-out.
	// Announcing from inside the goroutines makes the order depend on scheduler
	// timing; the reference's worker pool reports the scenes in submission order,
	// and the event stream is part of what the frontend renders.
	claimed := make([]int64, 0, len(sceneIDs))
	for _, sceneID := range sceneIDs {
		started, err := s.markSceneRunning(ctx, sceneID, 1)
		if err != nil {
			s.log.Warn("novelviz scene claim failed", "scene_id", sceneID, "error", err)
			continue
		}
		s.appendEvent(ctx, taskID, "image_started", newPyObj().
			Set("message", fmt.Sprintf("开始生成第 %d 张：%s", started.SceneNumber, started.Title)).
			Set("scene", serializeSceneImage(started)))
		claimed = append(claimed, sceneID)
	}

	// Generating is concurrent, but the per-scene bookkeeping (file write, row
	// update, event) happens in scene order: scene N commits only after scene
	// N-1 has. Otherwise two scenes finishing together would report their
	// progress in whichever order the scheduler happened to run, and the
	// progress the frontend renders would flap between runs. The order is
	// enforced with a chain of gates, and every gate is handed on in a defer so
	// a failed or panicking scene cannot strand the ones behind it.
	gates := make([]chan struct{}, len(claimed))
	for index := range gates {
		gates[index] = make(chan struct{})
	}
	if len(gates) > 0 {
		close(gates[0])
	}
	releaseNext := func(index int) {
		if index+1 >= len(gates) {
			return
		}
		select {
		case <-gates[index+1]:
		default:
			close(gates[index+1])
		}
	}

	// The concurrency slot is taken by this loop, not by the goroutine: a worker
	// that held a slot while waiting for its turn would deadlock against the
	// worker whose turn it is waiting for as soon as the pool is saturated.
	var wait sync.WaitGroup
	for index, sceneID := range claimed {
		semaphore <- struct{}{}
		wait.Add(1)
		go func(index int, id int64) {
			defer wait.Done()
			defer func() { <-semaphore }()
			defer releaseNext(index)
			defer func() {
				if recovered := recover(); recovered != nil {
					s.log.Error("novelviz scene generation panicked", "task_id", taskID, "scene_id", id, "panic", recovered)
				}
			}()
			waitTurn := func() { <-gates[index] }
			s.generateOneScene(ctx, taskID, id, true, waitTurn)
		}(index, sceneID)
	}
	wait.Wait()
}

// generateOneScene mirrors services._generate_one_scene. `claimed` says the
// caller already moved the scene to running and announced it, which is how the
// first attempt is reported in scene order.
func (s *Service) generateOneScene(ctx context.Context, taskID, sceneID int64, claimed bool, waitTurn func()) {
	image, err := s.sceneImageByID(ctx, sceneID)
	if err != nil {
		return
	}
	prompt := prepareScenePrompt(image)
	maxRetries := s.settings.imageRetries
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if !(claimed && attempt == 1) {
			started, err := s.markSceneRunning(ctx, sceneID, attempt)
			if err != nil {
				s.log.Warn("novelviz scene claim failed", "scene_id", sceneID, "error", err)
				return
			}
			s.appendEvent(ctx, taskID, "image_started", newPyObj().
				Set("message", fmt.Sprintf("开始生成第 %d 张：%s", started.SceneNumber, started.Title)).
				Set("scene", serializeSceneImage(started)))
		}

		content, generationErr := s.postImageGeneration(ctx, prompt)
		if generationErr == nil {
			// Take this scene's turn before the bookkeeping, so the progress each
			// image_done carries is 36+60*finished/total with `finished` growing
			// in scene order. The upstream call above is what dominates the
			// runtime and stays fully concurrent.
			waitTurn()
			payload, progress, err := s.markSceneSucceeded(ctx, sceneID, content, taskID)
			if err == nil {
				s.appendEvent(ctx, taskID, "image_done", newPyObj().
					Set("message", fmt.Sprintf("第 %d 张已完成", payload.SceneNumber)).
					Set("progress_percent", progress).
					Set("scene", serializeSceneImage(payload)))
				return
			}
			generationErr = err
		}
		errorMessage := truncateRunes(strings.TrimSpace(generationErr.Error()), 255)
		if errorMessage == "" {
			errorMessage = "生成失败"
		}
		failedScene, sceneNumber, err := s.markSceneAttemptFailed(ctx, sceneID, errorMessage)
		if err != nil {
			return
		}
		retryMessage := fmt.Sprintf("第 %d 张生成失败", sceneNumber)
		if attempt < maxRetries {
			retryMessage = fmt.Sprintf("第 %d 张生成失败，准备重试", sceneNumber)
		}
		s.appendEvent(ctx, taskID, "log", newPyObj().
			Set("message", retryMessage).
			Set("scene_id", failedScene.ID).
			Set("attempt", attempt).
			Set("error", errorMessage))
		if isAuditError(errorMessage) {
			prompt = rewritePromptForSafety(prompt)
		} else if attempt < maxRetries {
			prompt = rewritePromptForRetry(prompt, errorMessage)
		}
		if attempt < maxRetries {
			if !isRetryableProviderError(generationErr) {
				break
			}
			sleepContext(ctx, time.Duration(minInt(3*attempt, 8))*time.Second)
		}
	}
	waitTurn()
	payload, progress, err := s.markSceneFailed(ctx, sceneID)
	if err != nil {
		return
	}
	s.appendEvent(ctx, taskID, "image_failed", newPyObj().
		Set("message", fmt.Sprintf("第 %d 张最终失败", payload.SceneNumber)).
		Set("progress_percent", progress).
		Set("scene", serializeSceneImage(payload)))
}

func (s *Service) markSceneRunning(ctx context.Context, sceneID int64, attempt int) (SceneImage, error) {
	now := s.now()
	if _, err := s.db.ExecContext(ctx, `UPDATE novelviz_novelsceneimage SET status = ?, attempts = ?, error_message = '', updated_at = ? WHERE id = ?`,
		SceneStatusRunning, attempt, db.FormatTime(now), sceneID); err != nil {
		return SceneImage{}, err
	}
	return s.sceneImageByID(ctx, sceneID)
}

func (s *Service) markSceneAttemptFailed(ctx context.Context, sceneID int64, errorMessage string) (SceneImage, int, error) {
	now := s.now()
	if _, err := s.db.ExecContext(ctx, `UPDATE novelviz_novelsceneimage SET error_message = ?, updated_at = ? WHERE id = ?`,
		truncateRunes(errorMessage, 255), db.FormatTime(now), sceneID); err != nil {
		return SceneImage{}, 0, err
	}
	image, err := s.sceneImageByID(ctx, sceneID)
	if err != nil {
		return SceneImage{}, 0, err
	}
	return image, image.SceneNumber, nil
}

// markSceneSucceeded mirrors services._mark_scene_succeeded: the file and its
// thumbnail are written before the row is updated.
func (s *Service) markSceneSucceeded(ctx context.Context, sceneID int64, content []byte, taskID int64) (SceneImage, int, error) {
	image, err := s.sceneImageByID(ctx, sceneID)
	if err != nil {
		return SceneImage{}, 0, err
	}
	var userID int64
	if err := s.db.QueryRowContext(ctx, `SELECT user_id FROM novelviz_novelvisualizationtask WHERE id = ?`, taskID).Scan(&userID); err != nil {
		return SceneImage{}, 0, err
	}
	relativePath := filepath.ToSlash(filepath.Join("novelviz", "users", strconv.FormatInt(userID, 10), "tasks",
		strconv.FormatInt(taskID, 10), fmt.Sprintf("scene-%02d.png", image.SceneNumber)))
	mediaRoot, err := resolveAbsolute(s.settings.mediaRoot)
	if err != nil {
		return SceneImage{}, 0, err
	}
	absolutePath := filepath.Join(mediaRoot, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return SceneImage{}, 0, err
	}
	if err := os.WriteFile(absolutePath, content, 0o644); err != nil {
		return SceneImage{}, 0, err
	}
	previewPath, _ := s.buildScenePreviewFiles(content, relativePath)
	now := s.now()
	imageURL := fmt.Sprintf("/api/novelviz/tasks/%d/images/%d.png", taskID, image.ID)
	if _, err := s.db.ExecContext(ctx, `
		UPDATE novelviz_novelsceneimage
		SET status = ?, image_path = ?, image_url = ?, preview_image_path = ?, error_message = '',
			finished_at = ?, updated_at = ?
		WHERE id = ?`,
		SceneStatusSucceeded, relativePath, imageURL, previewPath, db.FormatTime(now), db.FormatTime(now), sceneID); err != nil {
		return SceneImage{}, 0, err
	}
	updated, err := s.sceneImageByID(ctx, sceneID)
	if err != nil {
		return SceneImage{}, 0, err
	}
	progress, err := s.calculateImageProgress(ctx, taskID)
	if err != nil {
		progress = 36
	}
	return updated, progress, nil
}

func (s *Service) markSceneFailed(ctx context.Context, sceneID int64) (SceneImage, int, error) {
	now := s.now()
	if _, err := s.db.ExecContext(ctx, `UPDATE novelviz_novelsceneimage SET status = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
		SceneStatusFailed, db.FormatTime(now), db.FormatTime(now), sceneID); err != nil {
		return SceneImage{}, 0, err
	}
	image, err := s.sceneImageByID(ctx, sceneID)
	if err != nil {
		return SceneImage{}, 0, err
	}
	progress, err := s.calculateImageProgress(ctx, image.TaskID)
	if err != nil {
		progress = 36
	}
	return image, progress, nil
}

// calculateImageProgress mirrors services._calculate_image_progress, including
// Python's round-half-to-even.
func (s *Service) calculateImageProgress(ctx context.Context, taskID int64) (int, error) {
	total, err := s.countScenes(ctx, taskID)
	if err != nil {
		return 0, err
	}
	if total <= 0 {
		return 36, nil
	}
	finished, err := s.countFinishedScenes(ctx, taskID)
	if err != nil {
		return 0, err
	}
	progress := 36 + int(math.RoundToEven(float64(finished)/float64(total)*60))
	if progress > 96 {
		progress = 96
	}
	return progress, nil
}

// ── events ──

type eventField struct {
	Key   string
	Value any
}

// appendEvent mirrors services.append_task_event: the id comes from the row's
// last_event_id inside a write transaction, and the log is capped.
func (s *Service) appendEvent(ctx context.Context, taskID int64, eventType string, payload *pyObj) (*Event, error) {
	safePayload := s.fitEventPayload(payload)
	now := s.now()
	var (
		event  Event
		status string
		events []Event
	)
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		var (
			lastID   int64
			logsRaw  string
			current  string
			progress int
		)
		if err := tx.QueryRow(`SELECT status, last_event_id, logs, progress_percent FROM novelviz_novelvisualizationtask WHERE id = ?`, taskID).
			Scan(&current, &lastID, &logsRaw, &progress); err != nil {
			return err
		}
		existing, err := eventsFromJSON(logsRaw)
		if err != nil {
			return err
		}
		lastID++
		event = Event{ID: lastID, Type: eventType, Payload: safePayload, CreatedAt: isoformat(now)}
		existing = append(existing, event)
		if len(existing) > s.settings.eventLogLimit {
			existing = existing[len(existing)-s.settings.eventLogLimit:]
		}
		encoded, err := eventsJSON(existing)
		if err != nil {
			return err
		}
		nextProgress := progress
		hasProgress := false
		if value, ok := safePayload.Get("progress_percent"); ok {
			if parsed, ok := pythonInt(value); ok {
				nextProgress = parsed
				if nextProgress < 0 {
					nextProgress = 0
				}
				if nextProgress > 100 {
					nextProgress = 100
				}
				hasProgress = true
			}
		}
		if hasProgress {
			_, err = tx.Exec(`UPDATE novelviz_novelvisualizationtask SET last_event_id = ?, logs = ?, progress_percent = ?, updated_at = ? WHERE id = ?`,
				lastID, encoded, nextProgress, db.FormatTime(now), taskID)
		} else {
			_, err = tx.Exec(`UPDATE novelviz_novelvisualizationtask SET last_event_id = ?, logs = ?, updated_at = ? WHERE id = ?`,
				lastID, encoded, db.FormatTime(now), taskID)
		}
		if err != nil {
			return err
		}
		status = current
		events = existing
		return nil
	})
	if err != nil {
		s.log.Warn("novelviz event append failed", "task_id", taskID, "type", eventType, "error", err)
		return nil, err
	}
	s.hub.store(taskID, status, event.ID, events)
	return &event, nil
}

func (s *Service) taskLog(ctx context.Context, taskID int64, message string, progress int, fields ...eventField) {
	payload := newPyObj().Set("message", message)
	for _, field := range fields {
		payload.Set(field.Key, field.Value)
	}
	payload.Set("progress_percent", progress)
	s.appendEvent(ctx, taskID, "log", payload)
}

// setTaskStatus mirrors services._set_task_status.
func (s *Service) setTaskStatus(ctx context.Context, taskID int64, status string, progress *int, errorMessage string) error {
	now := s.now()
	sets := []string{"status = ?", "error_message = ?", "updated_at = ?"}
	args := []any{status, truncateRunes(errorMessage, 255), db.FormatTime(now)}
	if progress != nil {
		sets = append(sets, "progress_percent = ?")
		args = append(args, *progress)
	}
	if status == StatusRunning {
		sets = append(sets, "started_at = COALESCE(started_at, ?)")
		args = append(args, db.FormatTime(now))
	}
	if status == StatusSucceeded || status == StatusFailed || status == StatusCanceled {
		sets = append(sets, "finished_at = ?")
		args = append(args, db.FormatTime(now))
	}
	args = append(args, taskID)
	if _, err := s.db.ExecContext(ctx, `UPDATE novelviz_novelvisualizationtask SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		return err
	}
	s.hub.setStatus(taskID, status)
	return nil
}

// ── task creation and retry ──

// SubmitInput carries the create payload the handler validated.
type SubmitInput struct {
	UserID         int64
	BookSource     string
	BookFilename   string
	UploadedBookID int64
	LLMChannelCode string
	SceneMode      string
	TargetPlot     string
}

// Submit mirrors the POST body of NovelTaskListCreateAPIView.
func (s *Service) Submit(ctx context.Context, in SubmitInput) (*Task, error) {
	bookSource := strings.TrimSpace(in.BookSource)
	if bookSource == "" {
		bookSource = SourceBuiltin
	}
	bookFilename := strings.TrimSpace(in.BookFilename)
	uploadedBookID := in.UploadedBookID
	llmChannelCode := truncateRunes(strings.TrimSpace(in.LLMChannelCode), 64)
	sceneMode := strings.TrimSpace(in.SceneMode)
	if sceneMode == "" {
		sceneMode = ModeAuto
	}
	targetPlot := strings.TrimSpace(in.TargetPlot)

	if bookSource == SourceUploaded {
		path, err := s.resolveUploadedBookPath(ctx, uploadedBookID, in.UserID)
		if err != nil {
			return nil, err
		}
		if bookFilename == "" {
			bookFilename = filepath.Base(path)
		}
	} else {
		bookSource = SourceBuiltin
		uploadedBookID = 0
		if _, err := resolveBookPath(s.settings.bookDir, bookFilename); err != nil {
			return nil, err
		}
	}
	if sceneMode != ModeAuto && sceneMode != ModeTargeted {
		sceneMode = ModeAuto
	}
	if sceneMode == ModeTargeted {
		if len([]rune(targetPlot)) < 4 {
			return nil, &ValueError{Message: "请填写想呈现的剧情"}
		}
		targetPlot = truncateRunes(targetPlot, 2000)
	} else {
		targetPlot = ""
	}
	if _, err := s.resolveSceneLLMConfig(ctx, llmChannelCode); err != nil {
		return nil, err
	}
	if s.settings.imageAPIKey == "" {
		return nil, imageKeyError{}
	}

	now := s.now()
	var uploadedValue any
	if uploadedBookID != 0 {
		uploadedValue = uploadedBookID
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO novelviz_novelvisualizationtask (created_at, updated_at, book_filename, status, progress_percent,
			logs, scenes_json, error_message, started_at, finished_at, last_event_id, user_id, llm_channel_code,
			book_source, uploaded_book_id, scene_mode, target_plot)
		VALUES (?, ?, ?, ?, 1, '[]', '[]', '', NULL, NULL, 0, ?, ?, ?, ?, ?, ?)`,
		db.FormatTime(now), db.FormatTime(now), bookFilename, StatusQueued, in.UserID, llmChannelCode,
		bookSource, uploadedValue, sceneMode, targetPlot)
	if err != nil {
		return nil, err
	}
	taskID, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	s.hub.invalidate(taskID)
	s.wakeWorker()
	return s.loadTask(ctx, taskID)
}

// ResetTaskForRetry mirrors services.reset_task_for_retry. Validation happens
// before the write transaction opens because SQLite serves the whole service
// from a single connection; the status is re-checked inside it.
func (s *Service) ResetTaskForRetry(ctx context.Context, taskID, userID int64) (*Task, error) {
	task, err := s.loadTaskForUser(ctx, taskID, userID)
	if err != nil {
		return nil, err
	}
	if task.Status != StatusFailed && task.Status != StatusCanceled {
		return nil, &VisualizationError{Message: "只有失败或已取消的小说可视化任务可以重试"}
	}
	if s.settings.imageAPIKey == "" {
		return nil, &VisualizationError{Message: "未配置小说生图模型 Key"}
	}
	hasScenePlan := len(task.Scenes) > 0 && len(task.Images) > 0
	progress := 1
	logs := []Event{}
	scenes := []*pyObj{}
	if hasScenePlan {
		progress = 36
		logs = task.Logs
		if len(logs) > 30 {
			logs = logs[len(logs)-30:]
		}
		scenes = task.Scenes
	} else {
		if _, err := s.resolveTaskBookPath(ctx, task); err != nil {
			return nil, err
		}
		if _, err := s.resolveSceneLLMConfig(ctx, task.LLMChannelCode); err != nil {
			return nil, err
		}
	}

	now := s.now()
	scenesEncoded, err := scenesJSON(scenes)
	if err != nil {
		return nil, err
	}
	logsEncoded, err := eventsJSON(logs)
	if err != nil {
		return nil, err
	}
	lastEventID := int64(0)
	for _, event := range logs {
		if event.ID > lastEventID {
			lastEventID = event.ID
		}
	}
	err = s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		var status string
		if err := tx.QueryRow(`SELECT status FROM novelviz_novelvisualizationtask WHERE id = ? AND user_id = ?`, taskID, userID).Scan(&status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errNotFound
			}
			return err
		}
		if status != StatusFailed && status != StatusCanceled {
			return &VisualizationError{Message: "只有失败或已取消的小说可视化任务可以重试"}
		}
		if hasScenePlan {
			if _, err := tx.Exec(`
				UPDATE novelviz_novelsceneimage
				SET status = ?, error_message = '', image_path = '', image_url = '', attempts = 0,
					finished_at = NULL, updated_at = ?
				WHERE task_id = ? AND status != ?`,
				SceneStatusPending, db.FormatTime(now), taskID, SceneStatusSucceeded); err != nil {
				return err
			}
		} else if _, err := tx.Exec(`DELETE FROM novelviz_novelsceneimage WHERE task_id = ?`, taskID); err != nil {
			return err
		}
		_, err := tx.Exec(`
			UPDATE novelviz_novelvisualizationtask
			SET status = ?, progress_percent = ?, logs = ?, scenes_json = ?, error_message = '',
				started_at = NULL, finished_at = NULL, last_event_id = ?, updated_at = ?
			WHERE id = ?`,
			StatusQueued, progress, logsEncoded, scenesEncoded, lastEventID, db.FormatTime(now), taskID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.hub.invalidate(taskID)
	s.wakeWorker()
	return s.loadTask(ctx, taskID)
}

// ── uploads ──

// StoreUploadedEPUBFile mirrors services.store_uploaded_epub for the multipart
// endpoint. The file is removed again when the row cannot be written.
func (s *Service) StoreUploadedEPUBFile(ctx context.Context, source io.Reader, filename string, userID int64) (*Book, error) {
	displayName, err := cleanEPUBDisplayName(filename)
	if err != nil {
		return nil, err
	}
	relativePath, absolutePath, err := s.uploadedTargetPath(displayName, userID)
	if err != nil {
		return nil, err
	}
	created := false
	defer func() {
		if !created {
			_ = os.Remove(absolutePath)
		}
	}()

	handle, err := os.Create(absolutePath)
	if err != nil {
		return nil, err
	}
	digest := sha256.New()
	total := int64(0)
	buffer := make([]byte, 64*1024)
	for {
		read, readErr := source.Read(buffer)
		if read > 0 {
			total += int64(read)
			if total > s.settings.maxEPUBBytes {
				handle.Close()
				return nil, maxEPUBBytesError(s.settings.maxEPUBBytes)
			}
			digest.Write(buffer[:read])
			if _, err := handle.Write(buffer[:read]); err != nil {
				handle.Close()
				return nil, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			handle.Close()
			return nil, readErr
		}
	}
	if err := handle.Close(); err != nil {
		return nil, err
	}
	if total <= 0 {
		return nil, &ValueError{Message: "上传的 EPUB 文件为空"}
	}
	book, err := s.insertUploadedBook(ctx, userID, displayName, relativePath, total, hex.EncodeToString(digest.Sum(nil)))
	if err != nil {
		return nil, err
	}
	created = true
	return book, nil
}

// StoreUploadedEPUBPath mirrors services.store_uploaded_epub_path, which the
// resumable upload session calls when an assembled "novel_epub" session
// completes.
func (s *Service) StoreUploadedEPUBPath(ctx context.Context, sourcePath, displayName string, userID int64, sha256Sum string) (*Book, error) {
	cleanName, err := cleanEPUBDisplayName(displayName)
	if err != nil {
		return nil, err
	}
	if !isRegularFile(sourcePath) {
		return nil, &ValueError{Message: "上传的 EPUB 文件不存在"}
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil, err
	}
	total := info.Size()
	if total <= 0 {
		return nil, &ValueError{Message: "上传的 EPUB 文件为空"}
	}
	if total > s.settings.maxEPUBBytes {
		return nil, maxEPUBBytesError(s.settings.maxEPUBBytes)
	}
	if sha256Sum == "" {
		digest := sha256.New()
		handle, err := os.Open(sourcePath)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(digest, handle); err != nil {
			handle.Close()
			return nil, err
		}
		handle.Close()
		sha256Sum = hex.EncodeToString(digest.Sum(nil))
	}
	relativePath, absolutePath, err := s.uploadedTargetPath(cleanName, userID)
	if err != nil {
		return nil, err
	}
	created := false
	defer func() {
		if !created {
			_ = os.Remove(absolutePath)
		}
	}()
	source, err := os.Open(sourcePath)
	if err != nil {
		return nil, err
	}
	target, err := os.Create(absolutePath)
	if err != nil {
		source.Close()
		return nil, err
	}
	if _, err := io.Copy(target, source); err != nil {
		source.Close()
		target.Close()
		return nil, err
	}
	source.Close()
	if err := target.Close(); err != nil {
		return nil, err
	}
	book, err := s.insertUploadedBook(ctx, userID, cleanName, relativePath, total, sha256Sum)
	if err != nil {
		return nil, err
	}
	created = true
	return book, nil
}

// ListBooks mirrors services.list_epub_books.
func (s *Service) ListBooks(ctx context.Context, userID int64) ([]Book, error) {
	books := s.listBuiltinBooks()
	if userID > 0 {
		uploaded, err := s.listUploadedBooks(ctx, userID, 50)
		if err != nil {
			return nil, err
		}
		books = append(books, uploaded...)
	}
	return books, nil
}

// SerializeBook mirrors views._serialize_book.
func SerializeBook(book Book) *pyObj { return serializeBook(book) }

// TaskByIDForUser loads a task with its images, reporting errNotFound when the
// row does not exist or belongs to another user.
func (s *Service) TaskByIDForUser(ctx context.Context, taskID, userID int64) (*Task, error) {
	return s.loadTaskForUser(ctx, taskID, userID)
}

// DeleteTask mirrors NovelTaskDetailAPIView.delete.
func (s *Service) DeleteTask(ctx context.Context, taskID, userID int64) error {
	if _, err := s.loadTaskForUser(ctx, taskID, userID); err != nil {
		return err
	}
	if err := s.deleteTask(ctx, taskID); err != nil {
		return err
	}
	s.hub.markMissing(taskID)
	return nil
}

// ListTasks mirrors NovelTaskListCreateAPIView.get. The stall rescue that the
// reference performs on every read is handled by the worker loop instead
// (review P1-29).
func (s *Service) ListTasks(ctx context.Context, userID int64) ([]*Task, error) {
	return s.listTasksForUser(ctx, userID, 30)
}

// SceneImageForDownload mirrors NovelSceneImageView.get's lookup.
func (s *Service) SceneImageForDownload(ctx context.Context, taskID, imageID, userID int64) (SceneImage, error) {
	return s.sceneImage(ctx, imageID, taskID, userID)
}

func intPointer(value int) *int { return &value }

func objectsAsAny(objects []*pyObj) []any {
	items := make([]any, 0, len(objects))
	for _, object := range objects {
		items = append(items, object)
	}
	return items
}

func thousands(value int) string {
	digits := strconv.Itoa(value)
	if value < 0 {
		return digits
	}
	parts := []string{}
	for len(digits) > 3 {
		parts = append([]string{digits[len(digits)-3:]}, parts...)
		digits = digits[:len(digits)-3]
	}
	parts = append([]string{digits}, parts...)
	return strings.Join(parts, ",")
}

// ErrNotFound reports a missing task or book row.
var ErrNotFound = errNotFound

// IsValueError reports the reference's ValueError mapping.
func IsValueError(err error) bool {
	var target *ValueError
	return errors.As(err, &target)
}

// IsVisualizationError reports the reference's NovelVisualizationError mapping.
func IsVisualizationError(err error) bool {
	var target *VisualizationError
	return errors.As(err, &target)
}
