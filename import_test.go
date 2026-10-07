package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

const testURI = "otpauth://totp/Example:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example"

func qrImage(t *testing.T, text string) image.Image {
	t.Helper()
	qr, err := qrcode.NewQRCodeWriter().EncodeWithoutHint(text, gozxing.BarcodeFormat_QR_CODE, 320, 320)
	if err != nil {
		t.Fatal(err)
	}
	return qr
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPreviewQRAndScreenshot(t *testing.T) {
	qr := qrImage(t, testURI)
	shot := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	draw.Draw(shot, shot.Bounds(), image.NewUniform(color.RGBA{245, 244, 249, 255}), image.Point{}, draw.Src)
	draw.Draw(shot, image.Rect(750, 180, 1070, 500), qr, image.Point{}, draw.Src)
	for name, img := range map[string]image.Image{"qr": qr, "screenshot": shot} {
		t.Run(name, func(t *testing.T) {
			got, err := NewApp().PreviewImage("data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes(t, img)))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Account != "alice@example.com" || got[0].Issuer != "Example" {
				t.Fatalf("unexpected preview: count=%d", len(got))
			}
		})
	}
}

func TestPreviewRejectsInvalidInputs(t *testing.T) {
	a := NewApp()
	for _, text := range []string{"", "https://example.com", strings.Replace(testURI, "totp", "hotp", 1), "otpauth-migration://offline?data=xyz", testURI + "\ninvalid"} {
		if _, err := a.PreviewText(text); err == nil {
			t.Fatal("invalid text accepted")
		}
	}
	if _, err := a.PreviewImage("not an image"); err == nil {
		t.Fatal("invalid base64 accepted")
	}
	if _, err := previewImageBytes(pngBytes(t, qrImage(t, "https://example.com"))); err == nil {
		t.Fatal("non-token QR accepted")
	}
	if _, err := previewImageBytes(pngBytes(t, image.NewRGBA(image.Rect(0, 0, 100, 100)))); err == nil {
		t.Fatal("blank image accepted")
	}
}

func TestMultipleQRCodePreview(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 800, 400))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(10, 10, 330, 330), qrImage(t, testURI), image.Point{}, draw.Src)
	second := strings.Replace(testURI, "alice%40example.com", "bob%40example.com", 1)
	draw.Draw(img, image.Rect(460, 10, 780, 330), qrImage(t, second), image.Point{}, draw.Src)
	previews, err := previewImageBytes(pngBytes(t, img))
	if err != nil {
		t.Fatal(err)
	}
	if len(previews) != 2 {
		t.Fatalf("expected 2 codes, got %d", len(previews))
	}
}
