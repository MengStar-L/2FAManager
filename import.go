package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"LumaAuthenticator/internal/vault"
	"github.com/makiuchi-d/gozxing"
	multiqr "github.com/makiuchi-d/gozxing/multi/qrcode"
	"github.com/makiuchi-d/gozxing/qrcode"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

const maxQRBytes = 20 << 20

type ImportPreview struct {
	Issuer  string `json:"issuer"`
	Account string `json:"account"`
	URI     string `json:"uri"`
}

func (a *App) PreviewText(text string) ([]ImportPreview, error) {
	if len(text) > 256<<10 {
		return nil, errors.New("链接内容过长")
	}
	lines := strings.Fields(text)
	if len(lines) == 0 || len(lines) > 100 {
		return nil, errors.New("请输入 1–100 条 otpauth 令牌链接，每行一条")
	}
	result := make([]ImportPreview, 0, len(lines))
	for i, line := range lines {
		input, err := vault.PreviewURI(line)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条链接：%w", i+1, err)
		}
		result = append(result, ImportPreview{Issuer: input.Issuer, Account: input.Account, URI: line})
	}
	return result, nil
}

func (a *App) PreviewImage(encoded string) ([]ImportPreview, error) {
	if len(encoded) > (maxQRBytes*4/3)+1024 {
		return nil, errors.New("二维码图片不能超过 20 MB")
	}
	if strings.HasPrefix(encoded, "data:") {
		var ok bool
		_, encoded, ok = strings.Cut(encoded, ",")
		if !ok {
			return nil, errors.New("图片格式无效")
		}
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("无法读取图片数据")
	}
	return previewImageBytes(data)
}

func previewImageBytes(data []byte) ([]ImportPreview, error) {
	if len(data) == 0 || len(data) > maxQRBytes {
		return nil, errors.New("二维码图片为空或超过 20 MB")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("无法识别图片，请使用 PNG、JPEG、WebP 或 BMP")
	}
	if cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 25_000_000 {
		return nil, errors.New("图片分辨率过大，请裁剪到二维码区域")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("图片数据损坏，无法读取")
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return nil, errors.New("无法读取二维码图像")
	}
	hints := map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true}
	results, err := multiqr.NewQRCodeMultiReader().DecodeMultiple(bitmap, hints)
	if err != nil || len(results) == 0 {
		one, oneErr := qrcode.NewQRCodeReader().Decode(bitmap, hints)
		if oneErr != nil {
			return nil, errors.New("没有识别到二维码，请复制清晰、完整的二维码截图")
		}
		results = []*gozxing.Result{one}
	}
	previews := make([]ImportPreview, 0, len(results))
	seen := make(map[string]bool)
	var tokenErr error
	for _, result := range results {
		uri := strings.TrimSpace(result.GetText())
		if !strings.HasPrefix(strings.ToLower(uri), "otpauth") {
			continue
		}
		input, parseErr := vault.PreviewURI(uri)
		if parseErr != nil {
			tokenErr = parseErr
			continue
		}
		if !seen[uri] {
			previews = append(previews, ImportPreview{Issuer: input.Issuer, Account: input.Account, URI: uri})
			seen[uri] = true
		}
	}
	if tokenErr != nil {
		return nil, tokenErr
	}
	if len(previews) == 0 {
		return nil, errors.New("识别到的二维码不是 TOTP 令牌二维码")
	}
	return previews, nil
}
