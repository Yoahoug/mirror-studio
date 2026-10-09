package generator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"mirror-studio/internal/db"
	"mirror-studio/internal/media"
)

// taskColumns is the generator_generatetask layout in table order, spelled out
// so a column cannot silently shift.
const taskColumns = `id, created_at, updated_at, task_no, status, prompt, negative_prompt, style_code, size_code,
	quota_cost, provider_request_id, provider_response_json, error_code, error_message,
	submitted_at, started_at, finished_at, client_request_id, client_ip, quota_finalized,
	user_id, topic_id, channel_id, progress_percent, deleted_at, is_deleted, generation_mode,
	reference_image_data_url, reference_image_name, reference_storage_provider, reference_bucket_name,
	reference_object_key, reference_image_mime_type, reference_image_size, reference_image_width,
	reference_image_height, reference_image_sha256, execution_attempts, lease_expires_at, quality_code`

const channelColumns = `id, created_at, updated_at, name, channel_code, base_url, api_path, api_key_encrypted,
	model_name, timeout_seconds, max_retries, is_active, priority, channel_type, provider_name, edit_api_path, provider_vendor`

const topicColumns = `id, created_at, updated_at, title, cover_image_id, last_task_at, is_archived, user_id`

type rowScanner interface {
	Scan(dest ...any) error
}

type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func scanTask(row rowScanner) (*Task, error) {
	task := &Task{}
	var (
		createdAt, updatedAt, submittedAt string
		startedAt, finishedAt, deletedAt  sql.NullString
		leaseExpiresAt                    sql.NullString
		clientIP                          sql.NullString
		topicID                           sql.NullInt64
		quotaFinalized, isDeleted         int
	)
	err := row.Scan(
		&task.ID, &createdAt, &updatedAt, &task.TaskNo, &task.Status, &task.Prompt, &task.NegativePrompt,
		&task.StyleCode, &task.SizeCode, &task.QuotaCost, &task.ProviderRequestID, &task.ProviderResponseJSON,
		&task.ErrorCode, &task.ErrorMessage, &submittedAt, &startedAt, &finishedAt, &task.ClientRequestID,
		&clientIP, &quotaFinalized, &task.UserID, &topicID, &task.ChannelID, &task.ProgressPercent,
		&deletedAt, &isDeleted, &task.GenerationMode, &task.ReferenceImageDataURL, &task.ReferenceImageName,
		&task.ReferenceStorageProvider, &task.ReferenceBucketName, &task.ReferenceObjectKey,
		&task.ReferenceImageMimeType, &task.ReferenceImageSize, &task.ReferenceImageWidth,
		&task.ReferenceImageHeight, &task.ReferenceImageSHA256, &task.ExecutionAttempts, &leaseExpiresAt,
		&task.QualityCode,
	)
	if err != nil {
		return nil, err
	}
	task.CreatedAt = parseStoredTime(createdAt)
	task.SubmittedAt = parseStoredTime(submittedAt)
	task.StartedAt = parseStoredTimePtr(startedAt)
	task.FinishedAt = parseStoredTimePtr(finishedAt)
	task.DeletedAt = parseStoredTimePtr(deletedAt)
	task.LeaseExpiresAt = parseStoredTimePtr(leaseExpiresAt)
	task.ClientIP = clientIP.String
	if topicID.Valid {
		value := topicID.Int64
		task.TopicID = &value
	}
	task.QuotaFinalized = quotaFinalized == 1
	task.IsDeleted = isDeleted == 1
	return task, nil
}

func scanChannel(row rowScanner) (*Channel, error) {
	channel := &Channel{}
	var (
		createdAt, updatedAt   string
		apiKey                 sql.NullString
		timeout, maxRetries    int
		priority               int
		isActive               int
		channelType, provider  sql.NullString
		editPath, providerVend sql.NullString
	)
	err := row.Scan(
		&channel.ID, &createdAt, &updatedAt, &channel.Name, &channel.ChannelCode, &channel.BaseURL,
		&channel.APIPath, &apiKey, &channel.ModelName, &timeout, &maxRetries, &isActive, &priority,
		&channelType, &provider, &editPath, &providerVend,
	)
	if err != nil {
		return nil, err
	}
	channel.CreatedAt = parseStoredTime(createdAt)
	channel.UpdatedAt = parseStoredTime(updatedAt)
	channel.APIKeyEncrypted = apiKey.String
	channel.TimeoutSeconds = timeout
	channel.MaxRetries = maxRetries
	channel.Priority = priority
	channel.IsActive = isActive == 1
	channel.ChannelType = channelType.String
	channel.ProviderName = provider.String
	channel.EditAPIPath = editPath.String
	channel.ProviderVendor = providerVend.String
	return channel, nil
}

func scanTopic(row rowScanner) (*Topic, error) {
	topic := &Topic{}
	var (
		createdAt, updatedAt string
		lastTaskAt           string
		coverImageID         sql.NullInt64
		isArchived           int
	)
	err := row.Scan(&topic.ID, &createdAt, &updatedAt, &topic.Title, &coverImageID, &lastTaskAt, &isArchived, &topic.UserID)
	if err != nil {
		return nil, err
	}
	topic.CreatedAt = parseStoredTime(createdAt)
	topic.UpdatedAt = parseStoredTime(updatedAt)
	topic.LastTaskAt = parseStoredTime(lastTaskAt)
	topic.IsArchived = isArchived == 1
	if coverImageID.Valid {
		value := coverImageID.Int64
		topic.CoverImageID = &value
	}
	return topic, nil
}

func parseStoredTime(value string) time.Time {
	parsed, _ := db.ParseTime(value)
	return parsed
}

func parseStoredTimePtr(value sql.NullString) *time.Time {
	if !value.Valid {
		return nil
	}
	parsed, ok := db.ParseTime(value.String)
	if !ok {
		return nil
	}
	return &parsed
}

// ── reads ──

func (s *Service) taskByNoQuery(q queryer, query string, args ...any) (*Task, error) {
	task, err := scanTask(q.QueryRow(query, args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return task, nil
}

func (s *Service) taskForUser(ctx context.Context, userID int64, taskNo string) (*Task, error) {
	return s.taskByNoQuery(s.db,
		`SELECT `+taskColumns+` FROM generator_generatetask WHERE task_no = ? AND user_id = ? AND is_deleted = ?`,
		taskNo, userID, db.Bool(false))
}

func (s *Service) taskByNoAny(ctx context.Context, taskNo string) (*Task, error) {
	return s.taskByNoQuery(s.db, `SELECT `+taskColumns+` FROM generator_generatetask WHERE task_no = ?`, taskNo)
}

func (s *Service) taskByIDTx(tx *db.Tx, id int64) (*Task, error) {
	return s.taskByNoQuery(tx, `SELECT `+taskColumns+` FROM generator_generatetask WHERE id = ?`, id)
}

func (s *Service) channelByCode(ctx context.Context, code string) (*Channel, error) {
	channel, err := scanChannel(s.db.QueryRow(
		`SELECT `+channelColumns+` FROM generator_imagechannel WHERE channel_code = ? AND is_active = ?`,
		code, db.Bool(true)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return channel, nil
}

func (s *Service) defaultLLMChannel(ctx context.Context) (*Channel, error) {
	channel, err := scanChannel(s.db.QueryRow(
		`SELECT `+channelColumns+` FROM generator_imagechannel
		 WHERE channel_type = ? AND is_active = ? ORDER BY priority, id LIMIT 1`,
		ChannelTypeLLM, db.Bool(true)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return channel, nil
}

func (s *Service) defaultImageChannel(ctx context.Context) (*Channel, error) {
	channel, err := scanChannel(s.db.QueryRow(
		`SELECT `+channelColumns+` FROM generator_imagechannel
		 WHERE channel_type = ? AND is_active = ? ORDER BY priority, id LIMIT 1`,
		ChannelTypeImage, db.Bool(true)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return channel, nil
}

func (s *Service) channelByIDTx(tx *db.Tx, id int64) (*Channel, error) {
	channel, err := scanChannel(tx.QueryRow(`SELECT `+channelColumns+` FROM generator_imagechannel WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return channel, nil
}

func (s *Service) topicsForUser(ctx context.Context, userID int64, limit int) ([]*Topic, error) {
	rows, err := s.db.Query(
		`SELECT `+topicColumns+` FROM generator_generationtopic
		 WHERE user_id = ? AND is_archived = ? ORDER BY last_task_at DESC LIMIT ?`,
		userID, db.Bool(false), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	topics := []*Topic{}
	for rows.Next() {
		topic, err := scanTopic(rows)
		if err != nil {
			return nil, err
		}
		topics = append(topics, topic)
	}
	return topics, rows.Err()
}

func (s *Service) topicByID(ctx context.Context, userID int64, topicID int64) (*Topic, error) {
	topic, err := scanTopic(s.db.QueryRow(
		`SELECT `+topicColumns+` FROM generator_generationtopic WHERE id = ? AND user_id = ?`, topicID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return topic, nil
}

func (s *Service) insertTopic(ctx context.Context, userID int64, title string) (*Topic, error) {
	now := time.Now().UTC()
	result, err := s.db.Exec(
		`INSERT INTO generator_generationtopic (created_at, updated_at, title, cover_image_id, last_task_at, is_archived, user_id)
		 VALUES (?, ?, ?, NULL, ?, ?, ?)`,
		db.FormatTime(now), db.FormatTime(now), title, db.FormatTime(now), db.Bool(false), userID)
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Topic{ID: id, UserID: userID, Title: title, LastTaskAt: now, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Service) touchTopic(ctx context.Context, topicID int64, coverImageID *int64) error {
	now := db.FormatTime(time.Now().UTC())
	if coverImageID != nil {
		_, err := s.db.Exec(
			`UPDATE generator_generationtopic SET last_task_at = ?, cover_image_id = ?, updated_at = ? WHERE id = ?`,
			now, *coverImageID, now, topicID)
		return err
	}
	_, err := s.db.Exec(
		`UPDATE generator_generationtopic SET last_task_at = ?, updated_at = ? WHERE id = ?`, now, now, topicID)
	return err
}

func (s *Service) listTasks(ctx context.Context, userID int64, topicID *int64, keyword string, limit, offset int) ([]*Task, error) {
	query := `SELECT ` + taskColumns + ` FROM generator_generatetask WHERE user_id = ? AND is_deleted = ?`
	args := []any{userID, db.Bool(false)}
	if topicID != nil {
		query += ` AND topic_id = ?`
		args = append(args, *topicID)
	}
	if keyword != "" {
		query += ` AND prompt LIKE ? ESCAPE '\'`
		args = append(args, likePattern(keyword))
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []*Task{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *Service) countTasks(ctx context.Context, userID int64, topicID *int64, keyword string) (int, error) {
	query := `SELECT COUNT(1) FROM generator_generatetask WHERE user_id = ? AND is_deleted = ?`
	args := []any{userID, db.Bool(false)}
	if topicID != nil {
		query += ` AND topic_id = ?`
		args = append(args, *topicID)
	}
	if keyword != "" {
		query += ` AND prompt LIKE ? ESCAPE '\'`
		args = append(args, likePattern(keyword))
	}
	var total int
	if err := s.db.QueryRow(query, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *Service) countUserTasks(ctx context.Context, userID int64) (int, error) {
	var total int
	err := s.db.QueryRow(
		`SELECT COUNT(1) FROM generator_generatetask WHERE user_id = ? AND is_deleted = ?`,
		userID, db.Bool(false)).Scan(&total)
	return total, err
}

func (s *Service) countDailyTasks(ctx context.Context, userID int64, start, end time.Time) (int, error) {
	var total int
	err := s.db.QueryRow(
		`SELECT COUNT(1) FROM generator_generatetask WHERE user_id = ? AND created_at >= ? AND created_at < ?`,
		userID, db.FormatTime(start), db.FormatTime(end)).Scan(&total)
	return total, err
}

func (s *Service) countPendingTasks(ctx context.Context, userID int64) (int, error) {
	var total int
	err := s.db.QueryRow(
		`SELECT COUNT(1) FROM generator_generatetask WHERE user_id = ? AND status IN (?, ?)`,
		userID, StatusQueued, StatusRunning).Scan(&total)
	return total, err
}

func (s *Service) taskByClientRequestID(ctx context.Context, userID int64, clientRequestID string) (*Task, error) {
	if clientRequestID == "" {
		return nil, nil
	}
	return s.taskByNoQuery(s.db,
		`SELECT `+taskColumns+` FROM generator_generatetask WHERE user_id = ? AND client_request_id = ? LIMIT 1`,
		userID, clientRequestID)
}

func (s *Service) topicRef(ctx context.Context, topicID int64) (*Topic, error) {
	return s.topicByID(ctx, 0, topicID)
}

// ── image loading ──

// imagesByTaskID reads mediahub_generatedimage for the given tasks. internal/media
// owns that table; it exposes no read accessor yet (see the package report), so
// the loader is isolated here for a one-line swap once it does.
func (s *Service) imagesByTaskID(ctx context.Context, ids []int64) (map[int64][]media.Image, error) {
	result := map[int64][]media.Image{}
	if len(ids) == 0 {
		return result, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.Query(
		`SELECT id, task_id, user_id, status, width, height, file_size, mime_type, object_key, bucket_name,
		        preview_object_key, created_at, expires_at
		 FROM mediahub_generatedimage WHERE task_id IN (`+placeholders+`) ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		image := media.Image{}
		var createdAt, expiresAt string
		if err := rows.Scan(
			&image.ID, &image.TaskID, &image.UserID, &image.Status, &image.Width, &image.Height,
			&image.FileSize, &image.MimeType, &image.ObjectKey, &image.BucketName, &image.PreviewObjectKey,
			&createdAt, &expiresAt,
		); err != nil {
			return nil, err
		}
		image.CreatedAt = parseStoredTime(createdAt)
		image.ExpiresAt = parseStoredTime(expiresAt)
		result[image.TaskID] = append(result[image.TaskID], image)
	}
	return result, rows.Err()
}

func (s *Service) attachImages(ctx context.Context, tasks []*Task) error {
	if len(tasks) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	grouped, err := s.imagesByTaskID(ctx, ids)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		task.Images = grouped[task.ID]
	}
	return nil
}

// ── writes ──

func (s *Service) insertTask(ctx context.Context, task *Task) (int64, error) {
	now := time.Now().UTC()
	task.CreatedAt = now
	task.SubmittedAt = now
	result, err := s.db.Exec(
		`INSERT INTO generator_generatetask (
			created_at, updated_at, task_no, status, prompt, negative_prompt, style_code, size_code,
			quota_cost, provider_request_id, provider_response_json, error_code, error_message,
			submitted_at, started_at, finished_at, client_request_id, client_ip, quota_finalized,
			user_id, topic_id, channel_id, progress_percent, deleted_at, is_deleted, generation_mode,
			reference_image_data_url, reference_image_name, reference_storage_provider, reference_bucket_name,
			reference_object_key, reference_image_mime_type, reference_image_size, reference_image_width,
			reference_image_height, reference_image_sha256, execution_attempts, lease_expires_at, quality_code
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '{}', '', '', ?, NULL, NULL, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, '', ?, '', '', '', '', 0, 0, 0, '', 0, NULL, ?)`,
		db.FormatTime(now), db.FormatTime(now), task.TaskNo, StatusQueued, task.Prompt, task.NegativePrompt,
		task.StyleCode, task.SizeCode, task.QuotaCost, db.FormatTime(now), task.ClientRequestID,
		db.NullableString(task.ClientIP), db.Bool(false), task.UserID, task.TopicID, task.ChannelID,
		task.ProgressPercent, db.Bool(false), task.GenerationMode, task.ReferenceImageName, task.QualityCode,
	)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	task.ID = id
	task.Status = StatusQueued
	task.ProviderResponseJSON = "{}"
	return id, nil
}

func (s *Service) updateTaskReference(ctx context.Context, task *Task, meta map[string]string) error {
	sets := []string{}
	args := []any{}
	for _, key := range []string{
		"reference_storage_provider", "reference_bucket_name", "reference_object_key",
		"reference_image_mime_type", "reference_image_size", "reference_image_width",
		"reference_image_height", "reference_image_sha256",
	} {
		if value, ok := meta[key]; ok {
			sets = append(sets, key+" = ?")
			args = append(args, value)
		}
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, db.FormatTime(time.Now().UTC()), task.ID)
	if _, err := s.db.Exec(`UPDATE generator_generatetask SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		return err
	}
	applyReferenceMeta(task, meta)
	return nil
}

func applyReferenceMeta(task *Task, meta map[string]string) {
	if value, ok := meta["reference_storage_provider"]; ok {
		task.ReferenceStorageProvider = value
	}
	if value, ok := meta["reference_bucket_name"]; ok {
		task.ReferenceBucketName = value
	}
	if value, ok := meta["reference_object_key"]; ok {
		task.ReferenceObjectKey = value
	}
	if value, ok := meta["reference_image_mime_type"]; ok {
		task.ReferenceImageMimeType = value
	}
	if value, ok := meta["reference_image_size"]; ok {
		task.ReferenceImageSize = atoi(value)
	}
	if value, ok := meta["reference_image_width"]; ok {
		task.ReferenceImageWidth = atoi(value)
	}
	if value, ok := meta["reference_image_height"]; ok {
		task.ReferenceImageHeight = atoi(value)
	}
	if value, ok := meta["reference_image_sha256"]; ok {
		task.ReferenceImageSHA256 = value
	}
}

func (s *Service) claimTask(ctx context.Context, taskNo string) (*Task, error) {
	var claimed *Task
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		now := time.Now().UTC()
		eligibility := `is_deleted = 0 AND (status = ? OR (status = ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)))`
		query := `SELECT ` + taskColumns + ` FROM generator_generatetask WHERE ` + eligibility
		args := []any{StatusQueued, StatusRunning, db.FormatTime(now)}
		if taskNo != "" {
			query += ` AND task_no = ?`
			args = append(args, taskNo)
		} else {
			query += ` ORDER BY submitted_at, id LIMIT 1`
		}
		task, err := scanTask(tx.QueryRow(query, args...))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		channel, err := s.channelByIDTx(tx, task.ChannelID)
		if err != nil {
			return err
		}
		task.Channel = channel
		lease := now.Add(time.Duration(s.taskLeaseTimeoutSeconds(task)) * time.Second)
		if _, err := tx.Exec(
			`UPDATE generator_generatetask
			 SET status = ?, lease_expires_at = ?, execution_attempts = execution_attempts + 1,
			     progress_percent = MAX(progress_percent, 25), started_at = COALESCE(started_at, ?),
			     finished_at = NULL, updated_at = ?
			 WHERE id = ?`,
			StatusRunning, db.FormatTime(lease), db.FormatTime(now), db.FormatTime(now), task.ID,
		); err != nil {
			return err
		}
		task.Status = StatusRunning
		task.LeaseExpiresAt = &lease
		task.ExecutionAttempts++
		if task.ProgressPercent < 25 {
			task.ProgressPercent = 25
		}
		if task.StartedAt == nil {
			started := now
			task.StartedAt = &started
		}
		task.FinishedAt = nil
		claimed = task
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *Service) extendTaskLease(ctx context.Context, taskNo string) error {
	return s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		task, err := s.taskByNoQuery(tx,
			`SELECT `+taskColumns+` FROM generator_generatetask WHERE task_no = ? AND status = ? AND is_deleted = 0`,
			taskNo, StatusRunning)
		if err != nil || task == nil {
			return err
		}
		channel, err := s.channelByIDTx(tx, task.ChannelID)
		if err != nil {
			return err
		}
		task.Channel = channel
		lease := time.Now().UTC().Add(time.Duration(s.taskLeaseTimeoutSeconds(task)) * time.Second)
		_, err = tx.Exec(`UPDATE generator_generatetask SET lease_expires_at = ?, updated_at = ? WHERE id = ?`,
			db.FormatTime(lease), db.FormatTime(time.Now().UTC()), task.ID)
		return err
	})
}

func (s *Service) bumpTaskProgress(ctx context.Context, taskID int64, progress int) error {
	_, err := s.db.Exec(
		`UPDATE generator_generatetask SET progress_percent = ?, updated_at = ?
		 WHERE id = ? AND progress_percent < ?`,
		clampProgress(progress), db.FormatTime(time.Now().UTC()), taskID, clampProgress(progress))
	return err
}

func (s *Service) taskAbortRequested(ctx context.Context, taskNo string) bool {
	var status string
	var isDeleted int
	err := s.db.QueryRow(
		`SELECT status, is_deleted FROM generator_generatetask WHERE task_no = ?`, taskNo).Scan(&status, &isDeleted)
	if err != nil {
		return true
	}
	return isDeleted == 1 || status == StatusCanceled
}

func (s *Service) requeueAfterWorkerLoss(ctx context.Context, taskNo string) (*Task, error) {
	var result *Task
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		task, err := s.taskByNoQuery(tx, `SELECT `+taskColumns+` FROM generator_generatetask WHERE task_no = ?`, taskNo)
		if err != nil || task == nil {
			result = task
			return err
		}
		if task.Status != StatusRunning {
			result = task
			return nil
		}
		progress := task.ProgressPercent
		if progress > 20 {
			progress = 20
		}
		if progress < 8 {
			progress = 8
		}
		now := db.FormatTime(time.Now().UTC())
		if _, err := tx.Exec(
			`UPDATE generator_generatetask SET status = ?, progress_percent = ?, lease_expires_at = NULL, updated_at = ? WHERE id = ?`,
			StatusQueued, progress, now, task.ID); err != nil {
			return err
		}
		task.Status = StatusQueued
		task.ProgressPercent = progress
		task.LeaseExpiresAt = nil
		result = task
		return nil
	})
	return result, err
}

func (s *Service) setTaskFailure(ctx context.Context, taskNo, errorCode, errorMessage, providerRequestID string, rawResponse any) (*Task, error) {
	var result *Task
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		task, err := s.taskByNoQuery(tx, `SELECT `+taskColumns+` FROM generator_generatetask WHERE task_no = ?`, taskNo)
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("generator task %q disappeared", taskNo)
		}
		result = task
		if task.Status == StatusSucceeded {
			return nil
		}
		if task.Status == StatusCanceled && task.QuotaFinalized {
			return nil
		}
		// The reference releases the frozen quota inside the same atomic block
		// that writes the terminal state; a crash can then never strand the
		// freeze behind an already-final task.
		if !task.QuotaFinalized {
			if err := s.quota.InTx(tx).ReleaseInTx(task.UserID, task.QuotaCost, task.TaskNo, "任务失败退回"); err != nil {
				return err
			}
			task.QuotaFinalized = true
		}
		payload := newOrderedObject()
		if rawResponse == nil {
			payload.set("failure", newOrderedObject())
		} else {
			payload.set("failure", rawResponse)
		}
		payload.set("error_code", errorCode)
		payload.set("error_message", errorMessage)
		if providerRequestID != "" {
			task.ProviderRequestID = trimRunes(providerRequestID, 128)
		}
		now := db.FormatTime(time.Now().UTC())
		if _, err := tx.Exec(
			`UPDATE generator_generatetask
			 SET status = ?, progress_percent = 100, error_code = ?, error_message = ?,
			     provider_request_id = ?, provider_response_json = ?, finished_at = ?,
			     lease_expires_at = NULL, quota_finalized = ?, updated_at = ?
			 WHERE id = ?`,
			StatusFailed, errorCode, trimRunes(errorMessage, 255), task.ProviderRequestID,
			encodeDjangoJSON(payload), now, db.Bool(true), now, task.ID,
		); err != nil {
			return err
		}
		task.Status = StatusFailed
		task.ProgressPercent = 100
		task.ErrorCode = errorCode
		task.ErrorMessage = trimRunes(errorMessage, 255)
		task.ProviderResponseJSON = encodeDjangoJSON(payload)
		finished := parseStoredTime(now)
		task.FinishedAt = &finished
		task.LeaseExpiresAt = nil
		task.QuotaFinalized = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) setTaskSucceeded(ctx context.Context, taskNo, providerRequestID, providerResponseJSON string) (*Task, error) {
	result, err := s.finalizeTask(ctx, taskNo, func(tx *db.Tx, task *Task, now string) error {
		// The reference consumes the frozen quota inside mark_task_succeeded's
		// atomic block, before the terminal row is written.
		if !task.QuotaFinalized {
			if err := s.quota.InTx(tx).ConsumeInTx(task.UserID, task.QuotaCost, task.TaskNo); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(
			`UPDATE generator_generatetask
			 SET status = ?, progress_percent = 100, provider_request_id = ?, provider_response_json = ?,
			     finished_at = ?, lease_expires_at = NULL, quota_finalized = ?, updated_at = ?
			 WHERE id = ?`,
			StatusSucceeded, providerRequestID, providerResponseJSON, now, db.Bool(true), now, task.ID,
		); err != nil {
			return err
		}
		task.Status = StatusSucceeded
		task.ProgressPercent = 100
		task.ProviderRequestID = providerRequestID
		task.ProviderResponseJSON = providerResponseJSON
		finished := parseStoredTime(now)
		task.FinishedAt = &finished
		task.LeaseExpiresAt = nil
		task.QuotaFinalized = true
		return nil
	})
	return result, err
}

// finalizeTask applies a terminal-state update exactly once. Quota settlement
// happens inside the same transaction through the quota.TxHandle the callback
// receives, mirroring the reference's single atomic block per terminal state.
func (s *Service) finalizeTask(ctx context.Context, taskNo string, apply func(tx *db.Tx, task *Task, now string) error) (*Task, error) {
	var result *Task
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		task, err := s.taskByNoQuery(tx, `SELECT `+taskColumns+` FROM generator_generatetask WHERE task_no = ?`, taskNo)
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("generator task %q disappeared", taskNo)
		}
		result = task
		if task.Status == StatusSucceeded {
			return nil
		}
		if task.Status == StatusCanceled && task.QuotaFinalized {
			return nil
		}
		return apply(tx, task, db.FormatTime(time.Now().UTC()))
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) setTaskCanceled(ctx context.Context, taskNo string) (*Task, error) {
	return s.finalizeTask(ctx, taskNo, func(tx *db.Tx, task *Task, now string) error {
		if task.Status == StatusCanceled {
			return nil
		}
		if _, err := tx.Exec(
			`UPDATE generator_generatetask
			 SET status = ?, progress_percent = 100, finished_at = ?, lease_expires_at = NULL,
			     quota_finalized = ?, updated_at = ?
			 WHERE id = ?`,
			StatusCanceled, now, db.Bool(true), now, task.ID,
		); err != nil {
			return err
		}
		task.Status = StatusCanceled
		task.ProgressPercent = 100
		finished := parseStoredTime(now)
		task.FinishedAt = &finished
		task.LeaseExpiresAt = nil
		task.QuotaFinalized = true
		return nil
	})
}

type cancelResult struct {
	task   *Task
	settle bool
}

func (s *Service) cancelTask(ctx context.Context, userID int64, taskNo string) (*Task, error) {
	var result *Task
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		task, err := s.taskByNoQuery(tx,
			`SELECT `+taskColumns+` FROM generator_generatetask WHERE task_no = ? AND user_id = ? AND is_deleted = 0`,
			taskNo, userID)
		if err != nil {
			return err
		}
		if task == nil {
			return missingTask()
		}
		result = task
		switch task.Status {
		case StatusSucceeded, StatusFailed, StatusCanceled:
			return nil
		}
		// The reference writes 用户取消任务 on the same transaction that flips the
		// row to canceled.
		if !task.QuotaFinalized {
			if err := s.quota.InTx(tx).ReleaseInTx(task.UserID, task.QuotaCost, task.TaskNo, "用户取消任务"); err != nil {
				return err
			}
		}
		now := db.FormatTime(time.Now().UTC())
		if _, err := tx.Exec(
			`UPDATE generator_generatetask
			 SET status = ?, progress_percent = 100, finished_at = ?, lease_expires_at = NULL,
			     quota_finalized = ?, updated_at = ?
			 WHERE id = ?`,
			StatusCanceled, now, db.Bool(true), now, task.ID); err != nil {
			return err
		}
		task.Status = StatusCanceled
		task.ProgressPercent = 100
		finished := parseStoredTime(now)
		task.FinishedAt = &finished
		task.LeaseExpiresAt = nil
		task.QuotaFinalized = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) deleteTaskHistory(ctx context.Context, userID int64, taskNo string) (*Task, error) {
	var result *Task
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		task, err := s.taskByNoQuery(tx,
			`SELECT `+taskColumns+` FROM generator_generatetask WHERE task_no = ? AND user_id = ? AND is_deleted = 0`,
			taskNo, userID)
		if err != nil {
			return err
		}
		if task == nil {
			return missingTask()
		}
		result = task
		now := db.FormatTime(time.Now().UTC())
		if task.Status == StatusQueued || task.Status == StatusRunning {
			// The reference writes 用户删除运行中任务 inside delete_task_history's
			// transaction, together with the canceled row.
			if !task.QuotaFinalized {
				if err := s.quota.InTx(tx).ReleaseInTx(task.UserID, task.QuotaCost, task.TaskNo, "用户删除运行中任务"); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(
				`UPDATE generator_generatetask
				 SET status = ?, progress_percent = 100, finished_at = ?, lease_expires_at = NULL,
				     quota_finalized = ?, updated_at = ?
				 WHERE id = ?`,
				StatusCanceled, now, db.Bool(true), now, task.ID); err != nil {
				return err
			}
			task.Status = StatusCanceled
			task.ProgressPercent = 100
			finished := parseStoredTime(now)
			task.FinishedAt = &finished
			task.LeaseExpiresAt = nil
			task.QuotaFinalized = true
		}
		if _, err := tx.Exec(
			`UPDATE generator_generatetask SET is_deleted = ?, deleted_at = ?, updated_at = ? WHERE id = ?`,
			db.Bool(true), now, now, task.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`UPDATE mediahub_generatedimage SET status = ?, deleted_at = ?, updated_at = ? WHERE task_id = ? AND status = ?`,
			imageStatusDeleted, now, now, task.ID, imageStatusAvailable); err != nil {
			return err
		}
		task.IsDeleted = true
		deleted := parseStoredTime(now)
		task.DeletedAt = &deleted
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func likePattern(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(value) + "%"
}
