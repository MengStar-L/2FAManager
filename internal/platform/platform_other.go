//go:build !windows

package platform

func ReadClipboard() (ClipboardData, error) { return ClipboardData{}, ErrUnsupported }

// There is intentionally no plaintext fallback for token secrets.
func Protect([]byte) ([]byte, error)   { return nil, ErrUnsupported }
func Unprotect([]byte) ([]byte, error) { return nil, ErrUnsupported }
