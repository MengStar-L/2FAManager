//go:build windows

package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsDPAPIVaultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	input := testInput("dpapi-round-trip@example.com")
	added, err := store.Add(input)
	if err != nil {
		t.Fatal(err)
	}
	onDisk := readFile(t, store.path)
	if bytes.Contains(onDisk, []byte(input.Secret)) || bytes.Contains(onDisk, []byte(input.Account)) {
		t.Fatal("DPAPI vault contains plaintext data")
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := reopened.List()
	if err != nil || len(tokens) != 1 || tokens[0].ID != added.ID || tokens[0].Account != input.Account {
		t.Fatalf("DPAPI round trip failed: %v", err)
	}
}

func TestWindowsLockedVaultKeepsPreviousState(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Add(testInput("alice")); err != nil {
		t.Fatal(err)
	}
	before, _ := store.List()
	saved := readFile(t, store.path)
	path, err := windows.UTF16PtrFromString(store.path)
	if err != nil {
		t.Fatal(err)
	}
	// Deny write/delete sharing to exercise an actual failed atomic replacement,
	// as can happen when backup or antivirus software holds the vault open.
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	input := testInput("bob")
	input.Secret = "MZXW6YTBOI"
	if _, err := store.Add(input); err == nil {
		t.Fatal("locked vault replacement succeeded unexpectedly")
	}
	after, _ := store.List()
	if !reflect.DeepEqual(before, after) || !bytes.Equal(saved, readFile(t, store.path)) {
		t.Fatal("failed replacement changed memory or the previous vault file")
	}
	entries, err := os.ReadDir(filepath.Dir(store.path))
	if err != nil || len(entries) != 1 || entries[0].Name() != vaultFileName {
		t.Fatal("failed replacement left a temporary file")
	}
}
