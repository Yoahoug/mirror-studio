package media

import (
	"mirror-studio/internal/imageutil"
)

// previewJPEGQuality matches the fallback renderer of the reference; the bytes
// are not part of the contract, only the dimensions and the mime type are.
const previewJPEGQuality = 82

// imageDimensions reports the pixel size of a stored object; an undecodable
// payload yields 0x0 exactly like the reference. The reference reads this from
// `Image.size` before any orientation handling, so no transposition is applied.
func imageDimensions(content []byte) (int, int) {
	return imageutil.DecodeConfig(content)
}

// renderPreview produces the thumbnail bytes and their mime type: PNG when the
// source carries transparency, otherwise a JPEG. The reference transposes EXIF
// orientation before resizing so the thumbnail matches the user's viewer.
func renderPreview(content []byte, maxDimension int) ([]byte, string, bool) {
	decoded, _, err := imageutil.Decode(content)
	if err != nil {
		return nil, "", false
	}
	scaled := imageutil.Resize(imageutil.ApplyEXIFOrientation(content, decoded), maxDimension)
	hasAlpha := imageutil.HasAlphaMode(content, decoded)
	var payload []byte
	if hasAlpha {
		payload, err = imageutil.EncodePNG(scaled, true)
	} else {
		payload, err = imageutil.EncodeJPEG(scaled, previewJPEGQuality)
	}
	if err != nil || len(payload) == 0 {
		return nil, "", false
	}
	if hasAlpha {
		return payload, "image/png", true
	}
	return payload, "image/jpeg", true
}

// sniffImageMIME detects the common formats from their magic bytes, then falls
// back to a full decode. It returns "" when the payload is not an image.
func sniffImageMIME(content []byte) string {
	return imageutil.SniffMIME(content)
}
