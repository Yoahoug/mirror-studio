package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"mirror-studio/internal/db"
)

// queryer is satisfied by both *db.DB and *db.Tx, so every store helper can run
// inside or outside a WithImmediate transaction.
type queryer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

const imageColumns = `i.id, i.task_id, i.user_id, t.task_no, t.prompt, i.status, i.image_index,
	i.width, i.height, i.file_size, i.mime_type, i.object_key, i.bucket_name, i.preview_object_key,
	i.sha256, i.created_at, i.updated_at, i.expires_at, i.deleted_at`

// imageScanShape is the shared row shape of every image SELECT.
func scanImage(scanner interface{ Scan(...any) error }) (*Image, error) {
	var (
		image     Image
		createdAt string
		updatedAt string
		expiresAt sql.NullString
		deletedAt sql.NullString
	)
	if err := scanner.Scan(
		&image.ID, &image.TaskID, &image.UserID, &image.TaskNo, &image.Prompt, &image.Status,
		&image.ImageIndex, &image.Width, &image.Height, &image.FileSize, &image.MimeType,
		&image.ObjectKey, &image.BucketName, &image.PreviewObjectKey, &image.SHA256,
		&createdAt, &updatedAt, &expiresAt, &deletedAt,
	); err != nil {
		return nil, err
	}
	image.CreatedAt, _ = db.ParseTime(createdAt)
	image.UpdatedAt, _ = db.ParseTime(updatedAt)
	if expiresAt.Valid {
		image.ExpiresAt, _ = db.ParseTime(expiresAt.String)
	}
	image.DeletedAt = db.TimePtr(deletedAt)
	return &image, nil
}

const imageSelectBase = `SELECT ` + imageColumns + `
	FROM mediahub_generatedimage i
	JOIN generator_generatetask t ON t.id = i.task_id`

func (s *Service) insertImage(ctx context.Context, q queryer, image *Image) (int64, error) {
	now := db.FormatTime(time.Now())
	result, err := q.Exec(`INSERT INTO mediahub_generatedimage
		(created_at, updated_at, image_index, storage_provider, bucket_name, object_key, mime_type,
		 file_size, width, height, sha256, status, expires_at, deleted_at, task_id, user_id, preview_object_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, image.ImageIndex, StorageProviderLocal, BucketName, image.ObjectKey, image.MimeType,
		image.FileSize, image.Width, image.Height, image.SHA256, StatusAvailable, db.FormatTime(image.ExpiresAt),
		nil, image.TaskID, image.UserID, image.PreviewObjectKey,
	)
	if err != nil {
		return 0, err
	}
	image.CreatedAt, _ = db.ParseTime(now)
	image.UpdatedAt = image.CreatedAt
	return result.LastInsertId()
}

func (s *Service) imageByID(ctx context.Context, userID, imageID int64) (*Image, error) {
	return s.imageByIDQuery(ctx, imageSelectBase+` WHERE i.id = ? AND i.user_id = ? AND t.is_deleted = 0`, imageID, userID)
}

func (s *Service) imageByIDQuery(ctx context.Context, query string, args ...any) (*Image, error) {
	image, err := scanImage(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return image, nil
}

func (s *Service) listImages(ctx context.Context, userID int64, status, keyword string, limit int) ([]*Image, error) {
	query := imageSelectBase + ` WHERE i.user_id = ? AND t.is_deleted = 0`
	args := []any{userID}
	if strings.TrimSpace(status) != "" {
		query += ` AND i.status = ?`
		args = append(args, status)
	}
	if strings.TrimSpace(keyword) != "" {
		query += ` AND t.prompt LIKE ? ESCAPE '\'`
		args = append(args, likeContains(keyword))
	}
	query += ` ORDER BY i.created_at DESC, i.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	images := []*Image{}
	for rows.Next() {
		image, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, image)
	}
	return images, rows.Err()
}

// likeContains builds a case-insensitive contains pattern, escaping the LIKE
// wildcards the way the reference's icontains lookup does.
func likeContains(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(value) + "%"
}

func (s *Service) updateImagePreviewKey(ctx context.Context, q queryer, imageID int64, previewKey string) error {
	_, err := q.Exec(
		`UPDATE mediahub_generatedimage SET preview_object_key = ?, updated_at = ? WHERE id = ?`,
		previewKey, db.FormatTime(time.Now()), imageID,
	)
	return err
}

// expireImageRow flips a row to `expired` and records when it happened.
func (s *Service) expireImageRow(ctx context.Context, q queryer, imageID int64, when time.Time) error {
	_, err := q.Exec(
		`UPDATE mediahub_generatedimage SET status = ?, deleted_at = ?, updated_at = ? WHERE id = ?`,
		StatusExpired, db.FormatTime(when), db.FormatTime(when), imageID,
	)
	return err
}

// imagesToExpire lists the rows whose retention window has passed.
func (s *Service) imagesToExpire(ctx context.Context, limit int, now time.Time) ([]*Image, error) {
	rows, err := s.db.QueryContext(ctx, imageSelectBase+`
		WHERE i.status = ? AND i.expires_at < ? ORDER BY i.expires_at, i.id LIMIT ?`,
		StatusAvailable, db.FormatTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectImages(rows)
}

// imagesToPurge lists soft-deleted rows whose retention window has passed and
// that still hold an object.
func (s *Service) imagesToPurge(ctx context.Context, limit int, cutoff time.Time) ([]*Image, error) {
	rows, err := s.db.QueryContext(ctx, imageSelectBase+`
		WHERE i.status = ? AND i.deleted_at <= ? AND i.object_key <> ''
		ORDER BY i.deleted_at, i.id LIMIT ?`,
		StatusDeleted, db.FormatTime(cutoff), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectImages(rows)
}

func collectImages(rows *sql.Rows) ([]*Image, error) {
	images := []*Image{}
	for rows.Next() {
		image, err := scanImage(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, image)
	}
	return images, rows.Err()
}

// purgeImage clears the storage references of a purged row.
func (s *Service) purgeImage(ctx context.Context, q queryer, imageID int64) error {
	_, err := q.Exec(`UPDATE mediahub_generatedimage
		SET object_key = '', preview_object_key = '', file_size = 0, updated_at = ?
		WHERE id = ?`, db.FormatTime(time.Now()), imageID)
	return err
}

// ── upload sessions ──

const uploadSessionColumns = `id, created_at, updated_at, upload_id, client_upload_id, kind, filename,
	content_type, total_size, chunk_size, total_chunks, uploaded_chunks, status, result_json,
	expires_at, completed_at, user_id`

func scanUploadSession(scanner interface{ Scan(...any) error }) (*UploadSession, error) {
	var (
		session      UploadSession
		createdAt    string
		updatedAt    string
		uploadedText string
		resultText   string
		expiresAt    string
		completedAt  sql.NullString
	)
	if err := scanner.Scan(
		&session.ID, &createdAt, &updatedAt, &session.UploadID, &session.ClientUploadID, &session.Kind,
		&session.Filename, &session.ContentType, &session.TotalSize, &session.ChunkSize,
		&session.TotalChunks, &uploadedText, &session.Status, &resultText, &expiresAt, &completedAt,
		&session.UserID,
	); err != nil {
		return nil, err
	}
	session.CreatedAt, _ = db.ParseTime(createdAt)
	session.UpdatedAt, _ = db.ParseTime(updatedAt)
	session.ExpiresAt, _ = db.ParseTime(expiresAt)
	session.CompletedAt = db.TimePtr(completedAt)
	session.UploadedChunks = decodeChunkIndexes(uploadedText)
	session.Result = decodeResultJSON(resultText)
	return &session, nil
}

func decodeChunkIndexes(payload string) []int {
	indexes := []int{}
	if strings.TrimSpace(payload) == "" {
		return indexes
	}
	if err := json.Unmarshal([]byte(payload), &indexes); err != nil {
		return []int{}
	}
	return indexes
}

func decodeResultJSON(payload string) map[string]any {
	result := map[string]any{}
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" || trimmed == "null" {
		return result
	}
	if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
		return map[string]any{}
	}
	return result
}

func (s *Service) uploadSessionForUser(ctx context.Context, q queryer, userID int64, uploadID string) (*UploadSession, error) {
	session, err := scanUploadSession(q.QueryRow(
		`SELECT `+uploadSessionColumns+` FROM mediahub_uploadsession WHERE user_id = ? AND upload_id = ?`,
		userID, strings.TrimSpace(uploadID)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}

// resumableSessionForClientID finds the newest session a client can continue.
func (s *Service) resumableSessionForClientID(
	ctx context.Context, q queryer, userID int64, kind, clientUploadID string, totalSize int64,
) (*UploadSession, error) {
	session, err := scanUploadSession(q.QueryRow(`SELECT `+uploadSessionColumns+`
		FROM mediahub_uploadsession
		WHERE user_id = ? AND kind = ? AND client_upload_id = ? AND total_size = ? AND status IN (?, ?)
		ORDER BY created_at DESC, id DESC LIMIT 1`,
		userID, kind, clientUploadID, totalSize, UploadStatusUploading, UploadStatusCompleted))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (s *Service) insertUploadSession(ctx context.Context, q queryer, session *UploadSession) (int64, error) {
	now := db.FormatTime(time.Now())
	result, err := q.Exec(`INSERT INTO mediahub_uploadsession
		(created_at, updated_at, upload_id, client_upload_id, kind, filename, content_type, total_size,
		 chunk_size, total_chunks, uploaded_chunks, status, result_json, expires_at, completed_at, user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, session.UploadID, session.ClientUploadID, session.Kind, session.Filename,
		session.ContentType, session.TotalSize, session.ChunkSize, session.TotalChunks,
		pythonJSONDump(session.UploadedChunks), session.Status,
		pythonJSONDump(session.Result), db.FormatTime(session.ExpiresAt), nil, session.UserID,
	)
	if err != nil {
		return 0, err
	}
	session.CreatedAt, _ = db.ParseTime(now)
	session.UpdatedAt = session.CreatedAt
	return result.LastInsertId()
}

func (s *Service) updateUploadSessionMeta(ctx context.Context, q queryer, session *UploadSession) error {
	_, err := q.Exec(`UPDATE mediahub_uploadsession
		SET filename = ?, content_type = ?, expires_at = ?, updated_at = ? WHERE id = ?`,
		session.Filename, session.ContentType, db.FormatTime(session.ExpiresAt),
		db.FormatTime(time.Now()), session.ID)
	return err
}

func (s *Service) updateUploadSessionChunks(ctx context.Context, q queryer, session *UploadSession) error {
	_, err := q.Exec(`UPDATE mediahub_uploadsession
		SET uploaded_chunks = ?, expires_at = ?, updated_at = ? WHERE id = ?`,
		pythonJSONDump(session.UploadedChunks), db.FormatTime(session.ExpiresAt),
		db.FormatTime(time.Now()), session.ID)
	return err
}

func (s *Service) updateUploadSessionStatus(ctx context.Context, q queryer, sessionID int64, status string, when time.Time) error {
	_, err := q.Exec(`UPDATE mediahub_uploadsession SET status = ?, updated_at = ? WHERE id = ?`,
		status, db.FormatTime(when), sessionID)
	return err
}

// updateUploadSessionCompleted stores the finished result. The ordered form is
// what gets written: result_json is compared byte for byte.
func (s *Service) updateUploadSessionCompleted(ctx context.Context, q queryer, session *UploadSession, result jsonObject) error {
	completedAt := time.Now()
	encoded := pythonJSONDump(result)
	_, err := q.Exec(`UPDATE mediahub_uploadsession
		SET status = ?, result_json = ?, completed_at = ?, updated_at = ? WHERE id = ?`,
		UploadStatusCompleted, encoded, db.FormatTime(completedAt),
		db.FormatTime(completedAt), session.ID)
	if err != nil {
		return err
	}
	session.Status = UploadStatusCompleted
	session.CompletedAt = &completedAt
	session.UpdatedAt = completedAt
	session.Result = result.AsMap()
	return nil
}

func (s *Service) uploadSessionsToExpire(ctx context.Context, limit int, now time.Time) ([]*UploadSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+uploadSessionColumns+`
		FROM mediahub_uploadsession WHERE status = ? AND expires_at <= ?
		ORDER BY expires_at, id LIMIT ?`, UploadStatusUploading, db.FormatTime(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := []*UploadSession{}
	for rows.Next() {
		session, err := scanUploadSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}
