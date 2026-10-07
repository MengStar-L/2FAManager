// Package platform contains the operating-system services used by the vault.
package platform

import "errors"

const (
	maxClipboardBytes = 64 << 20
	maxTextBytes      = 2 << 20
	maxImagePixels    = 16 << 20
	maxProtectedBytes = 32 << 20
)

var (
	ErrUnsupported    = errors.New("此系统暂不支持本地安全存储或剪贴板图片读取，请使用 Windows")
	ErrClipboardEmpty = errors.New("剪贴板中没有可识别的图片或文字")
)

// ClipboardData is a snapshot of the clipboard. Image contains PNG or JPEG
// bytes, never a Windows DIB. Both fields can be populated by a clipboard owner.
type ClipboardData struct {
	Text  string
	Image []byte
}
