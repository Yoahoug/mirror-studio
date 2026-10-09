package novel

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "image/gif" // provider responses may carry GIF; decoded only for previews

	"mirror-studio/internal/httpapi"

	xdraw "golang.org/x/image/draw"
)

// onTheFlyPreviewDimension is the fixed 768 the reference view passes to its
// preview builder; the stored thumbnail uses IMAGE_PREVIEW_MAX_DIMENSION.
const onTheFlyPreviewDimension = 768

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
	rendered, _ := decodeImage(content)
	if rendered == nil {
		return "", "image/png"
	}
	resized := fitWithin(rendered, s.settings.previewMaxDimension)
	hasAlpha := sourceHasAlpha(content)
	renderMime := "image/jpeg"
	if hasAlpha {
		renderMime = "image/png"
	}
	payload, err := encodePreview(resized, hasAlpha)
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
	rendered, _ := decodeImage(content)
	if rendered == nil {
		return content, "image/png"
	}
	resized := fitWithin(rendered, onTheFlyPreviewDimension)
	if sourceHasAlpha(content) {
		payload, err := encodePreview(resized, true)
		if err != nil {
			return content, "image/png"
		}
		return payload, "image/png"
	}
	payload, err := encodePreview(resized, false)
	if err != nil {
		return content, "image/png"
	}
	return payload, "image/jpeg"
}

func decodeImage(content []byte) (image.Image, string) {
	decoded, format, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, ""
	}
	return decoded, format
}

func fitWithin(source image.Image, maxDimension int) image.Image {
	if maxDimension < 1 {
		maxDimension = 1
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	longest := width
	if height > longest {
		longest = height
	}
	if longest <= maxDimension {
		return source
	}
	scale := float64(maxDimension) / float64(longest)
	targetWidth := int(float64(width) * scale)
	targetHeight := int(float64(height) * scale)
	if targetWidth < 1 {
		targetWidth = 1
	}
	if targetHeight < 1 {
		targetHeight = 1
	}
	target := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	xdraw.CatmullRom.Scale(target, target.Bounds(), source, bounds, xdraw.Over, nil)
	return target
}

func encodePreview(source image.Image, hasAlpha bool) ([]byte, error) {
	buffer := &bytes.Buffer{}
	if hasAlpha {
		if err := png.Encode(buffer, source); err != nil {
			return nil, err
		}
		return buffer.Bytes(), nil
	}
	if err := jpeg.Encode(buffer, source, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// sourceHasAlpha reproduces Pillow's mode check for the PNG inputs providers
// return: RGBA/LA colour types, or a palette that carries transparency.
func sourceHasAlpha(content []byte) bool {
	if len(content) < 8 || !bytes.Equal(content[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return false
	}
	offset := 8
	for offset+8 <= len(content) {
		length := int(binary.BigEndian.Uint32(content[offset : offset+4]))
		chunkType := string(content[offset+4 : offset+8])
		if offset+12+length > len(content) {
			return false
		}
		switch chunkType {
		case "IHDR":
			if length >= 10 {
				switch content[offset+8+9] {
				case 4, 6:
					return true
				case 3:
					return hasTransparencyChunk(content)
				}
			}
			return false
		case "tRNS":
			return true
		}
		offset += 12 + length
	}
	return false
}

func hasTransparencyChunk(content []byte) bool {
	offset := 8
	for offset+8 <= len(content) {
		length := int(binary.BigEndian.Uint32(content[offset : offset+4]))
		chunkType := string(content[offset+4 : offset+8])
		if chunkType == "tRNS" {
			return true
		}
		if chunkType == "IDAT" {
			return false
		}
		if offset+12+length > len(content) {
			return false
		}
		offset += 12 + length
	}
	return false
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
