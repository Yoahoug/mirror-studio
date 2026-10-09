package generator

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"mirror-studio/internal/media"
)

// StartWorker starts cfg.WorkerConcurrency goroutines that poll the queue. There
// is exactly one execution mode: the request path only persists and enqueues,
// the worker always claims (review P2-9).
func (s *Service) StartWorker(ctx context.Context) {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if s.workerCancel != nil {
		return
	}
	workerCtx, cancel := context.WithCancel(ctx)
	s.workerCancel = cancel
	concurrency := s.cfg.WorkerConcurrency
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 32 {
		concurrency = 32
	}
	for index := 0; index < concurrency; index++ {
		s.workerWG.Add(1)
		go func(workerIndex int) {
			defer s.workerWG.Done()
			s.workerLoop(workerCtx, workerIndex)
		}(index)
	}
	s.log.Info("generation worker started",
		"concurrency", concurrency, "poll_seconds", s.pollInterval().Seconds())
}

// StopWorker cancels the loops and waits for the tasks already claimed.
func (s *Service) StopWorker() {
	s.workerMu.Lock()
	cancel := s.workerCancel
	s.workerCancel = nil
	s.workerMu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.workerWG.Wait()
}

func (s *Service) pollInterval() time.Duration {
	interval := s.cfg.WorkerPollInterval
	if interval < 200*time.Millisecond {
		return 200 * time.Millisecond
	}
	return interval
}

// workerLoop is the top-level loop: every iteration body recovers from a panic,
// logs and backs off before polling again.
func (s *Service) workerLoop(ctx context.Context, index int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.log.Error("生图 worker 循环崩溃", "worker", index, "panic", recovered,
				"stack", string(debug.Stack()))
		}
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		processed, err := s.workerPass(ctx, index)
		if err != nil {
			s.log.Error("后台生图 worker 执行失败", "worker", index, "error", err)
			if !s.sleep(ctx, s.pollInterval()) {
				return
			}
			continue
		}
		if processed == "" {
			if !s.sleep(ctx, s.pollInterval()) {
				return
			}
		}
	}
}

func (s *Service) sleep(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// workerPass claims one task and runs it. It returns the task number it handled,
// or an empty string when the queue was empty.
func (s *Service) workerPass(ctx context.Context, index int) (taskNo string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("worker pass panicked: %v", recovered)
			s.log.Error("后台生图 worker 执行失败", "worker", index, "panic", recovered,
				"stack", string(debug.Stack()))
		}
	}()
	task, err := s.claimTask(ctx, "")
	if err != nil {
		return "", err
	}
	if task == nil {
		return "", nil
	}
	return task.TaskNo, s.executeClaimedTask(ctx, task)
}

// errTaskAborted stops the attempt loop when the task was cancelled or deleted
// while the worker held it.
var errTaskAborted = fmt.Errorf("task aborted")

// executeClaimedTask ports tasks.py `_generate_claimed_task`: one task record,
// up to channel.max_retries upstream attempts, backoff min(1.5*attempt, 3)
// seconds, and a single terminal transition.
func (s *Service) executeClaimedTask(ctx context.Context, task *Task) error {
	adapter := s.adapterFor(task)
	channelCode := ""
	if task.Channel != nil {
		channelCode = task.Channel.ChannelCode
	}
	s.log.Info("开始执行生图任务", "task_no", task.TaskNo, "mode", task.GenerationMode,
		"adapter", adapter.name(), "channel", channelCode)

	maxAttempts := 1
	if task.Channel != nil && task.Channel.MaxRetries > 1 {
		maxAttempts = task.Channel.MaxRetries
	}
	apiKey := ""
	if task.Channel != nil {
		key, err := task.Channel.APIKey(s.cipher)
		if err != nil {
			// A key that cannot be decrypted is unusable; the reference's
			// plaintext fallback is exactly the defect 0.0.1 removes.
			s.log.Error("渠道密钥解密失败，按缺少密钥处理", "channel", channelCode, "error", err)
		}
		apiKey = key
	}

	attemptsUsed := 0
	var (
		lastErr         error
		lastRawResponse any
		lastRequestID   string
	)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptsUsed = attempt
		err := s.runAttempt(ctx, task, adapter, apiKey, attempt, maxAttempts)
		if err == nil {
			return nil
		}
		if err == errTaskAborted {
			s.log.Info("生图任务在执行过程中被终止", "task_no", task.TaskNo)
			return nil
		}
		lastErr = err
		if requestError, ok := err.(*providerRequestError); ok {
			lastRequestID = requestError.requestID
			raw := newOrderedObject()
			raw.set("status_code", requestError.statusCode)
			if requestError.raw == nil {
				raw.set("response", newOrderedObject())
			} else {
				raw.set("response", requestError.raw)
			}
			lastRawResponse = raw
		} else {
			raw := newOrderedObject()
			raw.set("exception_type", exceptionTypeName(err))
			lastRawResponse = raw
		}
		if attempt >= maxAttempts || !isRetryableError(err) {
			break
		}
		backoff := 1.5 * float64(attempt)
		if backoff > 3 {
			backoff = 3
		}
		if !s.sleep(ctx, time.Duration(backoff*float64(time.Second))) {
			return nil
		}
	}

	if s.taskAbortRequested(ctx, task.TaskNo) {
		s.log.Info("生图任务在执行过程中被终止", "task_no", task.TaskNo)
		return nil
	}
	raw := lastRawResponse
	if raw == nil {
		raw = newOrderedObject()
	}
	if object, ok := raw.(*orderedObject); ok {
		object.set("adapter", adapter.name())
		object.set("attempts_used", attemptsUsed)
		object.set("max_attempts", maxAttempts)
	}
	if task.ReferenceObjectKey != "" {
		s.deleteStoredObject(ctx, task.ReferenceBucketName, task.ReferenceObjectKey)
	}
	message := "任务执行失败"
	if lastErr != nil {
		message = lastErr.Error()
	}
	if _, err := s.failTask(ctx, task, "generate_failed", message, lastRequestID, raw); err != nil {
		return err
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("生图任务执行失败")
}

// runAttempt performs one upstream round trip and settles the task on success.
func (s *Service) runAttempt(ctx context.Context, task *Task, adapter providerAdapter, apiKey string, attempt, maxAttempts int) error {
	if err := s.extendTaskLease(ctx, task.TaskNo); err != nil {
		return err
	}
	if s.taskAbortRequested(ctx, task.TaskNo) {
		return errTaskAborted
	}
	result, err := adapter.generate(ctx, task, task.Channel, apiKey)
	if err != nil {
		return err
	}
	if err := s.extendTaskLease(ctx, task.TaskNo); err != nil {
		return err
	}
	if s.taskAbortRequested(ctx, task.TaskNo) {
		return errTaskAborted
	}

	storedCount := 0
	firstImageID := int64(0)
	for index, image := range result.images {
		content := image.content
		mimeType := image.mimeType
		if len(content) == 0 && image.remoteURL != "" {
			fetched, fetchedMime, err := s.media.FetchRemoteImage(ctx, image.remoteURL)
			if err != nil {
				return err
			}
			content, mimeType = fetched, fetchedMime
		}
		if len(content) == 0 {
			return fmt.Errorf("上游未返回可存储图片内容")
		}
		stored, err := s.media.StoreGeneratedImage(ctx, media.StoreImageInput{
			UserID:   task.UserID,
			TaskID:   task.ID,
			TaskNo:   task.TaskNo,
			Index:    index + 1,
			Content:  content,
			MimeType: mimeType,
			Prompt:   task.Prompt,
		})
		if err != nil {
			return err
		}
		if firstImageID == 0 {
			firstImageID = stored.ID
		}
		storedCount++
	}
	if storedCount == 0 {
		return fmt.Errorf("上游未返回可存储图片内容")
	}

	raw := copyOrdered(result.rawResponse)
	meta := newOrderedObject()
	meta.set("attempts_used", attempt)
	meta.set("max_attempts", maxAttempts)
	raw.set("_meta", meta)
	providerResponse := encodeDjangoJSON(raw)

	updated, settle, err := s.setTaskSucceeded(ctx, task.TaskNo, result.requestID, providerResponse)
	if err != nil {
		return err
	}
	if settle {
		if err := s.quota.Consume(ctx, task.UserID, task.QuotaCost, task.TaskNo); err != nil {
			s.log.Error("任务成功扣减次数失败", "task_no", task.TaskNo, "error", err)
		}
	}
	if updated != nil && updated.TopicID != nil && firstImageID != 0 {
		if err := s.touchTopic(ctx, *updated.TopicID, &firstImageID); err != nil {
			s.log.Warn("主题封面更新失败", "topic_id", *updated.TopicID, "error", err)
		}
	}
	s.log.Info("生图任务执行成功", "task_no", task.TaskNo, "images", storedCount, "request_id", result.requestID)
	return nil
}

func copyOrdered(value any) *orderedObject {
	copied := newOrderedObject()
	object, ok := value.(*orderedObject)
	if !ok {
		return copied
	}
	for _, key := range object.keys {
		copied.set(key, object.values[key])
	}
	return copied
}

// exceptionTypeName reproduces the adapter/exception labels the reference stores
// in provider_response_json.
func exceptionTypeName(err error) string {
	switch err.(type) {
	case *providerRequestError:
		return "ProviderRequestError"
	case *timeoutError:
		return "TimeoutError"
	case *transportFailure:
		return "ConnectionError"
	}
	return "ValueError"
}
