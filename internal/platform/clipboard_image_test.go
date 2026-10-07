package platform

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
	"unicode/utf16"
)

func makeDIB(header uint32, width, height int32, depth uint16, compression uint32, pixels []byte) []byte {
	data := make([]byte, int(header)+len(pixels))
	binary.LittleEndian.PutUint32(data[0:4], header)
	binary.LittleEndian.PutUint32(data[4:8], uint32(width))
	binary.LittleEndian.PutUint32(data[8:12], uint32(height))
	binary.LittleEndian.PutUint16(data[12:14], 1)
	binary.LittleEndian.PutUint16(data[14:16], depth)
	binary.LittleEndian.PutUint32(data[16:20], compression)
	copy(data[header:], pixels)
	return data
}

func decodedDIB(t *testing.T, data []byte) image.Image {
	t.Helper()
	encoded, err := dibToPNG(data)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func assertPixel(t *testing.T, img image.Image, x, y int, want color.NRGBA) {
	t.Helper()
	got := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
	if got != want {
		t.Errorf("pixel %d,%d = %v, want %v", x, y, got, want)
	}
}

func TestDIB24BitOrientationAndPaddedRows(t *testing.T) {
	top := []byte{0, 0, 255, 0, 255, 0, 255, 0, 0, 171, 171, 171}
	bottom := []byte{255, 255, 255, 0, 0, 0, 0, 255, 255, 171, 171, 171}
	for _, topDown := range []bool{false, true} {
		name, height := "bottom-up", int32(2)
		pixels := append(append([]byte{}, bottom...), top...)
		if topDown {
			name, height = "top-down", -2
			pixels = append(append([]byte{}, top...), bottom...)
		}
		t.Run(name, func(t *testing.T) {
			img := decodedDIB(t, makeDIB(40, 3, height, 24, 0, pixels))
			if img.Bounds() != image.Rect(0, 0, 3, 2) {
				t.Fatal(img.Bounds())
			}
			assertPixel(t, img, 0, 0, color.NRGBA{R: 255, A: 255})
			assertPixel(t, img, 1, 0, color.NRGBA{G: 255, A: 255})
			assertPixel(t, img, 2, 0, color.NRGBA{B: 255, A: 255})
			assertPixel(t, img, 0, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			assertPixel(t, img, 1, 1, color.NRGBA{A: 255})
			assertPixel(t, img, 2, 1, color.NRGBA{R: 255, G: 255, A: 255})
		})
	}
}

func TestDIB32BitScreenshotWithUnusedAlpha(t *testing.T) {
	img := decodedDIB(t, makeDIB(40, 2, -1, 32, 0, []byte{255, 255, 255, 0, 0, 0, 0, 0}))
	assertPixel(t, img, 0, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	assertPixel(t, img, 1, 0, color.NRGBA{A: 255})
}

func TestDIBV5Bitfields(t *testing.T) {
	data := makeDIB(124, 2, -1, 32, 3, []byte{12, 34, 56, 0, 255, 255, 255, 0})
	for i, mask := range []uint32{0xff0000, 0xff00, 0xff, 0xff000000} {
		binary.LittleEndian.PutUint32(data[40+i*4:44+i*4], mask)
	}
	img := decodedDIB(t, data)
	assertPixel(t, img, 0, 0, color.NRGBA{R: 56, G: 34, B: 12, A: 255})
	assertPixel(t, img, 1, 0, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
}

func TestDIB16BitExternalMasks(t *testing.T) {
	masksAndPixels := make([]byte, 16)
	for i, mask := range []uint32{0xf800, 0x7e0, 0x1f} {
		binary.LittleEndian.PutUint32(masksAndPixels[i*4:i*4+4], mask)
	}
	binary.LittleEndian.PutUint16(masksAndPixels[12:14], 0x07e0)
	binary.LittleEndian.PutUint16(masksAndPixels[14:16], 0xf800)
	img := decodedDIB(t, makeDIB(40, 2, 1, 16, 3, masksAndPixels))
	assertPixel(t, img, 0, 0, color.NRGBA{G: 255, A: 255})
	assertPixel(t, img, 1, 0, color.NRGBA{R: 255, A: 255})
}

func TestDIBPalettePackedPixels(t *testing.T) {
	for _, depth := range []uint16{1, 4, 8} {
		palette := make([]byte, 8)
		copy(palette[4:], []byte{255, 255, 255, 0})
		var pixels []byte
		switch depth {
		case 1:
			pixels = []byte{0xaa, 0x80, 0, 0}
		case 4:
			pixels = []byte{0x10, 0x10, 0x10, 0x10, 0x10, 0, 0, 0}
		case 8:
			pixels = []byte{1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 0, 0}
		}
		data := makeDIB(40, 9, 1, depth, 0, append(palette, pixels...))
		binary.LittleEndian.PutUint32(data[32:36], 2)
		img := decodedDIB(t, data)
		for x := 0; x < 9; x++ {
			v := byte(0)
			if x%2 == 0 {
				v = 255
			}
			assertPixel(t, img, x, 0, color.NRGBA{R: v, G: v, B: v, A: 255})
		}
	}
}

func TestDIBRejectsMalformedAndOversizedInput(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", nil},
		{"truncated header", make([]byte, 39)},
		{"unsupported header", makeDIB(41, 1, 1, 24, 0, make([]byte, 4))},
		{"negative width", makeDIB(40, -1, 1, 24, 0, nil)},
		{"zero height", makeDIB(40, 1, 0, 24, 0, nil)},
		{"min int height", makeDIB(40, 1, -2147483648, 24, 0, nil)},
		{"pixel limit", makeDIB(40, 8192, 8192, 24, 0, nil)},
		{"huge dimensions", makeDIB(40, 2147483647, 2147483647, 32, 0, nil)},
		{"truncated pixels", makeDIB(40, 2, 2, 24, 0, make([]byte, 12))},
		{"truncated masks", makeDIB(40, 1, 1, 16, 3, make([]byte, 4))},
		{"unsupported compression", makeDIB(40, 1, 1, 8, 1, make([]byte, 4))},
		{"unsupported depth", makeDIB(40, 1, 1, 2, 0, make([]byte, 4))},
		{"mismatched bitfields", makeDIB(40, 1, 1, 24, 3, make([]byte, 4))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := dibToPNG(tc.data); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	for _, mutate := range []func([]byte){
		func(data []byte) { binary.LittleEndian.PutUint32(data[0:4], 124) },
		func(data []byte) { binary.LittleEndian.PutUint16(data[12:14], 2) },
		func(data []byte) { binary.LittleEndian.PutUint32(data[32:36], 0xffffffff) },
	} {
		data := makeDIB(40, 1, 1, 24, 0, make([]byte, 4))
		mutate(data)
		if _, err := dibToPNG(data); err == nil {
			t.Fatal("invalid header accepted")
		}
	}
}

func TestDIBRejectsInvalidColorMasksAndProfiles(t *testing.T) {
	for _, masks := range [][3]uint32{{0, 0xff00, 0xff}, {0xff00, 0xff00, 0xff}, {0xf000f, 0xff00, 0xff}} {
		data := makeDIB(124, 1, 1, 32, 3, make([]byte, 4))
		for i, mask := range masks {
			binary.LittleEndian.PutUint32(data[40+i*4:44+i*4], mask)
		}
		if _, err := dibToPNG(data); err == nil {
			t.Fatal("invalid masks accepted", masks)
		}
	}
	data := makeDIB(124, 1, 1, 32, 0, make([]byte, 8))
	binary.LittleEndian.PutUint32(data[56:60], 0x4d424544)
	binary.LittleEndian.PutUint32(data[112:116], 124)
	binary.LittleEndian.PutUint32(data[116:120], 4)
	if _, err := dibToPNG(data); err == nil {
		t.Fatal("overlapping color profile accepted")
	}
	binary.LittleEndian.PutUint32(data[112:116], 128)
	decodedDIB(t, data)
}

func TestClipboardUnicodeText(t *testing.T) {
	want := "otpauth://totp/测试:用户😀?secret=JBSWY3DPEHPK3PXP"
	units := append(utf16.Encode([]rune(want)), 0, 'x')
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:i*2+2], unit)
	}
	got, err := decodeUnicodeText(data)
	if err != nil || got != want {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, invalid := range [][]byte{nil, {1}, {65, 0}, make([]byte, maxTextBytes+2)} {
		if _, err := decodeUnicodeText(invalid); err == nil {
			t.Fatal("invalid Unicode text accepted")
		}
	}
}

func TestEncodedImageValidation(t *testing.T) {
	var valid bytes.Buffer
	if err := png.Encode(&valid, image.NewGray(image.Rect(0, 0, 3, 3))); err != nil {
		t.Fatal(err)
	}
	if err := validateEncodedImage(valid.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := validateEncodedImage([]byte("not a PNG")); err == nil {
		t.Fatal("invalid image accepted")
	}
	if err := validateDimensions(1<<24, 2); err == nil {
		t.Fatal("excessive dimensions accepted")
	}
}

func FuzzDIBToPNG(f *testing.F) {
	f.Add(makeDIB(40, 1, -1, 32, 0, []byte{255, 255, 255, 0}))
	f.Add(makeDIB(40, 3, 2, 24, 0, make([]byte, 24)))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		encoded, err := dibToPNG(data)
		if err == nil {
			if err := validateEncodedImage(encoded); err != nil {
				t.Fatalf("successful conversion returned invalid image: %v", err)
			}
		}
	})
}
