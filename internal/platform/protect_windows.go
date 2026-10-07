//go:build windows

package platform

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Protect encrypts data using DPAPI for the current Windows user. It is bound to
// that Windows account; a copied vault is not a portable backup.
func Protect(data []byte) ([]byte, error) {
	return cryptData(data, true)
}

// Unprotect authenticates and decrypts a DPAPI blob for the current user.
func Unprotect(data []byte) ([]byte, error) {
	return cryptData(data, false)
}

func cryptData(data []byte, encrypt bool) ([]byte, error) {
	if len(data) == 0 || len(data) > maxProtectedBytes {
		return nil, errors.New("安全存储数据为空或超出大小限制")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	var err error
	if encrypt {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	runtime.KeepAlive(data)
	if err != nil {
		return nil, fmt.Errorf("Windows 安全存储操作失败: %w", err)
	}
	if out.Data == nil {
		return nil, errors.New("Windows 安全存储返回了空数据")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	if out.Size == 0 || out.Size > maxProtectedBytes {
		return nil, errors.New("Windows 安全存储返回的数据超出大小限制")
	}
	buffer := unsafe.Slice(out.Data, int(out.Size))
	result := append([]byte(nil), buffer...)
	if !encrypt {
		// The returned copy belongs to the caller; erase the native plaintext
		// allocation before releasing it back to the OS.
		clear(buffer)
	}
	return result, nil
}
