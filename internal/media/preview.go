package media

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"math"

	_ "image/gif"  // register the GIF decoder
	_ "image/jpeg" // register the JPEG decoder
	_ "image/png"  // register the PNG decoder

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register the WebP decoder
)

// previewJPEGQuality matches the fallback renderer of the reference; the bytes
// are not part of the contract, only the dimensions and the mime type are.
const previewJPEGQuality = 82

// decodeImage reads an image using the decoders registered above.
func decodeImage(content []byte) (image.Image, string, error) {
	decoded, format, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, "", err
	}
	return decoded, format, nil
}

// imageDimensions reports the pixel size of a stored object; an undecodable
// payload yields 0x0 exactly like the reference.
func imageDimensions(content []byte) (int, int) {
	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return 0, 0
	}
	return config.Width, config.Height
}

// imageHasAlpha decides between the PNG and JPEG preview encodings. The PNG
// colour type is read from the header because Go's decoder widens opaque RGB
// to RGBA; paletted images (GIF, indexed PNG with tRNS) are inspected through
// their palette, which the decoders have already populated with alpha values.
func imageHasAlpha(content []byte, decoded image.Image) bool {
	if isPNG(content) {
		if len(content) < 26 {
			return false
		}
		switch content[25] {
		case 4, 6:
			return true
		case 3:
			return paletteHasAlpha(decoded)
		default:
			return false
		}
	}
	if isJPEG(content) {
		return false
	}
	if palette, ok := decoded.(*image.Paletted); ok {
		return paletteHasAlpha(palette)
	}
	return scanHasAlpha(decoded)
}

func isPNG(content []byte) bool {
	return len(content) >= 8 && bytes.Equal(content[:8], []byte("\x89PNG\r\n\x1a\n"))
}

func isJPEG(content []byte) bool {
	return len(content) >= 3 && content[0] == 0xff && content[1] == 0xd8 && content[2] == 0xff
}

func paletteHasAlpha(decoded image.Image) bool {
	paletted, ok := decoded.(*image.Paletted)
	if !ok {
		return false
	}
	for _, entry := range paletted.Palette {
		if _, _, _, alpha := entry.RGBA(); alpha < 0xffff {
			return true
		}
	}
	return false
}

func scanHasAlpha(decoded image.Image) bool {
	bounds := decoded.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, alpha := decoded.At(x, y).RGBA(); alpha < 0xffff {
				return true
			}
		}
	}
	return false
}

// resizePreview scales an image down to fit maxDimension on its longest edge.
// It is the single place the resampler is chosen, so it can be swapped without
// touching the encoders. The target size reproduces PIL's thumbnail geometry.
func resizePreview(source image.Image, maxDimension int) image.Image {
	width := source.Bounds().Dx()
	height := source.Bounds().Dy()
	longest := width
	if height > longest {
		longest = height
	}
	if longest <= maxDimension || longest == 0 {
		return source
	}
	ratio := math.Min(float64(maxDimension)/float64(width), float64(maxDimension)/float64(height))
	targetWidth := int(math.Round(float64(width) * ratio))
	targetHeight := int(math.Round(float64(height) * ratio))
	if targetWidth < 1 {
		targetWidth = 1
	}
	if targetHeight < 1 {
		targetHeight = 1
	}
	destination := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	draw.CatmullRom.Scale(destination, destination.Bounds(), source, source.Bounds(), draw.Src, nil)
	return destination
}

// renderPreview produces the thumbnail bytes and their mime type: PNG when the
// source carries transparency, otherwise a JPEG.
func renderPreview(content []byte, maxDimension int) ([]byte, string, bool) {
	decoded, _, err := decodeImage(content)
	if err != nil {
		return nil, "", false
	}
	scaled := resizePreview(decoded, maxDimension)
	hasAlpha := imageHasAlpha(content, decoded)
	buffer := &bytes.Buffer{}
	if hasAlpha {
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		if err := encoder.Encode(buffer, scaled); err != nil {
			return nil, "", false
		}
	} else {
		if err := jpeg.Encode(buffer, scaled, &jpeg.Options{Quality: previewJPEGQuality}); err != nil {
			return nil, "", false
		}
	}
	if buffer.Len() == 0 {
		return nil, "", false
	}
	if hasAlpha {
		return buffer.Bytes(), "image/png", true
	}
	return buffer.Bytes(), "image/jpeg", true
}

// sniffImageMIME detects the common formats from their magic bytes, then falls
// back to a full decode. It returns "" when the payload is not an image.
func sniffImageMIME(content []byte) string {
	if len(content) == 0 {
		return ""
	}
	head := content
	if len(head) > 16 {
		head = head[:16]
	}
	if isPNG(content) {
		return "image/png"
	}
	if isJPEG(content) {
		return "image/jpeg"
	}
	if len(head) >= 12 && bytes.Equal(head[0:4], []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")) {
		return "image/webp"
	}
	_, format, err := decodeImage(content)
	if err != nil {
		return ""
	}
	switch format {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	}
	return ""
}
