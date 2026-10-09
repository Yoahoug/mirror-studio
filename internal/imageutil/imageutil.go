// Package imageutil holds the image decode, orientation, resize and encode
// helpers shared by the generator, mediahub and novelviz pipelines. It is a
// leaf package: it depends only on the standard library and golang.org/x/image,
// holds no domain state and never touches the database or the network, so every
// domain can import it without introducing a cycle.
//
// The reference implementation routes all three pipelines through Pillow. Byte
// equality of derived images is not part of the contract (see
// docs/api-contract-0.0.1.md) — Pillow encodes progressive, Huffman-optimised
// JPEG and uses Lanczos resampling, none of which the Go standard library
// offers. What this package guarantees instead is the contract the reference's
// callers actually rely on: the same decoded pixels up to the resampling filter,
// the same orientation, the same output format decision, and a longest edge that
// never exceeds the requested limit.
package imageutil

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"
	"math"

	_ "image/gif"  // uploads may carry GIF; the reference accepts it
	_ "image/jpeg" // register the JPEG decoder
	_ "image/png"  // register the PNG decoder

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register the WebP decoder
)

var (
	pngSignature = []byte("\x89PNG\r\n\x1a\n")
	exifHeader   = []byte("Exif\x00\x00")
)

// Decode reads an image. The registered decoders cover the formats the upload
// paths accept: PNG, JPEG, GIF and WebP.
func Decode(content []byte) (image.Image, string, error) {
	return image.Decode(bytes.NewReader(content))
}

// DecodeConfig reports the pixel dimensions without decoding the pixels, which
// is what the reference's `_image_dimensions` reads off `Image.size` before any
// orientation handling. Undecodable payloads report 0x0.
func DecodeConfig(content []byte) (int, int) {
	config, _, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil {
		return 0, 0
	}
	return config.Width, config.Height
}

// ApplyEXIFOrientation mirrors `ImageOps.exif_transpose`: a JPEG carrying an
// EXIF orientation tag is transposed so its pixels match the way the user's own
// viewer shows the photo. Anything else, including a JPEG without the tag, is
// returned unchanged.
func ApplyEXIFOrientation(content []byte, source image.Image) image.Image {
	orientation := exifOrientation(content)
	if orientation < 2 || orientation > 8 {
		return source
	}
	return transpose(source, orientation)
}

// Resize scales an image down so its longest edge fits maxDimension, preserving
// aspect ratio, and returns the source untouched when it already fits. The
// Catmull-Rom cubic filter is the closest available equivalent to Pillow's
// Lanczos resampler.
func Resize(source image.Image, maxDimension int) image.Image {
	if maxDimension < 1 {
		return source
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	longest := width
	if height > longest {
		longest = height
	}
	if longest == 0 || longest <= maxDimension {
		return source
	}
	scale := float64(maxDimension) / float64(longest)
	targetWidth := atLeastOne(int(math.Round(float64(width) * scale)))
	targetHeight := atLeastOne(int(math.Round(float64(height) * scale)))
	target := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	draw.CatmullRom.Scale(target, target.Bounds(), source, bounds, draw.Src, nil)
	return target
}

// EncodePNG writes a PNG. `best` selects Pillow's `optimize=True` equivalent,
// which trades encode time for a smaller payload.
func EncodePNG(source image.Image, best bool) ([]byte, error) {
	buffer := &bytes.Buffer{}
	level := png.DefaultCompression
	if best {
		level = png.BestCompression
	}
	if err := (&png.Encoder{CompressionLevel: level}).Encode(buffer, source); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// EncodeJPEG writes a baseline JPEG. The reference writes progressive JPEGs
// with optimised Huffman tables; the standard library cannot, so the payload is
// larger for the same quality. Both are valid JPEG at the same dimensions.
func EncodeJPEG(source image.Image, quality int) ([]byte, error) {
	buffer := &bytes.Buffer{}
	if err := jpeg.Encode(buffer, source, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// HasAlphaChannel answers Pillow's `"A" in image.getbands() or "transparency" in
// image.info`, the test the reference's provider layer uses: whether the encoded
// image carries an alpha channel or a transparency tag. It is a property of the
// file format, not of the pixels — a fully opaque RGBA PNG still counts as
// having alpha, and Go's PNG decoder hands back an RGBA-family type for plain
// RGB images too, so the decoded type alone cannot answer this.
func HasAlphaChannel(content []byte, decoded image.Image) bool {
	return hasAlpha(content, decoded, true)
}

// HasAlphaMode answers the narrower test the reference's preview builders use —
// `mode in {"RGBA", "LA"} or (mode == "P" and "transparency" in info)` — which
// is about the decoded mode rather than the presence of a transparency tag. The
// two predicates differ for one input class: a truecolour PNG (colour type 0 or
// 2) with a tRNS chunk decodes to mode L/RGB in Pillow, so previews re-encode it
// as JPEG while the provider path treats it as transparent.
func HasAlphaMode(content []byte, decoded image.Image) bool {
	return hasAlpha(content, decoded, false)
}

func hasAlpha(content []byte, decoded image.Image, transparencyTagCounts bool) bool {
	if isPNG(content) {
		// IHDR: signature(8) length(4) type(4) width(4) height(4) depth(1) colourType(1)
		const colorTypeOffset = 8 + 4 + 4 + 4 + 4 + 1
		if len(content) <= colorTypeOffset {
			return false
		}
		switch content[colorTypeOffset] {
		case 4, 6:
			return true
		case 3:
			// An indexed PNG is only transparent when it has a tRNS chunk.
			return bytes.Contains(content, []byte("tRNS"))
		case 0, 2:
			return transparencyTagCounts && bytes.Contains(content, []byte("tRNS"))
		}
		return false
	}
	if isJPEG(content) {
		return false
	}
	// GIF and WebP carry alpha in their palette or pixels rather than in a
	// header field Pillow names, so inspect what the decoder produced.
	if palette, ok := decoded.(*image.Paletted); ok {
		for _, entry := range palette.Palette {
			if _, _, _, alpha := entry.RGBA(); alpha < 0xffff {
				return true
			}
		}
		return false
	}
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

// SniffMIME detects the accepted formats from their magic bytes, falling back to
// a full decode for anything without a distinctive header. It returns "" when
// the payload is not an image.
func SniffMIME(content []byte) string {
	if len(content) == 0 {
		return ""
	}
	if isPNG(content) {
		return "image/png"
	}
	if isJPEG(content) {
		return "image/jpeg"
	}
	if len(content) >= 12 && bytes.Equal(content[:4], []byte("RIFF")) && bytes.Equal(content[8:12], []byte("WEBP")) {
		return "image/webp"
	}
	_, format, err := Decode(content)
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

func isPNG(content []byte) bool {
	return len(content) >= len(pngSignature) && bytes.Equal(content[:len(pngSignature)], pngSignature)
}

func isJPEG(content []byte) bool {
	return len(content) >= 3 && content[0] == 0xff && content[1] == 0xd8 && content[2] == 0xff
}

func atLeastOne(value int) int {
	if value < 1 {
		return 1
	}
	return value
}

// ── EXIF ──

// exifOrientation reads tag 0x0112 from IFD0 of a JPEG's EXIF block. It returns
// 0 when there is no usable tag, which callers treat as "no transposition".
func exifOrientation(content []byte) int {
	tiff := exifTIFFBlock(content)
	if tiff == nil {
		return 0
	}
	var byteOrder binary.ByteOrder = binary.BigEndian
	if len(tiff) >= 2 && tiff[0] == 'I' && tiff[1] == 'I' {
		byteOrder = binary.LittleEndian
	} else if len(tiff) < 2 || tiff[0] != 'M' || tiff[1] != 'M' {
		return 0
	}
	if len(tiff) < 8 || byteOrder.Uint16(tiff[2:4]) != 42 {
		return 0
	}
	offset := int(byteOrder.Uint32(tiff[4:8]))
	if offset < 8 || offset+2 > len(tiff) {
		return 0
	}
	count := int(byteOrder.Uint16(tiff[offset : offset+2]))
	entry := offset + 2
	for index := 0; index < count; index++ {
		if entry+12 > len(tiff) {
			return 0
		}
		tag := byteOrder.Uint16(tiff[entry : entry+2])
		if tag == 0x0112 {
			value := byteOrder.Uint16(tiff[entry+8 : entry+10])
			return int(value)
		}
		entry += 12
	}
	return 0
}

// exifTIFFBlock walks the JPEG segment chain to the APP1 segment carrying the
// EXIF header and returns the TIFF block that follows it.
func exifTIFFBlock(content []byte) []byte {
	if !isJPEG(content) {
		return nil
	}
	offset := 2
	for offset+4 <= len(content) {
		if content[offset] != 0xff {
			return nil
		}
		marker := content[offset+1]
		// Standalone markers carry no length field.
		if marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			offset += 2
			continue
		}
		if marker == 0xda || marker == 0xd9 {
			// Start of scan: image data, no further metadata segments.
			return nil
		}
		length := int(binary.BigEndian.Uint16(content[offset+2 : offset+4]))
		if length < 2 || offset+2+length > len(content) {
			return nil
		}
		if marker == 0xe1 {
			payload := content[offset+4 : offset+2+length]
			if bytes.HasPrefix(payload, exifHeader) {
				return payload[len(exifHeader):]
			}
		}
		offset += 2 + length
	}
	return nil
}

// transpose applies the EXIF orientation transform. The numbering and the
// operations match Pillow's `ImageOps.exif_transpose`, including its convention
// that ROTATE_90 is 90 degrees counter-clockwise.
func transpose(source image.Image, orientation int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	targetWidth, targetHeight := width, height
	if orientation >= 5 {
		targetWidth, targetHeight = height, width
	}
	target := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var targetX, targetY int
			switch orientation {
			case 2: // flip left-right
				targetX, targetY = width-1-x, y
			case 3: // rotate 180
				targetX, targetY = width-1-x, height-1-y
			case 4: // flip top-bottom
				targetX, targetY = x, height-1-y
			case 5: // transpose across the top-left/bottom-right diagonal
				targetX, targetY = y, x
			case 6: // rotate 90 clockwise
				targetX, targetY = height-1-y, x
			case 7: // transverse across the top-right/bottom-left diagonal
				targetX, targetY = height-1-y, width-1-x
			case 8: // rotate 90 counter-clockwise
				targetX, targetY = y, width-1-x
			default:
				return source
			}
			target.Set(targetX, targetY, source.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return target
}
