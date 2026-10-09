package novel

import (
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mirror-studio/internal/httpapi"
	"mirror-studio/internal/imageutil"
)

// onTheFlyPreviewDimension is the fixed 768 the reference view passes to its
// preview builder; the stored thumbnail uses IMAGE_PREVIEW_MAX_DIMENSION.
const onTheFlyPreviewDimension = 768

// scenePreviewJPEGQuality matches the reference's novelviz preview encoder.
const scenePreviewJPEGQuality = 82

var (
	sceneFilenameWhitespace = regexp.MustCompile(`\s+`)
	sceneFilenameForbidden  = regexp.MustCompile(`[\\/:*?"<>|]+`)
)

// sceneFilename mirrors views._scene_filename.
func sceneFilename(title string, sceneNumber int) string {
	stem := sceneFilenameWhitespace.ReplaceAllString(strings.TrimSpace(title), " ")
	stem = sceneFilenameForbidden.ReplaceAllString(stem, "")
	if len([]rune(stem)) > 48 {
		stem = string([]rune(stem)[:48])
	}
	stem = strings.TrimSpace(stem)
	if stem == "" {
		stem = fmt.Sprintf("scene-%02d", sceneNumber)
	}
	return stem + ".png"
}

// contentDisposition mirrors views._content_disposition.
func contentDisposition(filename string, inline bool) string {
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	return fmt.Sprintf("%s; filename=\"novel-scene.png\"; filename*=UTF-8''%s", disposition, httpapi.PathEscapePercent(filename))
}

// buildScenePreviewFiles mirrors services._build_scene_preview_files: it writes
// the thumbnail once, at scene-success time, so ?preview=1 never resizes on the
// request path (review P2-15).
func (s *Service) buildScenePreviewFiles(content []byte, relativeOriginal string) (string, string) {
	rendered, err := decodeSceneImage(content)
	if err != nil {
		return "", "image/png"
	}
	resized := imageutil.Resize(rendered, s.settings.previewMaxDimension)
	hasAlpha := imageutil.HasAlphaMode(content, rendered)
	renderMime := "image/jpeg"
	if hasAlpha {
		renderMime = "image/png"
	}
	payload, err := encodeScenePreview(resized, hasAlpha)
	if err != nil || len(payload) == 0 {
		return "", "image/png"
	}
	extension := ".jpg"
	if renderMime == "image/png" {
		extension = ".png"
	}
	base := strings.TrimSuffix(relativeOriginal, filepath.Ext(relativeOriginal))
	relativePreview := base + "-preview" + extension
	absolutePreview := filepath.Join(s.settings.mediaRoot, filepath.FromSlash(relativePreview))
	if err := os.MkdirAll(filepath.Dir(absolutePreview), 0o755); err != nil {
		return "", "image/png"
	}
	if err := os.WriteFile(absolutePreview, payload, 0o644); err != nil {
		return "", "image/png"
	}
	return relativePreview, renderMime
}

// renderScenePreview mirrors views._build_scene_preview_content.
func renderScenePreview(content []byte) ([]byte, string) {
	rendered, err := decodeSceneImage(content)
	if err != nil {
		return content, "image/png"
	}
	resized := imageutil.Resize(rendered, onTheFlyPreviewDimension)
	hasAlpha := imageutil.HasAlphaMode(content, rendered)
	payload, err := encodeScenePreview(resized, hasAlpha)
	if err != nil {
		return content, "image/png"
	}
	if hasAlpha {
		return payload, "image/png"
	}
	return payload, "image/jpeg"
}

func encodeScenePreview(source image.Image, hasAlpha bool) ([]byte, error) {
	if hasAlpha {
		return imageutil.EncodePNG(source, true)
	}
	return imageutil.EncodeJPEG(source, scenePreviewJPEGQuality)
}

// decodeSceneImage decodes and applies the EXIF orientation the reference
// applies before resizing, so a rotated photo renders the way its owner sees it.
func decodeSceneImage(content []byte) (image.Image, error) {
	decoded, _, err := imageutil.Decode(content)
	if err != nil {
		return nil, err
	}
	return imageutil.ApplyEXIFOrientation(content, decoded), nil
}

// readSceneFile loads a scene file from MEDIA_ROOT, rejecting any path that
// escapes the media root (the reference resolves and checks ancestry).
func (s *Service) readSceneFile(relative string) ([]byte, string, bool) {
	mediaRoot, err := resolveAbsolute(s.settings.mediaRoot)
	if err != nil {
		return nil, "", false
	}
	target := posixNormpathAbs(mediaRoot, filepath.FromSlash(relative))
	if !pathWithin(mediaRoot, target) {
		return nil, "", false
	}
	handle, err := os.Open(target)
	if err != nil {
		return nil, "", false
	}
	defer handle.Close()
	content, err := io.ReadAll(handle)
	if err != nil {
		return nil, "", false
	}
	return content, target, true
}
