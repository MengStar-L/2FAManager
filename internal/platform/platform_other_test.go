//go:build !windows

package platform

import (
	"errors"
	"testing"
)

func TestNoPlaintextFallbackOnUnsupportedOS(t *testing.T) {
	if data, err := Protect([]byte("sensitive")); data != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal("protection must explicitly fail on unsupported platforms")
	}
	if data, err := Unprotect([]byte("sensitive")); data != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatal("decryption must explicitly fail on unsupported platforms")
	}
	if _, err := ReadClipboard(); !errors.Is(err, ErrUnsupported) {
		t.Fatal("clipboard must explicitly fail on unsupported platforms")
	}
}
