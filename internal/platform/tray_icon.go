package platform

import (
	"encoding/binary"
	"errors"
)

// iconResource extracts one bounded ICO image for CreateIconFromResourceEx.
func iconResource(data []byte, target int) ([]byte, error) {
	if len(data) < 6 || len(data) > 4<<20 || binary.LittleEndian.Uint16(data[:2]) != 0 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		return nil, errors.New("应用图标格式无效")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 || count > 256 || 6+count*16 > len(data) {
		return nil, errors.New("应用图标目录无效")
	}
	best, score := []byte(nil), int(^uint(0)>>1)
	for i := 0; i < count; i++ {
		entry := data[6+i*16 : 6+(i+1)*16]
		width := int(entry[0])
		if width == 0 {
			width = 256
		}
		height := int(entry[1])
		if height == 0 {
			height = 256
		}
		size, offset := uint64(binary.LittleEndian.Uint32(entry[8:12])), uint64(binary.LittleEndian.Uint32(entry[12:16]))
		if size == 0 || offset < uint64(6+count*16) || offset+size > uint64(len(data)) {
			return nil, errors.New("应用图标数据范围无效")
		}
		distance := absInt(width-target) + absInt(height-target)
		if width < target || height < target {
			distance += 1000
		}
		if distance < score {
			best, score = data[offset:offset+size], distance
		}
	}
	return best, nil
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
