//go:build windows

package platform

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfDIB         = 8
	cfUnicodeText = 13
	cfDIBV5       = 17
)

var (
	user32                     = windows.NewLazySystemDLL("user32.dll")
	kernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	openClipboard              = user32.NewProc("OpenClipboard")
	closeClipboard             = user32.NewProc("CloseClipboard")
	getClipboardData           = user32.NewProc("GetClipboardData")
	isClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	registerClipboardFormat    = user32.NewProc("RegisterClipboardFormatW")
	globalSize                 = kernel32.NewProc("GlobalSize")
	globalLock                 = kernel32.NewProc("GlobalLock")
	globalUnlock               = kernel32.NewProc("GlobalUnlock")
)

type clipboardImage struct {
	data []byte
	dib  bool
}

// ReadClipboard takes a native snapshot. It does not clear or modify the user's
// clipboard. PNG takes priority, followed by DIBV5 and DIB screenshot formats.
func ReadClipboard() (ClipboardData, error) {
	result, candidates, snapshotErr := clipboardSnapshot()
	var decodeErr error
	for _, candidate := range candidates {
		if candidate.dib {
			result.Image, decodeErr = dibToPNG(candidate.data)
		} else {
			decodeErr = validateEncodedImage(candidate.data)
			if decodeErr == nil {
				result.Image = candidate.data
			}
		}
		if decodeErr == nil {
			break
		}
	}
	if len(result.Image) > 0 || result.Text != "" {
		return result, nil
	}
	if decodeErr != nil {
		return ClipboardData{}, decodeErr
	}
	if snapshotErr != nil {
		return ClipboardData{}, snapshotErr
	}
	return ClipboardData{}, ErrClipboardEmpty
}

func clipboardSnapshot() (ClipboardData, []clipboardImage, error) {
	// Win32 clipboard ownership and closing are thread-affine. Keep the Go
	// goroutine on one OS thread until all native handles are released.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var openErr error
	opened := false
	for attempt := 0; attempt < 8; attempt++ {
		var ok uintptr
		ok, _, openErr = openClipboard.Call(0)
		if ok != 0 {
			opened = true
			break
		}
		if attempt < 7 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !opened {
		return ClipboardData{}, nil, winError("剪贴板正在被其他程序使用，请稍后重试", openErr)
	}
	defer closeClipboard.Call()

	result := ClipboardData{}
	var readErr error
	if available(cfUnicodeText) {
		var data []byte
		data, readErr = readClipboardBytes(cfUnicodeText, maxTextBytes)
		if readErr == nil {
			result.Text, readErr = decodeUnicodeText(data)
		}
	}

	// Bound the entire snapshot, even when an application advertises duplicate
	// representations of the same large screenshot. Decode after closing the
	// clipboard so image conversion cannot block other applications' copy/paste.
	remaining := maxClipboardBytes
	candidates := make([]clipboardImage, 0, 3)
	for _, name := range []string{"PNG", "image/png"} {
		formatName, _ := windows.UTF16PtrFromString(name)
		format, _, _ := registerClipboardFormat.Call(uintptr(unsafe.Pointer(formatName)))
		if format == 0 || !available(format) {
			continue
		}
		data, err := readClipboardBytes(format, remaining)
		if err != nil {
			readErr = err
			continue
		}
		remaining -= len(data)
		candidates = append(candidates, clipboardImage{data: data})
		// PNG and image/png are aliases used by different applications. One
		// encoded copy is enough; keep the rest of the budget for DIB fallback.
		break
	}
	for _, format := range []uintptr{cfDIBV5, cfDIB} {
		if !available(format) {
			continue
		}
		data, err := readClipboardBytes(format, remaining)
		if err != nil {
			readErr = err
			continue
		}
		remaining -= len(data)
		candidates = append(candidates, clipboardImage{data: data, dib: true})
	}
	return result, candidates, readErr
}

func available(format uintptr) bool {
	ok, _, _ := isClipboardFormatAvailable.Call(format)
	return ok != 0
}

func readClipboardBytes(format uintptr, limit int) ([]byte, error) {
	handle, _, err := getClipboardData.Call(format)
	if handle == 0 {
		return nil, winError("无法读取剪贴板内容", err)
	}
	size, _, err := globalSize.Call(handle)
	if size == 0 {
		return nil, winError("剪贴板数据为空", err)
	}
	if size > uintptr(limit) {
		return nil, errors.New("剪贴板内容超过读取大小限制")
	}
	ptr, _, err := globalLock.Call(handle)
	if ptr == 0 {
		return nil, winError("无法锁定剪贴板数据", err)
	}
	defer globalUnlock.Call(handle)
	// Win32 owns this allocation. Only use it while the clipboard and the
	// movable global allocation are both locked; never retain its pointer.
	data := make([]byte, int(size))
	var copied uintptr
	if err := windows.ReadProcessMemory(windows.CurrentProcess(), ptr, &data[0], size, &copied); err != nil {
		return nil, fmt.Errorf("无法复制剪贴板数据: %w", err)
	}
	if copied != size {
		return nil, errors.New("剪贴板数据未能完整读取")
	}
	return data, nil
}

func winError(message string, err error) error {
	if err != nil && err != syscall.Errno(0) {
		return fmt.Errorf("%s: %w", message, err)
	}
	return errors.New(message)
}
