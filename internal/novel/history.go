package novel

import (
	"context"
	"strconv"
	"strings"
)

// HistoryTotal and HistoryCandidates expose the novel side of the merged
// workspace history. internal/generator declares the matching
// NovelHistoryProvider interface and consumes these two methods through
// SetNovelHistory; they are the Go equivalent of the two queries
// apps/generator/views.py runs against NovelVisualizationTask before merging the
// two ordered sources.
//
// The payload each candidate carries under "__serialized" is
// _serialize_novel_history_task, which lives in the generator app in the
// reference. It is produced here because the generator package cannot import
// this one (internal/novel already imports it for the LLM channel resolver).

// HistoryTotal counts the user's novel tasks, applying the same
// book_filename__icontains filter the reference uses for the `q` parameter.
func (s *Service) HistoryTotal(ctx context.Context, userID int64, keyword string) (int, error) {
	var total int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM novelviz_novelvisualizationtask WHERE user_id = ?`+historyKeywordClause(keyword),
		historyArgs(userID, keyword)...,
	).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total, nil
}

// HistoryCandidates returns the newest `limit` novel tasks for the user, each as
// a map carrying "created_at", "id" and "__serialized". The caller merges these
// with the generation candidates, sorts by (created_at, id) descending and
// paginates; `limit` is page*page_size in the reference.
func (s *Service) HistoryCandidates(ctx context.Context, userID int64, keyword string, limit int) ([]map[string]any, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, created_at, updated_at, book_filename, status, progress_percent, '', scenes_json,
			error_message, started_at, finished_at, last_event_id, user_id, llm_channel_code, book_source,
			uploaded_book_id, scene_mode, target_plot
		FROM novelviz_novelvisualizationtask WHERE user_id = ?`+historyKeywordClause(keyword)+`
		ORDER BY created_at DESC LIMIT ?`,
		append(historyArgs(userID, keyword), limit)...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := []*Task{}
	ids := []int64{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
		ids = append(ids, task.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	images, err := s.sceneImagesForTasks(ctx, ids)
	if err != nil {
		return nil, err
	}

	candidates := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		task.Images = images[task.ID]
		candidates = append(candidates, map[string]any{
			"created_at":   task.CreatedAt,
			"id":           task.ID,
			"__serialized": serializeNovelHistoryTask(task),
		})
	}
	return candidates, nil
}

func historyKeywordClause(keyword string) string {
	if strings.TrimSpace(keyword) == "" {
		return ""
	}
	return ` AND book_filename LIKE ? ESCAPE '\'`
}

func historyArgs(userID int64, keyword string) []any {
	if strings.TrimSpace(keyword) == "" {
		return []any{userID}
	}
	return []any{userID, "%" + escapeLike(strings.TrimSpace(keyword)) + "%"}
}

// escapeLike escapes the LIKE wildcards so a keyword containing % or _ is
// matched literally, matching Django's icontains escaping.
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

// serializeNovelHistoryTask mirrors apps/generator/views.py
// _serialize_novel_history_task. The reference hard-codes 1024x1024 dimensions
// and a nil expiry for every scene image, so those values are literals here too.
func serializeNovelHistoryTask(task *Task) map[string]any {
	images := make([]any, 0, len(task.Images))
	for _, image := range task.Images {
		available := image.Status == StatusSucceeded && image.ImageURL != ""
		status := image.Status
		previewURL := ""
		downloadURL := ""
		if available {
			status = "available"
			previewURL = image.ImageURL + "?preview=1"
			downloadURL = image.ImageURL + "?download=1"
		}
		images = append(images, map[string]any{
			"id":                 image.ID,
			"status":             status,
			"width":              1024,
			"height":             1024,
			"expires_at":         nil,
			"preview_url":        previewURL,
			"download_url":       downloadURL,
			"image_url":          image.ImageURL,
			"scene_number":       image.SceneNumber,
			"title":              image.Title,
			"chapter":            image.Chapter,
			"story_significance": image.StorySignificance,
			"excerpt":            image.Excerpt,
		})
	}

	prompt := "小说可视化：" + task.BookFilename
	if task.SceneMode == ModeTargeted {
		prompt = "小说指定剧情：" + task.BookFilename
	}

	return map[string]any{
		"task_no":                  "novelviz:" + strconv.FormatInt(task.ID, 10),
		"task_kind":                "novelviz",
		"novel_task_id":            task.ID,
		"status":                   task.Status,
		"progress_percent":         task.Progress,
		"generation_mode":          "novel_visualization",
		"prompt":                   prompt,
		"negative_prompt":          "",
		"style_code":               "",
		"size_code":                "1024x1024",
		"quality_code":             "auto",
		"reference_image_name":     task.BookFilename,
		"reference_image_attached": false,
		"reference_image_data_url": "",
		"quota_cost":               0,
		"error_code":               "",
		"error_message":            task.ErrorMessage,
		"topic_id":                 nil,
		"book_filename":            task.BookFilename,
		"scene_mode":               task.SceneMode,
		"target_plot":              task.TargetPlot,
		"submitted_at":             isoformat(task.CreatedAt),
		"started_at":               isoformatPtr(task.StartedAt),
		"finished_at":              isoformatPtr(task.FinishedAt),
		"images":                   images,
	}
}
