package novel

import (
	"fmt"
	"strings"
	"time"
)

// Task statuses, scene statuses, book sources and scene modes are the fixed
// vocabulary the reference implementation persists in these columns.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"

	SceneStatusPending   = "pending"
	SceneStatusRunning   = "running"
	SceneStatusSucceeded = "succeeded"
	SceneStatusFailed    = "failed"

	SourceBuiltin  = "builtin"
	SourceUploaded = "uploaded"

	ModeAuto     = "auto"
	ModeTargeted = "targeted"
)

// Book is a selectable EPUB: either a file shipped in the novel directory or a
// row uploaded by the user.
type Book struct {
	ID             string
	Source         string
	Filename       string
	DisplayName    string
	Path           string
	Size           int64
	UploadedBookID *int64
	CreatedAt      *time.Time
}

// Task is one novel visualisation job.
type Task struct {
	ID             int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	BookFilename   string
	Status         string
	Progress       int
	Logs           []Event
	Scenes         []*pyObj
	ErrorMessage   string
	StartedAt      *time.Time
	FinishedAt     *time.Time
	LastEventID    int64
	UserID         int64
	LLMChannelCode string
	BookSource     string
	UploadedBookID *int64
	SceneMode      string
	TargetPlot     string
	Images         []SceneImage
}

// SceneImage is one generated scene picture of a task.
type SceneImage struct {
	ID                int64
	TaskID            int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
	SceneNumber       int
	Title             string
	Chapter           string
	StorySignificance string
	Excerpt           string
	VisualPrompt      string
	Status            string
	ErrorMessage      string
	ImagePath         string
	ImageURL          string
	PreviewImagePath  string
	Attempts          int
	FinishedAt        *time.Time
}

func (t *Task) isTargeted() bool {
	return t.SceneMode == ModeTargeted && strings.TrimSpace(t.TargetPlot) != ""
}

func (t *Task) isTerminal() bool {
	switch t.Status {
	case StatusSucceeded, StatusFailed, StatusCanceled:
		return true
	}
	return false
}

// isoformat mirrors Python's datetime.isoformat() for the naive-UTC aware
// datetimes the reference stores: six fractional digits when they are non-zero,
// and an explicit +00:00 offset.
func isoformat(value time.Time) string {
	utc := value.UTC()
	if micro := utc.Nanosecond() / 1000; micro != 0 {
		return fmt.Sprintf("%s.%06d+00:00", utc.Format("2006-01-02T15:04:05"), micro)
	}
	return utc.Format("2006-01-02T15:04:05") + "+00:00"
}

func isoformatPtr(value *time.Time) any {
	if value == nil {
		return nil
	}
	return isoformat(*value)
}

// serializeSceneImage mirrors services.serialize_scene_image.
func serializeSceneImage(image SceneImage) *pyObj {
	previewURL := ""
	downloadURL := ""
	if image.ImageURL != "" {
		previewURL = image.ImageURL + "?preview=1"
		downloadURL = image.ImageURL + "?download=1"
	}
	return newPyObj().
		Set("id", image.ID).
		Set("scene_number", image.SceneNumber).
		Set("title", image.Title).
		Set("chapter", image.Chapter).
		Set("story_significance", image.StorySignificance).
		Set("excerpt", image.Excerpt).
		Set("visual_prompt", image.VisualPrompt).
		Set("status", image.Status).
		Set("error_message", image.ErrorMessage).
		Set("image_url", image.ImageURL).
		Set("preview_url", previewURL).
		Set("download_url", downloadURL).
		Set("attempts", image.Attempts).
		Set("created_at", isoformat(image.CreatedAt)).
		Set("finished_at", isoformatPtr(image.FinishedAt))
}

// serializeTask mirrors services.serialize_task.
func serializeTask(task *Task, includeLogs bool) *pyObj {
	images := make([]any, 0, len(task.Images))
	for _, image := range task.Images {
		images = append(images, serializeSceneImage(image))
	}
	scenes := make([]any, 0, len(task.Scenes))
	for _, scene := range task.Scenes {
		scenes = append(scenes, scene)
	}
	logs := []any{}
	if includeLogs {
		for _, event := range task.Logs {
			logs = append(logs, event.pyValue())
		}
	}
	var uploaded any
	if task.UploadedBookID != nil {
		uploaded = *task.UploadedBookID
	}
	return newPyObj().
		Set("id", task.ID).
		Set("book_filename", task.BookFilename).
		Set("book_source", task.BookSource).
		Set("uploaded_book_id", uploaded).
		Set("scene_mode", task.SceneMode).
		Set("target_plot", task.TargetPlot).
		Set("status", task.Status).
		Set("progress_percent", task.Progress).
		Set("error_message", task.ErrorMessage).
		Set("scenes", scenes).
		Set("images", images).
		Set("logs", logs).
		Set("created_at", isoformat(task.CreatedAt)).
		Set("started_at", isoformatPtr(task.StartedAt)).
		Set("finished_at", isoformatPtr(task.FinishedAt))
}

// serializeBook mirrors views._serialize_book.
func serializeBook(book Book) *pyObj {
	var uploaded any
	if book.UploadedBookID != nil {
		uploaded = *book.UploadedBookID
	}
	var createdAt any
	if book.CreatedAt != nil {
		createdAt = isoformat(*book.CreatedAt)
	}
	return newPyObj().
		Set("id", book.ID).
		Set("source", book.Source).
		Set("filename", book.Filename).
		Set("display_name", book.DisplayName).
		Set("size", book.Size).
		Set("uploaded_book_id", uploaded).
		Set("created_at", createdAt)
}
