package media

import (
	"context"
	"strconv"
	"strings"
	"time"

	"mirror-studio/internal/crypt"
	"mirror-studio/internal/httpapi"
)

// StoreGeneratedImage writes the original object, generates its preview and
// records the row. The original is never resized: its dimensions are part of
// the public contract.
func (s *Service) StoreGeneratedImage(ctx context.Context, in StoreImageInput) (*Image, error) {
	key := objectKey(in.UserID, in.TaskNo, in.Index, in.MimeType)
	width, height := imageDimensions(in.Content)
	if err := s.writeObject(key, in.Content); err != nil {
		return nil, err
	}
	previewKey := s.buildAndStorePreview(in.UserID, in.TaskNo, in.Index, in.Content)
	image := &Image{
		TaskID:           in.TaskID,
		UserID:           in.UserID,
		TaskNo:           in.TaskNo,
		Prompt:           in.Prompt,
		Status:           StatusAvailable,
		ImageIndex:       in.Index,
		Width:            width,
		Height:           height,
		FileSize:         int64(len(in.Content)),
		MimeType:         in.MimeType,
		ObjectKey:        key,
		BucketName:       BucketName,
		PreviewObjectKey: previewKey,
		SHA256:           sha256Hex(in.Content),
		ExpiresAt:        time.Now().AddDate(0, 0, s.cfg.ImageRetentionDays),
	}
	identifier, err := s.insertImage(ctx, s.db, image)
	if err != nil {
		return nil, err
	}
	image.ID = identifier
	return image, nil
}

// buildAndStorePreview renders the thumbnail once at storage time so a preview
// request never decodes the full-resolution image (review P2-12). A failure
// only costs the fast path; the original stays readable.
func (s *Service) buildAndStorePreview(userID int64, taskNo string, imageIndex int, content []byte) string {
	maxDimension := s.cfg.ImagePreviewMaxDimension
	if maxDimension <= 0 {
		maxDimension = 768
	}
	preview, renderMIME, ok := renderPreview(content, maxDimension)
	if !ok {
		s.logger().Warn("preview render skipped",
			"task_no", taskNo, "image_index", imageIndex, "size", len(content))
		return ""
	}
	key := previewObjectKey(userID, taskNo, imageIndex, renderMIME)
	if err := s.writeObject(key, preview); err != nil {
		s.logger().Warn("preview thumbnail store failed", "task_no", taskNo, "error", err)
		return ""
	}
	return key
}

// StoreReferenceImage keeps a reference image for a generation task and returns
// the storage metadata the signed upload token carries. Numeric entries are
// decimal strings because the frozen cross-package signature is
// map[string]string; build the token payload in ReferenceMetaKeys order.
func (s *Service) StoreReferenceImage(ctx context.Context, userID int64, taskNo string, content []byte, mimeType string) (map[string]string, error) {
	ordered, err := s.storeReferenceImageOrdered(userID, taskNo, content, mimeType)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(ordered))
	for _, field := range ordered {
		switch typed := field.value.(type) {
		case string:
			result[field.key] = typed
		case int:
			result[field.key] = strconv.Itoa(typed)
		}
	}
	return result, nil
}

// ReferenceMetaKeys is the key order the reference expands the upload metadata
// with. The signed token payload is JSON, so a different order is still
// verifiable, but this is the order the reference stores.
var ReferenceMetaKeys = []string{
	"reference_storage_provider",
	"reference_bucket_name",
	"reference_object_key",
	"reference_image_mime_type",
	"reference_image_size",
	"reference_image_width",
	"reference_image_height",
	"reference_image_sha256",
}

// storeReferenceImageOrdered writes the reference object and returns its
// metadata in the reference's key order.
func (s *Service) storeReferenceImageOrdered(userID int64, taskNo string, content []byte, mimeType string) (jsonObject, error) {
	key := referenceObjectKey(userID, taskNo, mimeType)
	width, height := imageDimensions(content)
	if err := s.writeObject(key, content); err != nil {
		return nil, err
	}
	return jsonObject{
		{"reference_storage_provider", StorageProviderLocal},
		{"reference_bucket_name", BucketName},
		{"reference_object_key", key},
		{"reference_image_mime_type", mimeType},
		{"reference_image_size", len(content)},
		{"reference_image_width", width},
		{"reference_image_height", height},
		{"reference_image_sha256", sha256Hex(content)},
	}, nil
}

// ReadObject returns a stored object. The bucket argument is accepted for
// signature compatibility; the local backend has a single root.
func (s *Service) ReadObject(ctx context.Context, bucket, key string) ([]byte, error) {
	return s.readObject(key)
}

// DeleteObject removes one stored object; a missing object is not an error.
func (s *Service) DeleteObject(ctx context.Context, bucket, key string) error {
	return s.deleteObject(key)
}

// ImageCount reports how many image rows exist, which the console dashboard
// shows as its `images` total.
func (s *Service) ImageCount(ctx context.Context) (int64, error) {
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM mediahub_generatedimage`).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// IssueDownloadURL signs a link for one image. Preview links are stable for a
// given image version so the browser can cache them (review P1-21); plain links
// expire after five minutes.
func (s *Service) IssueDownloadURL(ctx context.Context, image *Image, userID int64, inline, preview bool) (string, error) {
	var token string
	if preview {
		signer := crypt.NewSigner(s.cfg.SecretKey, s.cfg.SecretKey+previewSignerSalt)
		identity := "p:" + strconv.FormatInt(image.ID, 10) + ":" + strconv.FormatInt(userID, 10) +
			":" + strconv.FormatInt(previewStamp(image.UpdatedAt), 10)
		token = signer.Sign(identity)
	} else {
		signer := crypt.NewTimestampSigner(s.cfg.SecretKey, "")
		token = signer.Sign(strconv.FormatInt(image.ID, 10) + ":" + strconv.FormatInt(userID, 10))
	}
	query := make([]string, 0, 2)
	if inline {
		query = append(query, "disposition=inline")
	}
	if preview {
		query = append(query, "preview=1")
	}
	suffix := ""
	if len(query) > 0 {
		suffix = "?" + strings.Join(query, "&")
	}
	return "/downloads/" + httpapi.PathEscapePercent(token) + "/" + suffix, nil
}

// previewStamp reproduces python's f"{timestamp:.0f}" rounding to whole
// seconds, ties to even, without the precision loss of a float conversion.
func previewStamp(value time.Time) int64 {
	utc := value.UTC()
	seconds := utc.Unix()
	nanos := utc.Nanosecond()
	switch {
	case nanos > 500_000_000:
		return seconds + 1
	case nanos < 500_000_000:
		return seconds
	case seconds%2 == 0:
		return seconds
	}
	return seconds + 1
}

// ResolveDownloadToken verifies a signed token: the stable preview signature
// first, then the timestamped one, exactly like the reference.
func (s *Service) ResolveDownloadToken(ctx context.Context, token string, maxAge time.Duration) (*Image, error) {
	imageID, userID, ok := previewTokenIdentity(s.cfg.SecretKey, token)
	if !ok {
		signer := crypt.NewTimestampSigner(s.cfg.SecretKey, "")
		unsigned, err := signer.Unsign(token, maxAge)
		if err != nil {
			return nil, ErrImageNotFound
		}
		index := strings.LastIndex(unsigned, ":")
		if index < 0 {
			return nil, ErrImageNotFound
		}
		parts := strings.Split(unsigned[:index], ":")
		if len(parts) != 2 {
			return nil, ErrImageNotFound
		}
		if imageID, err = strconv.ParseInt(parts[0], 10, 64); err != nil {
			return nil, ErrImageNotFound
		}
		if userID, err = strconv.ParseInt(parts[1], 10, 64); err != nil {
			return nil, ErrImageNotFound
		}
	}
	image, err := s.imageByIDQuery(ctx, imageSelectBase+
		` WHERE i.id = ? AND i.user_id = ? AND t.is_deleted = 0`, imageID, userID)
	if err != nil {
		return nil, err
	}
	if image == nil {
		return nil, ErrImageNotFound
	}
	return image, nil
}

func previewTokenIdentity(secretKey, token string) (int64, int64, bool) {
	signer := crypt.NewSigner(secretKey, secretKey+previewSignerSalt)
	unsigned, err := signer.Unsign(token)
	if err != nil {
		return 0, 0, false
	}
	parts := strings.SplitN(unsigned, ":", 4)
	if len(parts) != 4 || parts[0] != "p" {
		return 0, 0, false
	}
	imageID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	userID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return imageID, userID, true
}

// BuildDownloadResponse assembles the payload and the headers of
// GET /downloads/{token}/. Previews are served from the pre-generated object
// and built on demand for rows that predate it.
func (s *Service) BuildDownloadResponse(ctx context.Context, image *Image, inline, preview bool) ([]byte, string, string, string, error) {
	var (
		content     []byte
		contentType string
		err         error
	)
	if preview {
		content, contentType, err = s.previewContent(image)
	} else {
		content, contentType, err = s.originalContent(image)
	}
	if err != nil {
		return nil, "", "", "", err
	}
	filename := s.downloadFilename(image)
	mode := "attachment"
	if inline {
		mode = "inline"
	}
	extension := extensionForMIME(contentType)
	if extension == "" {
		extension = pathExtension(filename)
	}
	if extension == "" {
		extension = ".png"
	}
	disposition := mode + `; filename="generated-image` + extension + `"; filename*=UTF-8''` +
		httpapi.PathEscapePercent(filename)
	cacheControl := "private, max-age=300"
	if preview {
		cacheControl = "private, max-age=86400, immutable"
	}
	return content, contentType, disposition, cacheControl, nil
}

func (s *Service) originalContent(image *Image) ([]byte, string, error) {
	if image.Status != StatusAvailable {
		return nil, "", ErrImageUnavailable
	}
	content, err := s.readObject(image.ObjectKey)
	if err != nil {
		return nil, "", err
	}
	return content, image.MimeType, nil
}

// previewContent prefers the object rendered at storage time and falls back to
// an on-demand render for legacy rows, persisting the result for the next call.
func (s *Service) previewContent(image *Image) ([]byte, string, error) {
	if cached, mimeType, ok := s.cachedPreview(image); ok {
		return cached, mimeType, nil
	}
	content, _, err := s.originalContent(image)
	if err != nil {
		return nil, "", err
	}
	maxDimension := s.cfg.ImagePreviewMaxDimension
	if maxDimension < 64 {
		maxDimension = 64
	}
	rendered, renderMIME, ok := renderPreview(content, maxDimension)
	if !ok {
		return content, image.MimeType, nil
	}
	key := previewObjectKey(image.UserID, strconv.FormatInt(image.TaskID, 10), image.ImageIndex, renderMIME)
	if err := s.writeObject(key, rendered); err != nil {
		s.logger().Warn("preview lazy backfill failed", "image_id", image.ID, "error", err)
		return rendered, renderMIME, nil
	}
	if image.PreviewObjectKey != key {
		if err := s.updateImagePreviewKey(context.Background(), s.db, image.ID, key); err != nil {
			s.logger().Warn("preview backfill record failed", "image_id", image.ID, "error", err)
		} else {
			image.PreviewObjectKey = key
		}
	}
	return rendered, renderMIME, nil
}

func (s *Service) cachedPreview(image *Image) ([]byte, string, bool) {
	previewKey := strings.TrimSpace(image.PreviewObjectKey)
	if previewKey == "" {
		return nil, "", false
	}
	content, err := s.readObject(previewKey)
	if err != nil {
		s.logger().Warn("preview object read failed", "image_id", image.ID, "error", err)
		return nil, "", false
	}
	if len(content) == 0 {
		return nil, "", false
	}
	mimeType := "image/png"
	if strings.HasSuffix(previewKey, ".jpg") {
		mimeType = "image/jpeg"
	}
	return content, mimeType, true
}

// downloadFilename derives the user-visible name from the prompt, falling back
// to the task number.
func (s *Service) downloadFilename(image *Image) string {
	prompt := removeFilenameSymbols(collapseWhitespace(strings.TrimSpace(image.Prompt)))
	prompt = truncateRunes(prompt, 48)
	stem := strings.TrimSpace(prompt)
	if stem == "" {
		stem = image.TaskNo
	}
	return stem + extensionOrPNG(image.MimeType)
}

// HistoryImages returns the newest images of one user for the SSR history page.
func (s *Service) HistoryImages(ctx context.Context, userID int64, limit int) ([]map[string]any, error) {
	images, err := s.listImages(ctx, userID, "", "", limit)
	if err != nil {
		return nil, err
	}
	payload := make([]map[string]any, 0, len(images))
	for _, image := range images {
		payload = append(payload, s.historyItem(image))
	}
	return payload, nil
}

// HistoryDetail returns the images of one task plus the task header the detail
// page renders.
func (s *Service) HistoryDetail(ctx context.Context, userID int64, taskNo string) (map[string]any, []map[string]any, bool, error) {
	images, err := s.listTaskImages(ctx, userID, taskNo)
	if err != nil {
		return nil, nil, false, err
	}
	if len(images) == 0 {
		return nil, nil, false, nil
	}
	task := map[string]any{
		"id":      images[0].TaskID,
		"task_no": images[0].TaskNo,
		"prompt":  images[0].Prompt,
	}
	payload := make([]map[string]any, 0, len(images))
	for _, image := range images {
		payload = append(payload, s.historyItem(image))
	}
	return task, payload, true, nil
}

func (s *Service) listTaskImages(ctx context.Context, userID int64, taskNo string) ([]*Image, error) {
	query := imageSelectBase + ` WHERE i.user_id = ? AND t.task_no = ? AND t.is_deleted = 0
		ORDER BY i.image_index, i.id`
	rows, err := s.db.QueryContext(ctx, query, userID, taskNo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectImages(rows)
}

// historyItem is the shape the SSR templates consume.
func (s *Service) historyItem(image *Image) map[string]any {
	return map[string]any{
		"id":             image.ID,
		"task_id":        image.TaskID,
		"task_no":        image.TaskNo,
		"prompt":         image.Prompt,
		"status":         image.Status,
		"status_display": StatusDisplay(image.Status),
		"image_index":    image.ImageIndex,
		"width":          image.Width,
		"height":         image.Height,
		"file_size":      image.FileSize,
		"mime_type":      image.MimeType,
		"created_at":     image.CreatedAt,
		"expires_at":     image.ExpiresAt,
		"created_at_iso": pythonIsoformat(image.CreatedAt),
		"expires_at_iso": pythonIsoformat(image.ExpiresAt),
		"task": map[string]any{
			"id":      image.TaskID,
			"task_no": image.TaskNo,
			"prompt":  image.Prompt,
		},
	}
}

// StatusDisplay maps a status to the label the templates and the SPA show.
func StatusDisplay(status string) string {
	switch status {
	case StatusAvailable:
		return "可用"
	case StatusExpired:
		return "已过期"
	case StatusDeleted:
		return "已删除"
	}
	return status
}
