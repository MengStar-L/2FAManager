package platform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"math/bits"
	"unicode/utf16"
)

// validateEncodedImage checks dimensions before an image decoder allocates a
// potentially unbounded pixel buffer. Registered PNG clipboard data may include
// unused bytes at the end of its GlobalAlloc allocation, which decoders permit.
func validateEncodedImage(data []byte) error {
	if len(data) == 0 || len(data) > maxClipboardBytes {
		return errors.New("剪贴板图片为空或超过 64 MB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("无法读取剪贴板图片: %w", err)
	}
	if format != "png" && format != "jpeg" {
		return errors.New("剪贴板图片格式不受支持")
	}
	return validateDimensions(int64(cfg.Width), int64(cfg.Height))
}

func validateDimensions(width, height int64) error {
	if width <= 0 || height <= 0 || width > maxImagePixels || height > maxImagePixels || width*height > maxImagePixels {
		return errors.New("剪贴板图片尺寸无效或超过 1600 万像素")
	}
	return nil
}

func decodeUnicodeText(data []byte) (string, error) {
	if len(data) > maxTextBytes || len(data)%2 != 0 {
		return "", errors.New("剪贴板文字大小或编码无效")
	}
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		u := binary.LittleEndian.Uint16(data[i : i+2])
		if u == 0 {
			return string(utf16.Decode(units)), nil
		}
		units = append(units, u)
	}
	return "", errors.New("剪贴板文字缺少结束标记")
}

// dibToPNG decodes packed Windows CF_DIB/CF_DIBV5 data. It accepts uncompressed
// 1/4/8-bit palettes, 16/24/32-bit RGB, and 16/32-bit bitfields. Clipboard DIBs
// have no BMP file header. Alpha is deliberately ignored: many screenshot
// producers leave the unused fourth channel zero, despite supplying RGB data.
func dibToPNG(data []byte) ([]byte, error) {
	if len(data) < 40 || len(data) > maxClipboardBytes {
		return nil, errors.New("剪贴板位图数据大小无效")
	}
	u32 := func(offset int) uint32 { return binary.LittleEndian.Uint32(data[offset : offset+4]) }
	u16 := func(offset int) uint16 { return binary.LittleEndian.Uint16(data[offset : offset+2]) }
	header := int64(u32(0))
	switch header {
	case 40, 52, 56, 108, 124:
	default:
		return nil, errors.New("剪贴板位图头不受支持")
	}
	if header > int64(len(data)) {
		return nil, errors.New("剪贴板位图头已截断")
	}
	width, signedHeight := int64(int32(u32(4))), int64(int32(u32(8)))
	height := signedHeight
	if height < 0 {
		height = -height
	}
	if err := validateDimensions(width, height); err != nil {
		return nil, err
	}
	if u16(12) != 1 {
		return nil, errors.New("剪贴板位图的颜色平面无效")
	}
	depth, compression := int(u16(14)), u32(16)
	switch depth {
	case 1, 4, 8, 16, 24, 32:
	default:
		return nil, errors.New("剪贴板位图色深不受支持")
	}
	if compression != 0 && compression != 3 && compression != 6 {
		return nil, errors.New("剪贴板压缩位图不受支持，请复制 PNG 图片")
	}
	if compression != 0 && depth != 16 && depth != 32 {
		return nil, errors.New("剪贴板位图颜色掩码与色深不匹配")
	}

	offset := header
	masks := [3]uint32{0x00ff0000, 0x0000ff00, 0x000000ff}
	if depth == 16 {
		masks = [3]uint32{0x7c00, 0x03e0, 0x001f}
	}
	if compression != 0 {
		maskOffset := int64(40)
		if header == 40 {
			maskOffset = offset
			offset += 12
			if compression == 6 {
				offset += 4
			}
		}
		if offset > int64(len(data)) || maskOffset+12 > header && header != 40 {
			return nil, errors.New("剪贴板位图颜色掩码已截断")
		}
		for i := range masks {
			masks[i] = u32(int(maskOffset) + i*4)
			if !validColorMask(masks[i], depth) {
				return nil, errors.New("剪贴板位图颜色掩码无效")
			}
		}
		if masks[0]&masks[1] != 0 || masks[0]&masks[2] != 0 || masks[1]&masks[2] != 0 {
			return nil, errors.New("剪贴板位图颜色掩码重叠")
		}
	}
	paletteCount := int64(u32(32))
	if depth <= 8 {
		if paletteCount == 0 {
			paletteCount = 1 << depth
		}
		if paletteCount > 1<<depth {
			return nil, errors.New("剪贴板位图调色板无效")
		}
	}
	paletteOffset := offset
	offset += paletteCount * 4
	stride := ((width*int64(depth) + 31) / 32) * 4
	end := offset + stride*height
	if offset > int64(len(data)) || end > int64(len(data)) {
		return nil, errors.New("剪贴板位图像素数据已截断")
	}
	if header == 124 {
		// Packed DIBV5 color profiles follow the pixel array. Never follow
		// linked profile paths; color management is unnecessary for QR codes.
		colorSpace := u32(56)
		if colorSpace == 0x4c494e4b || colorSpace == 0x4d424544 {
			profileOffset, profileSize := int64(u32(112)), int64(u32(116))
			if profileSize != 0 && (profileOffset < end || profileOffset+profileSize > int64(len(data))) {
				return nil, errors.New("剪贴板位图颜色配置范围无效")
			}
		}
	}

	img := image.NewNRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := int64(0); y < height; y++ {
		sourceY := y
		if signedHeight > 0 {
			sourceY = height - 1 - y
		}
		row := data[offset+sourceY*stride : offset+(sourceY+1)*stride]
		for x := int64(0); x < width; x++ {
			var r, g, b byte
			switch depth {
			case 1, 4, 8:
				var index uint32
				if depth == 1 {
					index = uint32((row[x/8] >> (7 - uint(x%8))) & 1)
				} else if depth == 4 {
					index = uint32((row[x/2] >> (4 - 4*uint(x%2))) & 15)
				} else {
					index = uint32(row[x])
				}
				if int64(index) >= paletteCount {
					return nil, errors.New("剪贴板位图包含无效的调色板索引")
				}
				p := paletteOffset + int64(index)*4
				b, g, r = data[p], data[p+1], data[p+2]
			case 16, 32:
				var value uint32
				if depth == 16 {
					value = uint32(binary.LittleEndian.Uint16(row[x*2 : x*2+2]))
				} else {
					value = binary.LittleEndian.Uint32(row[x*4 : x*4+4])
				}
				r, g, b = maskComponent(value, masks[0]), maskComponent(value, masks[1]), maskComponent(value, masks[2])
			case 24:
				p := x * 3
				b, g, r = row[p], row[p+1], row[p+2]
			}
			img.SetNRGBA(int(x), int(y), color.NRGBA{R: r, G: g, B: b, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		return nil, fmt.Errorf("剪贴板图片转换失败: %w", err)
	}
	return encoded.Bytes(), nil
}

func validColorMask(mask uint32, depth int) bool {
	if mask == 0 || depth < 32 && mask >= 1<<depth {
		return false
	}
	normalized := mask >> bits.TrailingZeros32(mask)
	return normalized&(normalized+1) == 0
}

func maskComponent(pixel, mask uint32) byte {
	shift := bits.TrailingZeros32(mask)
	maximum := uint64(mask >> shift)
	value := uint64((pixel & mask) >> shift)
	return byte((value*255 + maximum/2) / maximum)
}
