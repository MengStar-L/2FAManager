//go:build windows

package platform

import (
	"bytes"
	"testing"
)

func TestDPAPIRoundTripAndAuthentication(t *testing.T) {
	plain := []byte(`{"secret":"JBSWY3DPEHPK3PXP","account":"fixture@example.test"}`)
	sealed, err := Protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(sealed, plain) || bytes.Contains(sealed, []byte("JBSWY3DPEHPK3PXP")) {
		t.Fatal("ciphertext exposed the secret")
	}
	got, err := Unprotect(sealed)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip failed: %v", err)
	}
	second, err := Protect(plain)
	if err != nil || bytes.Equal(sealed, second) {
		t.Fatalf("encryption did not randomize ciphertext: %v", err)
	}
	for _, broken := range [][]byte{append([]byte(nil), sealed...), sealed[:len(sealed)/2], []byte("plaintext")} {
		broken[len(broken)/2] ^= 0x80
		if _, err := Unprotect(broken); err == nil {
			t.Fatal("unauthenticated ciphertext accepted")
		}
	}
}

func TestDPAPIRejectsEmptyInput(t *testing.T) {
	if _, err := Protect(nil); err == nil {
		t.Fatal("empty plaintext accepted")
	}
	if _, err := Unprotect(nil); err == nil {
		t.Fatal("empty ciphertext accepted")
	}
}
