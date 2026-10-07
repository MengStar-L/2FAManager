package vault

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func testProtection(t *testing.T) (func([]byte) ([]byte, error), func([]byte) ([]byte, error)) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return func(data []byte) ([]byte, error) {
			nonce := make([]byte, aead.NonceSize())
			if _, err := rand.Read(nonce); err != nil {
				return nil, err
			}
			return aead.Seal(nonce, nonce, data, nil), nil
		}, func(data []byte) ([]byte, error) {
			if len(data) < aead.NonceSize() {
				return nil, errors.New("short ciphertext")
			}
			return aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], nil)
		}
}

func newTestStore(t *testing.T) (*Store, func() *Store) {
	t.Helper()
	dir := t.TempDir()
	protect, unprotect := testProtection(t)
	open := func() *Store {
		store, err := openWithProtection(dir, protect, unprotect)
		if err != nil {
			t.Fatal(err)
		}
		store.now = func() time.Time { return time.Unix(59, 0) }
		return store
	}
	return open(), open
}

func testInput(account string) TokenInput {
	return TokenInput{Issuer: "Example", Account: account, Secret: "JBSWY3DPEHPK3PXP", Group: "工作", Color: "mint"}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStoreLifecycleAndSecretBoundary(t *testing.T) {
	store, reopen := newTestStore(t)
	empty, err := store.List()
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty list: %v, %v", empty, err)
	}
	input := testInput("alice@example.com")
	added, err := store.Add(input)
	if err != nil || len(added.ID) != 32 || len(added.Code) != 6 || added.Remaining != 1 {
		t.Fatalf("add: %+v, %v", added, err)
	}
	jsonToken, err := json.Marshal(added)
	if err != nil || bytes.Contains(jsonToken, []byte("secret")) || bytes.Contains(jsonToken, []byte(input.Secret)) {
		t.Fatal("list DTO leaked a secret")
	}
	saved := readFile(t, store.path)
	if bytes.Contains(saved, []byte(input.Secret)) || bytes.Contains(saved, []byte(input.Account)) {
		t.Fatal("vault saved plaintext account or secret")
	}
	duplicate := input
	duplicate.Account = "renamed"
	duplicate.Secret = "jbsw y3dp ehpk 3pxp"
	if _, err := store.Add(duplicate); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate not detected: %v", err)
	}
	if err := store.ToggleFavorite(added.ID); err != nil {
		t.Fatal(err)
	}
	input.Account = "renamed@example.com"
	input.Secret = ""
	updated, err := store.Update(added.ID, input)
	if err != nil || !updated.Favorite || updated.Code != added.Code || updated.Account != input.Account {
		t.Fatalf("edit preserving secret and favorite: %+v, %v", updated, err)
	}
	reopened := reopen()
	tokens, err := reopened.List()
	if err != nil || !reflect.DeepEqual(tokens, []Token{updated}) {
		t.Fatalf("reopened token mismatch: %+v, %v", tokens, err)
	}
	if err := reopened.Delete(added.ID); err != nil {
		t.Fatal(err)
	}
	tokens, err = reopen().List()
	if err != nil || len(tokens) != 0 {
		t.Fatal("deletion was not persisted")
	}
}

func TestBatchImportIsAtomic(t *testing.T) {
	store, reopen := newTestStore(t)
	if _, err := store.ImportURIs([]string{testURI, "not an otp"}); err == nil {
		t.Fatal("invalid batch succeeded")
	}
	if _, err := os.Stat(store.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid batch created a vault")
	}
	if _, err := store.ImportURIs([]string{testURI, testURI}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("batch duplicate accepted: %v", err)
	}
	secondURI := "otpauth://totp/Example:bob?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ&algorithm=SHA256&digits=8&period=60"
	added, err := store.ImportURIs([]string{testURI, secondURI})
	if err != nil || len(added) != 2 {
		t.Fatalf("batch: %v, %v", added, err)
	}
	before := readFile(t, store.path)
	if _, err := store.ImportURIs([]string{"otpauth://totp/new?secret=MZXW6YTBOI", testURI}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("existing duplicate accepted: %v", err)
	}
	if !bytes.Equal(before, readFile(t, store.path)) {
		t.Fatal("failed batch changed the saved vault")
	}
	after, err := reopen().List()
	if err != nil || !reflect.DeepEqual(after, added) {
		t.Fatal("batch did not persist exactly the successful inputs")
	}
}

func TestFailedMutationPreservesMemoryAndDisk(t *testing.T) {
	for _, failure := range []string{"encryption", "write"} {
		for _, operation := range []string{"add", "update", "delete", "favorite", "batch"} {
			t.Run(failure+"/"+operation, func(t *testing.T) {
				store, _ := newTestStore(t)
				added, err := store.Add(testInput("alice"))
				if err != nil {
					t.Fatal(err)
				}
				before, _ := store.List()
				disk := readFile(t, store.path)
				if failure == "encryption" {
					store.protect = func([]byte) ([]byte, error) { return nil, errors.New("injected encryption failure") }
				} else {
					store.write = func(string, []byte) error { return errors.New("injected write failure") }
				}
				input := testInput("bob")
				input.Secret = "MZXW6YTBOI"
				switch operation {
				case "add":
					_, err = store.Add(input)
				case "update":
					_, err = store.Update(added.ID, input)
				case "delete":
					err = store.Delete(added.ID)
				case "favorite":
					err = store.ToggleFavorite(added.ID)
				case "batch":
					_, err = store.ImportURIs([]string{"otpauth://totp/bob?secret=MZXW6YTBOI"})
				}
				if err == nil {
					t.Fatal("injected failure was ignored")
				}
				after, _ := store.List()
				if !reflect.DeepEqual(before, after) || !bytes.Equal(disk, readFile(t, store.path)) {
					t.Fatal("failed mutation changed memory or disk")
				}
			})
		}
	}
}

func TestCorruptVaultNeverOverwritten(t *testing.T) {
	valid := record{ID: "00112233445566778899aabbccddeeff", TokenInput: testInput("alice")}
	valid.TokenInput, _ = normalize(valid.TokenInput)
	badSecret := valid
	badSecret.Secret = "invalid!"
	badID := valid
	badID.ID = "not-a-real-id"
	missingPeriod := valid
	missingPeriod.Period = 0
	duplicateID := valid
	duplicateID.Secret = "MZXW6YTBOI"
	duplicateSecret := valid
	duplicateSecret.ID = "112233445566778899aabbccddeeff00"
	cases := map[string]any{
		"invalid JSON":      []byte("{not-json"),
		"unknown version":   document{Version: 2, Tokens: []record{valid}},
		"missing tokens":    document{Version: 1},
		"invalid secret":    document{Version: 1, Tokens: []record{badSecret}},
		"invalid id":        document{Version: 1, Tokens: []record{badID}},
		"missing period":    document{Version: 1, Tokens: []record{missingPeriod}},
		"duplicate ids":     document{Version: 1, Tokens: []record{valid, duplicateID}},
		"duplicate secrets": document{Version: 1, Tokens: []record{valid, duplicateSecret}},
		"trailing JSON":     []byte(`{"version":1,"tokens":[]} {}`),
		"unknown fields":    []byte(`{"version":1,"tokens":[],"unexpected":true}`),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			protect, unprotect := testProtection(t)
			plain, ok := content.([]byte)
			if !ok {
				var err error
				plain, err = json.Marshal(content)
				if err != nil {
					t.Fatal(err)
				}
			}
			encrypted, _ := protect(plain)
			saved := append([]byte(vaultMagic), encrypted...)
			path := filepath.Join(dir, vaultFileName)
			if err := os.WriteFile(path, saved, 0600); err != nil {
				t.Fatal(err)
			}
			store, err := openWithProtection(dir, protect, unprotect)
			if store != nil || !errors.Is(err, ErrInvalidVault) || !bytes.Equal(saved, readFile(t, path)) {
				t.Fatalf("corrupt vault not rejected/preserved: %v", err)
			}
		})
	}
}

func TestUndecryptableVaultPreserved(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Add(testInput("alice")); err != nil {
		t.Fatal(err)
	}
	saved := readFile(t, store.path)
	otherProtect, otherUnprotect := testProtection(t)
	if _, err := openWithProtection(filepath.Dir(store.path), otherProtect, otherUnprotect); !errors.Is(err, ErrInvalidVault) {
		t.Fatalf("wrong decryption key accepted: %v", err)
	}
	if !bytes.Equal(saved, readFile(t, store.path)) {
		t.Fatal("undecryptable vault changed")
	}
}

func TestInvalidHeaderAndOversizedVaultPreserved(t *testing.T) {
	for _, saved := range [][]byte{nil, []byte(vaultMagic), []byte("unexpected file content"), append([]byte(vaultMagic), make([]byte, maxVaultSize)...)} {
		dir := t.TempDir()
		path := filepath.Join(dir, vaultFileName)
		if err := os.WriteFile(path, saved, 0600); err != nil {
			t.Fatal(err)
		}
		unexpectedCall := func([]byte) ([]byte, error) {
			t.Error("invalid header or oversized vault reached encryption layer")
			return nil, errors.New("unexpected encryption call")
		}
		if _, err := openWithProtection(dir, unexpectedCall, unexpectedCall); !errors.Is(err, ErrInvalidVault) {
			t.Fatalf("invalid vault accepted: %v", err)
		}
		if !bytes.Equal(saved, readFile(t, path)) {
			t.Fatal("invalid vault changed")
		}
	}
}

func TestConcurrentAddsDoNotLoseTokens(t *testing.T) {
	store, reopen := newTestStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			input := testInput(fmt.Sprintf("account-%d", i))
			input.Secret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(fmt.Sprintf("unique-token-secret-%d", i)))
			if _, err := store.Add(input); err != nil {
				t.Errorf("concurrent add: %v", err)
			}
			if _, err := store.List(); err != nil {
				t.Errorf("concurrent list: %v", err)
			}
		}(i)
	}
	wg.Wait()
	tokens, err := reopen().List()
	if err != nil || len(tokens) != 20 {
		t.Fatalf("concurrent tokens lost: count=%d err=%v", len(tokens), err)
	}
}

func TestAtomicWriteCleansUpOnReplaceFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing-directory")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(target, []byte("encrypted data")); err == nil {
		t.Fatal("replace directory should fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "existing-directory" || !entries[0].IsDir() {
		t.Fatal("atomic writer left temporary files or changed existing destination")
	}
}
