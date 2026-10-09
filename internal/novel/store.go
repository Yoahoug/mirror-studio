package novel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mirror-studio/internal/db"
)

var errNotFound = errors.New("novel: row not found")

const (
	taskColumns = `id, created_at, updated_at, book_filename, status, progress_percent, logs, scenes_json,
		error_message, started_at, finished_at, last_event_id, user_id, llm_channel_code, book_source,
		uploaded_book_id, scene_mode, target_plot`

	sceneColumns = `id, created_at, updated_at, scene_number, title, chapter, story_significance, excerpt,
		visual_prompt, status, error_message, image_path, image_url, preview_image_path, attempts, finished_at, task_id`
)

// listBuiltinBooks mirrors services.list_epub_books for the shipped EPUBs.
func (s *Service) listBuiltinBooks() []Book {
	books := []Book{}
	entries, err := os.ReadDir(s.settings.bookDir)
	if err != nil {
		return books
	}
	names := []string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".epub") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.SliceStable(names, func(left, right int) bool {
		return strings.ToLower(names[left]) < strings.ToLower(names[right])
	})
	for _, name := range names {
		fullPath := filepath.Join(s.settings.bookDir, name)
		info, err := os.Stat(fullPath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		books = append(books, Book{
			ID:          "builtin:" + name,
			Source:      SourceBuiltin,
			Filename:    name,
			DisplayName: name,
			Path:        fullPath,
			Size:        info.Size(),
		})
	}
	return books
}

// listUploadedBooks mirrors the user half of services.list_epub_books.
func (s *Service) listUploadedBooks(ctx context.Context, userID int64, limit int) ([]Book, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, created_at, display_name, storage_path, file_size
		FROM novelviz_noveluploadedbook WHERE user_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`,
		userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	books := []Book{}
	for rows.Next() {
		var (
			id          int64
			createdAt   string
			displayName string
			storagePath string
			fileSize    int64
		)
		if err := rows.Scan(&id, &createdAt, &displayName, &storagePath, &fileSize); err != nil {
			return nil, err
		}
		book := Book{
			ID:          fmt.Sprintf("uploaded:%d", id),
			Source:      SourceUploaded,
			Filename:    displayName,
			DisplayName: displayName,
			Path:        storagePath,
			Size:        fileSize,
		}
		bookID := id
		book.UploadedBookID = &bookID
		if parsed, ok := db.ParseTime(createdAt); ok {
			book.CreatedAt = &parsed
		}
		books = append(books, book)
	}
	return books, rows.Err()
}

type uploadedBookRow struct {
	ID          int64
	DisplayName string
	StoragePath string
	FileSize    int64
	CreatedAt   time.Time
}

func (s *Service) uploadedBook(ctx context.Context, id, userID int64) (*uploadedBookRow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, created_at, display_name, storage_path, file_size
		FROM novelviz_noveluploadedbook WHERE id = ? AND user_id = ?`, id, userID)
	result := &uploadedBookRow{}
	var createdAt string
	if err := row.Scan(&result.ID, &createdAt, &result.DisplayName, &result.StoragePath, &result.FileSize); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	parsed, _ := db.ParseTime(createdAt)
	result.CreatedAt = parsed
	return result, nil
}

// insertUploadedBook stores the row after the bytes are on disk.
func (s *Service) insertUploadedBook(ctx context.Context, userID int64, displayName, storagePath string, size int64, sha256 string) (*Book, error) {
	now := s.now()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO novelviz_noveluploadedbook (created_at, updated_at, display_name, storage_path, file_size, sha256, user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		db.FormatTime(now), db.FormatTime(now), displayName, storagePath, size, sha256, userID)
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	bookID := id
	return &Book{
		ID:             fmt.Sprintf("uploaded:%d", id),
		Source:         SourceUploaded,
		Filename:       displayName,
		DisplayName:    displayName,
		Path:           storagePath,
		Size:           size,
		UploadedBookID: &bookID,
		CreatedAt:      &now,
	}, nil
}

// resolveUploadedBookPath mirrors services.resolve_uploaded_book_path.
func (s *Service) resolveUploadedBookPath(ctx context.Context, uploadedBookID, userID int64) (string, error) {
	row, err := s.uploadedBook(ctx, uploadedBookID, userID)
	if err != nil {
		return "", &ValueError{Message: "上传的 EPUB 文件不存在"}
	}
	mediaRoot, err := resolveAbsolute(s.settings.mediaRoot)
	if err != nil {
		return "", &ValueError{Message: "上传的 EPUB 文件不存在"}
	}
	target := posixNormpathAbs(mediaRoot, filepath.FromSlash(row.StoragePath))
	if !pathWithin(mediaRoot, target) || !isRegularFile(target) {
		return "", &ValueError{Message: "上传的 EPUB 文件不存在"}
	}
	return target, nil
}

// resolveTaskBookPath mirrors services.resolve_task_book_path.
func (s *Service) resolveTaskBookPath(ctx context.Context, task *Task) (string, error) {
	if task.BookSource == SourceUploaded {
		if task.UploadedBookID == nil || *task.UploadedBookID == 0 {
			return "", &ValueError{Message: "上传的 EPUB 文件不存在"}
		}
		return s.resolveUploadedBookPath(ctx, *task.UploadedBookID, task.UserID)
	}
	return resolveBookPath(s.settings.bookDir, task.BookFilename)
}

func scanTask(row interface {
	Scan(dest ...any) error
}) (*Task, error) {
	var (
		task         Task
		createdAt    string
		updatedAt    string
		logsRaw      string
		scenesRaw    string
		startedAt    sql.NullString
		finishedAt   sql.NullString
		uploadedBook sql.NullInt64
	)
	if err := row.Scan(
		&task.ID, &createdAt, &updatedAt, &task.BookFilename, &task.Status, &task.Progress,
		&logsRaw, &scenesRaw, &task.ErrorMessage, &startedAt, &finishedAt, &task.LastEventID,
		&task.UserID, &task.LLMChannelCode, &task.BookSource, &uploadedBook, &task.SceneMode, &task.TargetPlot,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errNotFound
		}
		return nil, err
	}
	if parsed, ok := db.ParseTime(createdAt); ok {
		task.CreatedAt = parsed
	}
	if parsed, ok := db.ParseTime(updatedAt); ok {
		task.UpdatedAt = parsed
	}
	events, err := eventsFromJSON(logsRaw)
	if err != nil {
		return nil, err
	}
	task.Logs = events
	scenes, err := scenesFromJSON(scenesRaw)
	if err != nil {
		return nil, err
	}
	task.Scenes = scenes
	task.StartedAt = db.TimePtr(startedAt)
	task.FinishedAt = db.TimePtr(finishedAt)
	if uploadedBook.Valid {
		value := uploadedBook.Int64
		task.UploadedBookID = &value
	}
	return &task, nil
}

func scenesFromJSON(raw string) ([]*pyObj, error) {
	text := strings.TrimSpace(raw)
	if text == "" || text == "null" {
		return nil, nil
	}
	decoded, err := decodePyJSON([]byte(text))
	if err != nil {
		return nil, err
	}
	items, ok := decoded.([]any)
	if !ok {
		return nil, nil
	}
	scenes := make([]*pyObj, 0, len(items))
	for _, item := range items {
		if object, ok := item.(*pyObj); ok {
			scenes = append(scenes, object)
		}
	}
	return scenes, nil
}

func scenesJSON(scenes []*pyObj) (string, error) {
	items := make([]any, 0, len(scenes))
	for _, scene := range scenes {
		items = append(items, scene)
	}
	return marshalPython(items, true)
}

func (s *Service) loadTask(ctx context.Context, taskID int64) (*Task, error) {
	task, err := scanTask(s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM novelviz_novelvisualizationtask WHERE id = ?`, taskID))
	if err != nil {
		return nil, err
	}
	images, err := s.sceneImagesForTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	task.Images = images
	return task, nil
}

func (s *Service) loadTaskForUser(ctx context.Context, taskID, userID int64) (*Task, error) {
	task, err := scanTask(s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM novelviz_novelvisualizationtask WHERE id = ? AND user_id = ?`, taskID, userID))
	if err != nil {
		return nil, err
	}
	images, err := s.sceneImagesForTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	task.Images = images
	return task, nil
}

// listTasksForUser mirrors the list projection: order by created_at descending,
// logs not loaded (serialize_task(include_logs=False) never emits them).
func (s *Service) listTasksForUser(ctx context.Context, userID int64, limit int) ([]*Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, created_at, updated_at, book_filename, status, progress_percent, '', scenes_json,
			error_message, started_at, finished_at, last_event_id, user_id, llm_channel_code, book_source,
			uploaded_book_id, scene_mode, target_plot
		FROM novelviz_novelvisualizationtask WHERE user_id = ? ORDER BY created_at DESC LIMIT ?`,
		userID, limit)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	grouped, err := s.sceneImagesForTasks(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		task.Images = grouped[task.ID]
	}
	return tasks, nil
}

func scanSceneImage(row interface {
	Scan(dest ...any) error
}) (SceneImage, error) {
	var (
		image      SceneImage
		createdAt  string
		updatedAt  string
		finishedAt sql.NullString
	)
	err := row.Scan(
		&image.ID, &createdAt, &updatedAt, &image.SceneNumber, &image.Title, &image.Chapter,
		&image.StorySignificance, &image.Excerpt, &image.VisualPrompt, &image.Status, &image.ErrorMessage,
		&image.ImagePath, &image.ImageURL, &image.PreviewImagePath, &image.Attempts, &finishedAt, &image.TaskID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SceneImage{}, errNotFound
		}
		return SceneImage{}, err
	}
	if parsed, ok := db.ParseTime(createdAt); ok {
		image.CreatedAt = parsed
	}
	if parsed, ok := db.ParseTime(updatedAt); ok {
		image.UpdatedAt = parsed
	}
	image.FinishedAt = db.TimePtr(finishedAt)
	return image, nil
}

func (s *Service) sceneImagesForTask(ctx context.Context, taskID int64) ([]SceneImage, error) {
	grouped, err := s.sceneImagesForTasks(ctx, []int64{taskID})
	if err != nil {
		return nil, err
	}
	images := grouped[taskID]
	if images == nil {
		images = []SceneImage{}
	}
	return images, nil
}

// sceneImagesForTasks loads the images of several tasks in one query, the same
// shape prefetch_related produces for the list endpoint.
func (s *Service) sceneImagesForTasks(ctx context.Context, taskIDs []int64) (map[int64][]SceneImage, error) {
	grouped := map[int64][]SceneImage{}
	if len(taskIDs) == 0 {
		return grouped, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(taskIDs)), ",")
	args := make([]any, 0, len(taskIDs))
	for _, id := range taskIDs {
		args = append(args, id)
		grouped[id] = []SceneImage{}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+sceneColumns+` FROM novelviz_novelsceneimage
		WHERE task_id IN (`+placeholders+`) ORDER BY task_id, scene_number, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		image, err := scanSceneImage(rows)
		if err != nil {
			return nil, err
		}
		grouped[image.TaskID] = append(grouped[image.TaskID], image)
	}
	return grouped, rows.Err()
}

func (s *Service) sceneImage(ctx context.Context, imageID, taskID, userID int64) (SceneImage, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+sceneColumns+` FROM novelviz_novelsceneimage
		WHERE id = ? AND task_id = ? AND status = ? AND task_id IN (
			SELECT id FROM novelviz_novelvisualizationtask WHERE id = ? AND user_id = ?
		)`, imageID, taskID, SceneStatusSucceeded, taskID, userID)
	return scanSceneImage(row)
}

func (s *Service) sceneImageByID(ctx context.Context, imageID int64) (SceneImage, error) {
	return scanSceneImage(s.db.QueryRowContext(ctx, `SELECT `+sceneColumns+` FROM novelviz_novelsceneimage WHERE id = ?`, imageID))
}

// pendingSceneIDs mirrors services._get_scene_ids.
func (s *Service) pendingSceneIDs(ctx context.Context, taskID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM novelviz_novelsceneimage
		WHERE task_id = ? AND status IN (?, ?, ?) ORDER BY scene_number`,
		taskID, SceneStatusPending, SceneStatusRunning, SceneStatusFailed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Service) countScenes(ctx context.Context, taskID int64) (int, error) {
	var total int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM novelviz_novelsceneimage WHERE task_id = ?`, taskID).Scan(&total)
	return total, err
}

func (s *Service) countFinishedScenes(ctx context.Context, taskID int64) (int, error) {
	var finished int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM novelviz_novelsceneimage WHERE task_id = ? AND status IN (?, ?)`,
		taskID, SceneStatusSucceeded, SceneStatusFailed).Scan(&finished)
	return finished, err
}

func (s *Service) countScenesByStatus(ctx context.Context, taskID int64, status string) (int, error) {
	var total int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM novelviz_novelsceneimage WHERE task_id = ? AND status = ?`, taskID, status).Scan(&total)
	return total, err
}

func (s *Service) deleteScenes(ctx context.Context, taskID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM novelviz_novelsceneimage WHERE task_id = ?`, taskID)
	return err
}

// resetUnfinishedScenes mirrors the retry reset of every non-succeeded scene.
func (s *Service) resetUnfinishedScenes(ctx context.Context, taskID int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE novelviz_novelsceneimage
		SET status = ?, error_message = '', image_path = '', image_url = '', attempts = 0,
			finished_at = NULL, updated_at = ?
		WHERE task_id = ? AND status != ?`,
		SceneStatusPending, db.FormatTime(now), taskID, SceneStatusSucceeded)
	return err
}

func (s *Service) deleteTask(ctx context.Context, taskID int64) error {
	if err := s.deleteScenes(ctx, taskID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM novelviz_novelvisualizationtask WHERE id = ?`, taskID)
	return err
}

// updateTaskProgress mirrors the progress-only writes of the reference.
func (s *Service) updateTaskProgress(ctx context.Context, taskID int64, progress int, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE novelviz_novelvisualizationtask SET progress_percent = ?, updated_at = ? WHERE id = ?`,
		progress, db.FormatTime(now), taskID)
	return err
}

// claimQueuedTask claims the oldest queued task atomically (review P1-16: never
// select-then-update).
func (s *Service) claimQueuedTask(ctx context.Context, now time.Time) (int64, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		UPDATE novelviz_novelvisualizationtask
		SET status = ?, started_at = COALESCE(started_at, ?), progress_percent = ?, error_message = '', updated_at = ?
		WHERE id = (
			SELECT id FROM novelviz_novelvisualizationtask WHERE status = ? ORDER BY created_at, id LIMIT 1
		) AND status = ?
		RETURNING id`,
		StatusRunning, db.FormatTime(now), 3, db.FormatTime(now), StatusQueued, StatusQueued)
	var id int64
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return id, true, nil
}

// stalledTaskIDs mirrors services._task_is_stalled.
func (s *Service) stalledTaskIDs(ctx context.Context, now time.Time) ([]int64, error) {
	threshold := now.Add(-s.settings.stallRequeue)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM novelviz_novelvisualizationtask
		WHERE status = ? AND started_at IS NOT NULL AND started_at <= ? AND last_event_id = 0
		ORDER BY id`, StatusRunning, db.FormatTime(threshold))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// requeueStalledTask mirrors services._rescue_stalled_task.
func (s *Service) requeueStalledTask(ctx context.Context, taskID int64, now time.Time) error {
	if err := s.deleteScenes(ctx, taskID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE novelviz_novelvisualizationtask
		SET status = ?, progress_percent = 1, error_message = '', started_at = NULL, finished_at = NULL,
			last_event_id = 0, logs = '[]', scenes_json = '[]', updated_at = ?
		WHERE id = ?`, StatusQueued, db.FormatTime(now), taskID)
	return err
}
