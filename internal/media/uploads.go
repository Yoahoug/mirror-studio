package media

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mirror-studio/internal/crypt"
	"mirror-studio/internal/db"
	"mirror-studio/internal/imageutil"
)

// ReferenceUploadTokenMaxAge is how long a signed reference-upload token stays
// resolvable, and therefore how long a temporary reference object lives.
const ReferenceUploadTokenMaxAge = 30 * time.Minute

// UploadSession is one row of mediahub_uploadsession.
type UploadSession struct {
	ID             int64
	UserID         int64
	UploadID       string
	ClientUploadID string
	Kind           string
	Filename       string
	ContentType    string
	TotalSize      int64
	ChunkSize      int64
	TotalChunks    int
	UploadedChunks []int
	Status         string
	Result         map[string]any
	ExpiresAt      time.Time
	CompletedAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Serialize is the payload every upload endpoint returns.
func (session *UploadSession) Serialize() map[string]any {
	result := session.Result
	if result == nil {
		result = map[string]any{}
	}
	return map[string]any{
		"upload_id":       session.UploadID,
		"kind":            session.Kind,
		"filename":        session.Filename,
		"content_type":    session.ContentType,
		"total_size":      session.TotalSize,
		"chunk_size":      session.ChunkSize,
		"total_chunks":    session.TotalChunks,
		"uploaded_chunks": sortedUniqueChunks(session.UploadedChunks),
		"status":          session.Status,
		"expires_at":      pythonIsoformat(session.ExpiresAt),
		"result":          result,
	}
}

// CreateOrResumeUploadSession starts a chunked upload, or hands back the
// session a client already started for the same file.
func (s *Service) CreateOrResumeUploadSession(
	ctx context.Context, userID int64, kind, filename, contentType string, totalSize any, clientUploadID string,
) (map[string]any, error) {
	kind = strings.TrimSpace(kind)
	if kind != KindGenerationReferenceImage && kind != KindNovelEPUB {
		return nil, invalidValue("不支持的上传类型")
	}
	size, ok := coerceInt64(totalSize)
	if !ok {
		return nil, invalidValue("文件大小不合法")
	}
	if size <= 0 {
		return nil, invalidValue("上传文件不能为空")
	}
	limit := s.uploadSizeLimit(kind)
	if size > limit {
		return nil, invalidValue("上传文件不能超过 %d MB", limit/1024/1024)
	}

	fallback := "reference-image"
	if kind == KindNovelEPUB {
		fallback = "novel.epub"
	}
	cleanFilename := safeUploadFilename(filename, fallback)
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if len(contentType) > 128 {
		contentType = contentType[:128]
	}
	clientUploadID = strings.TrimSpace(clientUploadID)
	if len(clientUploadID) > 128 {
		clientUploadID = clientUploadID[:128]
	}
	chunkSize := s.cfg.ResumableUploadChunkSize
	if chunkSize < 1 {
		chunkSize = 1
	}
	totalChunks := int((size + chunkSize - 1) / chunkSize)

	var session *UploadSession
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		var existing *UploadSession
		if clientUploadID != "" {
			found, err := s.resumableSessionForClientID(ctx, tx, userID, kind, clientUploadID, size)
			if err != nil {
				return err
			}
			existing = found
		}
		if existing != nil {
			if err := s.markUploadExpiredIfNeeded(ctx, tx, existing); err != nil {
				return err
			}
			switch {
			case existing.Status == UploadStatusExpired:
				s.cleanupUploadSessionFiles(existing)
				existing = nil
			case existing.Status == UploadStatusUploading && existing.ChunkSize != chunkSize:
				if err := s.abandonUploadSession(ctx, tx, existing); err != nil {
					return err
				}
				existing = nil
			}
		}
		if existing == nil {
			session = &UploadSession{
				UserID:         userID,
				UploadID:       randomHex(32),
				ClientUploadID: clientUploadID,
				Kind:           kind,
				Filename:       cleanFilename,
				ContentType:    contentType,
				TotalSize:      size,
				ChunkSize:      chunkSize,
				TotalChunks:    totalChunks,
				UploadedChunks: []int{},
				Status:         UploadStatusUploading,
				Result:         map[string]any{},
				ExpiresAt:      s.uploadSessionExpiry(),
			}
			identifier, err := s.insertUploadSession(ctx, tx, session)
			if err != nil {
				return err
			}
			session.ID = identifier
			return nil
		}
		session = existing
		if existing.Status == UploadStatusUploading {
			existing.Filename = cleanFilename
			existing.ContentType = contentType
			existing.ExpiresAt = s.uploadSessionExpiry()
			if err := s.updateUploadSessionMeta(ctx, tx, existing); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return session.Serialize(), nil
}

// GetUploadSession returns one session of the calling user.
func (s *Service) GetUploadSession(ctx context.Context, userID int64, uploadID string) (map[string]any, error) {
	var session *UploadSession
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		found, err := s.uploadSessionForUser(ctx, tx, userID, uploadID)
		if err != nil {
			return err
		}
		if found == nil {
			return ErrUploadSessionNotFound
		}
		if err := s.markUploadExpiredIfNeeded(ctx, tx, found); err != nil {
			return err
		}
		session = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return session.Serialize(), nil
}

// StoreUploadChunk writes one chunk of a session, idempotently.
func (s *Service) StoreUploadChunk(ctx context.Context, userID int64, uploadID string, index int, content []byte) (map[string]any, error) {
	if len(content) == 0 {
		return nil, invalidValue("分片内容不能为空")
	}
	var session *UploadSession
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		found, err := s.uploadSessionForUser(ctx, tx, userID, uploadID)
		if err != nil {
			return err
		}
		if found == nil {
			return ErrUploadSessionNotFound
		}
		if err := s.markUploadExpiredIfNeeded(ctx, tx, found); err != nil {
			return err
		}
		if found.Status != UploadStatusUploading {
			return invalidValue("上传会话不可继续上传")
		}
		if index < 0 || index >= found.TotalChunks {
			return invalidValue("分片序号超出范围")
		}
		expected := found.ChunkSize
		if index == found.TotalChunks-1 {
			expected = found.TotalSize - found.ChunkSize*int64(found.TotalChunks-1)
		}
		if int64(len(content)) != expected {
			return invalidValue("分片大小不匹配")
		}
		if err := s.writeChunk(found, index, content); err != nil {
			return err
		}
		found.UploadedChunks = sortedUniqueChunks(append(found.UploadedChunks, index))
		found.ExpiresAt = s.uploadSessionExpiry()
		if err := s.updateUploadSessionChunks(ctx, tx, found); err != nil {
			return err
		}
		session = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return session.Serialize(), nil
}

// writeChunk stores a chunk, skipping the write when the same bytes are already
// there.
func (s *Service) writeChunk(session *UploadSession, index int, content []byte) error {
	target := s.chunkPath(session, index)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(target); err == nil && info.Size() == int64(len(content)) {
		existing, err := fileSHA256(target)
		if err == nil && existing == sha256Hex(content) {
			return nil
		}
	}
	return os.WriteFile(target, content, 0o644)
}

// CompleteUploadSession assembles the chunks and finalises the session. It is
// idempotent: a session that is already completed is returned unchanged.
func (s *Service) CompleteUploadSession(ctx context.Context, userID int64, uploadID string) (map[string]any, error) {
	var session *UploadSession
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		found, err := s.uploadSessionForUser(ctx, tx, userID, uploadID)
		if err != nil {
			return err
		}
		if found == nil {
			return ErrUploadSessionNotFound
		}
		if found.Status == UploadStatusCompleted {
			session = found
			return nil
		}
		if found.Status != UploadStatusUploading {
			return invalidValue("上传会话不可完成")
		}
		assembledPath, digest, err := s.assembleUpload(found)
		if err != nil {
			return err
		}
		var result jsonObject
		switch found.Kind {
		case KindGenerationReferenceImage:
			result, err = s.completeReferenceUpload(ctx, found, assembledPath)
		case KindNovelEPUB:
			result, err = s.completeNovelEPUBUpload(ctx, tx, found, assembledPath, digest)
		default:
			err = invalidValue("不支持的上传类型")
		}
		if err != nil {
			return err
		}
		if err := s.updateUploadSessionCompleted(ctx, tx, found, result); err != nil {
			return err
		}
		session = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.cleanupUploadSessionFiles(session)
	return session.Serialize(), nil
}

// CancelUploadSession abandons a session and removes its files.
func (s *Service) CancelUploadSession(ctx context.Context, userID int64, uploadID string) (map[string]any, error) {
	var session *UploadSession
	err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
		found, err := s.uploadSessionForUser(ctx, tx, userID, uploadID)
		if err != nil {
			return err
		}
		if found == nil {
			return ErrUploadSessionNotFound
		}
		if found.Status == UploadStatusUploading {
			if err := s.updateUploadSessionStatus(ctx, tx, found.ID, UploadStatusCanceled, time.Now()); err != nil {
				return err
			}
			found.Status = UploadStatusCanceled
		}
		session = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.cleanupUploadSessionFiles(session)
	return session.Serialize(), nil
}

// ExpireUploadSessions sweeps the sessions whose TTL has passed.
func (s *Service) ExpireUploadSessions(ctx context.Context, limit int) (int, error) {
	sessions, err := s.uploadSessionsToExpire(ctx, limit, time.Now())
	if err != nil {
		return 0, err
	}
	for _, session := range sessions {
		if err := s.db.WithImmediate(ctx, func(tx *db.Tx) error {
			return s.updateUploadSessionStatus(ctx, tx, session.ID, UploadStatusExpired, time.Now())
		}); err != nil {
			s.logger().Warn("upload session expiry failed", "upload_id", session.UploadID, "error", err)
			continue
		}
		session.Status = UploadStatusExpired
		s.cleanupUploadSessionFiles(session)
	}
	return len(sessions), nil
}

func (s *Service) uploadSizeLimit(kind string) int64 {
	if kind == KindNovelEPUB {
		return s.cfg.NovelvizMaxEpubBytes
	}
	return s.cfg.MaxReferenceImageBytes
}

func (s *Service) uploadSessionExpiry() time.Time {
	ttl := s.cfg.ResumableUploadSessionTTL
	if ttl < 60*time.Second {
		ttl = 60 * time.Second
	}
	return time.Now().Add(ttl)
}

// markUploadExpiredIfNeeded flips a session past its TTL, which is what every
// read path reports.
func (s *Service) markUploadExpiredIfNeeded(ctx context.Context, q queryer, session *UploadSession) error {
	if session.Status != UploadStatusUploading || session.ExpiresAt.After(time.Now()) {
		return nil
	}
	if err := s.updateUploadSessionStatus(ctx, q, session.ID, UploadStatusExpired, time.Now()); err != nil {
		return err
	}
	session.Status = UploadStatusExpired
	return nil
}

// abandonUploadSession cancels a session whose chunk size no longer matches the
// configured one.
func (s *Service) abandonUploadSession(ctx context.Context, q queryer, session *UploadSession) error {
	if err := s.updateUploadSessionStatus(ctx, q, session.ID, UploadStatusCanceled, time.Now()); err != nil {
		return err
	}
	session.Status = UploadStatusCanceled
	s.cleanupUploadSessionFiles(session)
	return nil
}

// assembleUpload concatenates the chunks in order and reports their digest. The
// digest is recorded, not compared: the protocol carries no client checksum
// (review P1-24).
func (s *Service) assembleUpload(session *UploadSession) (string, string, error) {
	uploaded := make(map[int]bool, len(session.UploadedChunks))
	for _, index := range session.UploadedChunks {
		uploaded[index] = true
	}
	for index := 0; index < session.TotalChunks; index++ {
		if !uploaded[index] {
			return "", "", invalidValue("上传分片不完整，请继续上传")
		}
	}
	target := s.assembledPath(session)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", "", err
	}
	output, err := os.Create(target)
	if err != nil {
		return "", "", err
	}
	digest := sha256.New()
	total := int64(0)
	writer := io.MultiWriter(output, digest)
	for index := 0; index < session.TotalChunks; index++ {
		chunk := s.chunkPath(session, index)
		source, err := os.Open(chunk)
		if err != nil {
			output.Close()
			_ = os.Remove(target)
			return "", "", invalidValue("上传分片不完整，请继续上传")
		}
		written, copyErr := io.Copy(writer, source)
		source.Close()
		total += written
		if copyErr != nil {
			output.Close()
			_ = os.Remove(target)
			return "", "", copyErr
		}
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(target)
		return "", "", err
	}
	if total != session.TotalSize {
		_ = os.Remove(target)
		return "", "", invalidValue("上传文件大小校验失败，请重新上传")
	}
	return target, hex.EncodeToString(digest.Sum(nil)), nil
}

// completeReferenceUpload validates the assembled bytes, stores them as a
// reference object and issues the signed token the generation endpoint accepts.
func (s *Service) completeReferenceUpload(ctx context.Context, session *UploadSession, assembledPath string) (jsonObject, error) {
	content, err := os.ReadFile(assembledPath)
	if err != nil {
		return nil, err
	}
	validated, mimeType, imageName, err := s.validateReferenceImageContent(content, session.ContentType, session.Filename)
	if err != nil {
		return nil, err
	}
	taskNo := fmt.Sprintf("R%d%d", session.UserID, time.Now().UnixMilli())
	meta, err := s.storeReferenceImageOrdered(session.UserID, taskNo, validated, mimeType)
	if err != nil {
		return nil, err
	}
	payload := append(jsonObject{
		{"user_id", session.UserID},
		{"reference_image_name", imageName},
	}, meta...)
	token := crypt.NewTimestampSigner(s.cfg.SecretKey, referenceUploadToken).Sign(pythonJSONCompact(payload))
	return jsonObject{
		{"reference_upload_token", token},
		{"reference_image_name", imageName},
		{"reference_image_attached", true},
	}, nil
}

// validateReferenceImageContent mirrors the generator's reference validation:
// the mime type is normalised, the size capped and the payload must decode.
func (s *Service) validateReferenceImageContent(content []byte, mimeType, name string) ([]byte, string, string, error) {
	normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(mimeType)), "jpg", "jpeg")
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		trimmedName = "reference-image"
	}
	rawName := truncateRunes(path.Base(trimmedName), 255)
	switch normalized {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return nil, "", "", invalidValue("参考图仅支持 PNG、JPG、JPEG、WEBP")
	}
	if int64(len(content)) > s.cfg.MaxReferenceImageBytes {
		return nil, "", "", invalidValue("参考图过大，请压缩后再试")
	}
	// The reference runs Pillow's Image.verify over the whole file; a truncated
	// payload whose header parses must be rejected the same way.
	if _, _, err := imageutil.Decode(content); err != nil {
		return nil, "", "", invalidValue("参考图无法识别，请重新上传")
	}
	width, height := imageutil.DecodeConfig(content)
	if width == 0 || height == 0 {
		return nil, "", "", invalidValue("参考图无法识别，请重新上传")
	}
	return content, normalized, rawName, nil
}

// completeNovelEPUBUpload stores the assembled EPUB and records the uploaded
// book row the novel domain reads.
func (s *Service) completeNovelEPUBUpload(
	ctx context.Context, q queryer, session *UploadSession, assembledPath, digest string,
) (jsonObject, error) {
	displayName, err := cleanEPUBDisplayName(session.Filename)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(assembledPath)
	if err != nil {
		return nil, invalidValue("上传的 EPUB 文件不存在")
	}
	totalSize := info.Size()
	if totalSize <= 0 {
		return nil, invalidValue("上传的 EPUB 文件为空")
	}
	maxBytes := s.cfg.NovelvizMaxEpubBytes
	if totalSize > maxBytes {
		return nil, invalidValue("EPUB 文件不能超过 %d MB", maxBytes/1024/1024)
	}

	relativePath := path.Join(novelUploadRootDir, "users", strconv.FormatInt(session.UserID, 10),
		randomHex(32)+"-"+displayName)
	root := filepath.Clean(s.cfg.MediaRoot)
	absolute := filepath.Join(root, filepath.FromSlash(relativePath))
	if !strings.HasPrefix(absolute, root+string(filepath.Separator)) {
		return nil, invalidValue("上传路径不合法")
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return nil, err
	}
	if err := copyFile(assembledPath, absolute); err != nil {
		return nil, err
	}
	now := time.Now()
	result, err := q.Exec(`INSERT INTO novelviz_noveluploadedbook
		(created_at, updated_at, display_name, storage_path, file_size, sha256, user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		db.FormatTime(now), db.FormatTime(now), displayName, relativePath, totalSize, digest, session.UserID)
	if err != nil {
		_ = os.Remove(absolute)
		return nil, err
	}
	identifier, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	return jsonObject{
		{"id", fmt.Sprintf("uploaded:%d", identifier)},
		{"source", "uploaded"},
		{"filename", displayName},
		{"display_name", displayName},
		{"size", totalSize},
		{"uploaded_book_id", identifier},
		{"created_at", pythonIsoformat(now)},
	}, nil
}

// cleanEPUBDisplayName reproduces the novel domain's filename sanitiser: the
// name must end in .epub.
func cleanEPUBDisplayName(filename string) (string, error) {
	clean := path.Base(strings.TrimSpace(filename))
	if clean == "." || clean == "/" {
		clean = ""
	}
	clean = strings.Trim(unsafeFilenameChars.ReplaceAllString(clean, "-"), " .")
	if clean == "" {
		clean = "novel.epub"
	}
	if !strings.HasSuffix(strings.ToLower(clean), ".epub") {
		return "", invalidValue("请上传 EPUB 小说文件")
	}
	return truncateRunes(clean, 255), nil
}

func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

// coerceInt64 accepts the shapes a payload field can take, truncating floats
// exactly like python's int().
func coerceInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

func randomHex(length int) string {
	buffer := make([]byte, length/2)
	if _, err := rand.Read(buffer); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buffer)
}
