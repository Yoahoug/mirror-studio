package imageutil

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"testing"
)

// jpegWithOrientation splices an EXIF APP1 segment carrying `orientation` into
// an otherwise ordinary JPEG, which is how a phone camera reports the tag.
func jpegWithOrientation(t *testing.T, orientation int) []byte {
	t.Helper()
	base := solidJPEG(t)
	if len(base) < 2 || base[0] != 0xff || base[1] != 0xd8 {
		t.Fatalf("not a jpeg: % x", base[:2])
	}

	tiff := &bytes.Buffer{}
	tiff.WriteString("MM")                     // big endian
	tiff.Write([]byte{0x00, 0x2a})             // magic 42
	tiff.Write([]byte{0x00, 0x00, 0x00, 0x08}) // offset to IFD0
	tiff.Write([]byte{0x00, 0x01})             // one entry
	tiff.Write([]byte{0x01, 0x12})             // tag: orientation
	tiff.Write([]byte{0x00, 0x03})             // type: SHORT
	tiff.Write([]byte{0x00, 0x00, 0x00, 0x01}) // count: 1
	tiff.Write([]byte{byte(orientation >> 8), byte(orientation), 0x00, 0x00})
	tiff.Write([]byte{0x00, 0x00, 0x00, 0x00}) // no next IFD

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	segment := &bytes.Buffer{}
	segment.Write([]byte{0xff, 0xe1})
	_ = binary.Write(segment, binary.BigEndian, uint16(len(payload)+2))
	segment.Write(payload)

	out := &bytes.Buffer{}
	out.Write(base[:2])
	out.Write(segment.Bytes())
	out.Write(base[2:])
	return out.Bytes()
}

// solidJPEG encodes a 2x1 image, small enough to keep the payload tiny while
// still exercising the segment walk.
func solidJPEG(t *testing.T) []byte {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, 2, 1))
	source.Set(0, 0, color.RGBA{R: 255, A: 255})
	source.Set(1, 0, color.RGBA{B: 255, A: 255})
	encoded, err := EncodeJPEG(source, 90)
	if err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return encoded
}

func TestApplyEXIFOrientation(t *testing.T) {
	content := jpegWithOrientation(t, 6)
	decoded, _, err := Decode(content)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	width, height := decoded.Bounds().Dx(), decoded.Bounds().Dy()
	if width != 2 || height != 1 {
		t.Fatalf("decoded size = %dx%d, want 2x1", width, height)
	}
	// EXIF orientation 6 is a 90-degree clockwise rotation, so the axes swap.
	rotated := ApplyEXIFOrientation(content, decoded)
	if got := rotated.Bounds().Dx(); got != 1 {
		t.Fatalf("rotated width = %d, want 1", got)
	}
	if got := rotated.Bounds().Dy(); got != 2 {
		t.Fatalf("rotated height = %d, want 2", got)
	}
}

func TestApplyEXIFOrientationLeavesUntaggedImagesAlone(t *testing.T) {
	content := solidJPEG(t)
	decoded, _, err := Decode(content)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := ApplyEXIFOrientation(content, decoded); got != image.Image(decoded) {
		t.Fatal("an image without an orientation tag must be returned unchanged")
	}
}

// pngChunk assembles one PNG chunk with its CRC.
func pngChunk(kind string, payload []byte) []byte {
	chunk := &bytes.Buffer{}
	_ = binary.Write(chunk, binary.BigEndian, uint32(len(payload)))
	chunk.WriteString(kind)
	chunk.Write(payload)
	_ = binary.Write(chunk, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), payload...)))
	return chunk.Bytes()
}

// truecolourPNGWithTransparency builds a colour type 2 (truecolour) PNG with a
// tRNS chunk. Pillow decodes this to mode "RGB" while still reporting a
// transparency entry — the one input where the provider and preview alpha tests
// disagree.
func truecolourPNGWithTransparency(t *testing.T) []byte {
	t.Helper()
	ihdr := &bytes.Buffer{}
	_ = binary.Write(ihdr, binary.BigEndian, uint32(2)) // width
	_ = binary.Write(ihdr, binary.BigEndian, uint32(1)) // height
	ihdr.Write([]byte{8, 2, 0, 0, 0})                   // depth 8, colour type 2, no interlace

	raw := &bytes.Buffer{}
	raw.WriteByte(0) // filter: none
	raw.Write([]byte{255, 0, 0, 0, 0, 255})
	compressed := &bytes.Buffer{}
	writer := zlib.NewWriter(compressed)
	if _, err := writer.Write(raw.Bytes()); err != nil {
		t.Fatalf("compress: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("compress: %v", err)
	}

	out := &bytes.Buffer{}
	out.Write(pngSignature)
	out.Write(pngChunk("IHDR", ihdr.Bytes()))
	// Truecolour tRNS holds three 16-bit values per the PNG spec.
	out.Write(pngChunk("tRNS", []byte{0x00, 0x00, 0xff, 0xff, 0x00, 0x00})) // green is transparent
	out.Write(pngChunk("IDAT", compressed.Bytes()))
	out.Write(pngChunk("IEND", nil))
	return out.Bytes()
}

func TestAlphaPredicatesDifferOnTruecolourTransparency(t *testing.T) {
	content := truecolourPNGWithTransparency(t)
	decoded, _, err := Decode(content)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := decoded.Bounds().Dx(); got != 2 {
		t.Fatalf("width = %d, want 2", got)
	}
	if !HasAlphaChannel(content, decoded) {
		t.Fatal("the provider predicate counts a tRNS chunk as transparency")
	}
	if HasAlphaMode(content, decoded) {
		t.Fatal("the preview predicate follows the decoded mode, which is RGB here")
	}
}

func TestAlphaPredicatesAgreeOnRGBA(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	source.Set(0, 0, color.NRGBA{R: 255, A: 255})
	content, err := EncodePNG(source, false)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, _, err := Decode(content)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !HasAlphaChannel(content, decoded) || !HasAlphaMode(content, decoded) {
		t.Fatal("an RGBA png counts as transparent for both predicates")
	}
	// An opaque NRGBA source is still colour type 6: Pillow reports mode RGBA,
	// and neither predicate looks at the pixels.
	if !HasAlphaChannel(content, decoded) {
		t.Fatal("colour type 6 counts as having alpha even when fully opaque")
	}
}

func TestResizeFitsLongestEdge(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 400, 200))
	resized := Resize(source, 100)
	if got := resized.Bounds().Dx(); got != 100 {
		t.Fatalf("width = %d, want 100", got)
	}
	if got := resized.Bounds().Dy(); got != 50 {
		t.Fatalf("height = %d, want 50", got)
	}
	if untouched := Resize(source, 1000); untouched != image.Image(source) {
		t.Fatal("an image that already fits must be returned unchanged")
	}
}
