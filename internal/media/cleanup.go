package media

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// CleanupStorage runs one retention pass over the media library and reports how
// many rows each category touched.
func (s *Service) CleanupStorage(ctx context.Context, limit int) (map[string]int, error) {
	expired, err := s.expireAvailableImages(ctx, limit)
	if err != nil {
		return nil, err
	}
	temporary, err := s.expireTemporaryReferenceImages(limit)
	if err != nil {
		return nil, err
	}
	sessions, err := s.ExpireUploadSessions(ctx, limit)
	if err != nil {
		return nil, err
	}
	purged, err := s.purgeDeletedImageFiles(ctx, limit)
	if err != nil {
		return nil, err
	}
	return map[string]int{
		"expired_images":             expired,
		"temporary_reference_images": temporary,
		"upload_sessions":            sessions,
		"deleted_image_files":        purged,
	}, nil
}

// expireAvailableImages moves the rows whose retention window has passed to
// `expired` and then removes their objects. The status change happens first
// (review P1-22) so a failure between the two steps leaves a row the next pass
// can retry, and a reader never sees a row pointing at a half-deleted object.
func (s *Service) expireAvailableImages(ctx context.Context, limit int) (int, error) {
	now := time.Now()
	images, err := s.imagesToExpire(ctx, limit, now)
	if err != nil {
		return 0, err
	}
	expired := make([]*Image, 0, len(images))
	for _, image := range images {
		if err := s.expireImageRow(ctx, s.db, image.ID, now); err != nil {
			s.logger().Warn("image expiry failed", "image_id", image.ID, "error", err)
			continue
		}
		image.Status = StatusExpired
		expired = append(expired, image)
	}
	for _, image := range expired {
		s.removeImageObjects(image)
	}
	return len(expired), nil
}

// purgeDeletedImageFiles removes the objects of soft-deleted rows once the
// retention window has passed.
func (s *Service) purgeDeletedImageFiles(ctx context.Context, limit int) (int, error) {
	retentionDays := s.cfg.SoftDeleteRetentionDays
	if retentionDays < 0 {
		retentionDays = 0
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	images, err := s.imagesToPurge(ctx, limit, cutoff)
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, image := range images {
		s.removeImageObjects(image)
		if err := s.purgeImage(ctx, s.db, image.ID); err != nil {
			s.logger().Warn("image purge failed", "image_id", image.ID, "error", err)
			continue
		}
		purged++
	}
	return purged, nil
}

func (s *Service) removeImageObjects(image *Image) {
	if err := s.deleteObject(image.ObjectKey); err != nil {
		s.logger().Warn("image object delete failed",
			"image_id", image.ID, "object_key", image.ObjectKey, "error", err)
	}
	if image.PreviewObjectKey != "" {
		if err := s.deleteObject(image.PreviewObjectKey); err != nil {
			s.logger().Warn("preview object delete failed",
				"image_id", image.ID, "object_key", image.PreviewObjectKey, "error", err)
		}
	}
}

// expireTemporaryReferenceImages drops the reference objects whose upload token
// has expired; permanent task references keep their name and are untouched.
func (s *Service) expireTemporaryReferenceImages(limit int) (int, error) {
	cutoff := time.Now().Add(-ReferenceUploadTokenMaxAge)
	root := filepath.Join(s.cfg.MediaRoot, "users")
	matches, err := filepath.Glob(filepath.Join(root, "*", "tasks", "R*", "reference*"))
	if err != nil {
		return 0, err
	}
	type candidate struct {
		path    string
		modTime time.Time
	}
	candidates := make([]candidate, 0, len(matches))
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil || info.IsDir() {
			continue
		}
		candidates = append(candidates, candidate{path: match, modTime: info.ModTime()})
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].modTime.Before(candidates[right].modTime)
	})
	mediaRoot := filepath.Clean(s.cfg.MediaRoot)
	deleted := 0
	for _, item := range candidates {
		if deleted >= limit {
			break
		}
		if item.modTime.After(cutoff) {
			continue
		}
		relative, err := filepath.Rel(mediaRoot, item.path)
		if err != nil {
			continue
		}
		if err := s.deleteObject(filepath.ToSlash(relative)); err != nil {
			s.logger().Warn("temporary reference delete failed", "object_key", relative, "error", err)
			continue
		}
		deleted++
	}
	return deleted, nil
}
