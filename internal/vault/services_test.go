package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestOpenAIGroupDefaultsOnAddAndUpdate(t *testing.T) {
	for _, issuer := range []string{"OpenAI", "OPENAI", "  openai  ", "Open AI", "open-ai", "open_ai", "OpenAI.com", "www.openai.com"} {
		t.Run(issuer, func(t *testing.T) {
			store, reopen := newTestStore(t)
			input := testInput("group-fixture@example.com")
			input.Issuer, input.Group = issuer, ""
			added, err := store.Add(input)
			if err != nil || added.Group != "OpenAI" {
				t.Fatalf("default on add: %+v, %v", added, err)
			}
			input.Secret, input.Group = "", "我的工作"
			updated, err := store.Update(added.ID, input)
			if err != nil || updated.Group != "我的工作" {
				t.Fatalf("explicit group lost: %+v, %v", updated, err)
			}
			input.Group = ""
			updated, err = store.Update(added.ID, input)
			if err != nil || updated.Group != "OpenAI" || updated.Code != added.Code {
				t.Fatalf("default on edit: %+v, %v", updated, err)
			}
			tokens, err := reopen().List()
			if err != nil || len(tokens) != 1 || tokens[0].Group != "OpenAI" {
				t.Fatal("default group was not persisted")
			}
		})
	}
	for _, issuer := range []string{"OpenAI Team", "notopenai", "openai.com.example.com", "ChatGPT", "Example"} {
		t.Run("unmatched/"+issuer, func(t *testing.T) {
			store, _ := newTestStore(t)
			input := testInput("other-fixture@example.com")
			input.Issuer, input.Group = issuer, ""
			added, err := store.Add(input)
			if err != nil || added.Group != "" {
				t.Fatalf("unrelated issuer was regrouped: %+v, %v", added, err)
			}
		})
	}
	store, _ := newTestStore(t)
	input := testInput("custom-group@example.com")
	input.Issuer = "OpenAI"
	added, err := store.Add(input)
	if err != nil || added.Group != input.Group {
		t.Fatalf("explicit group on add changed: %+v, %v", added, err)
	}
}

func TestOpenAIGroupDefaultOnQRImport(t *testing.T) {
	store, reopen := newTestStore(t)
	added, err := store.ImportURIs([]string{
		"otpauth://totp/OpenAI:first%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=OpenAI",
		"otpauth://totp/second%40example.com?secret=MZXW6YTBOI&issuer=openai.com",
		"otpauth://totp/Example:third%40example.com?secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
	})
	if err != nil || len(added) != 3 {
		t.Fatalf("import failed: %v", err)
	}
	if added[0].Group != "OpenAI" || added[1].Group != "OpenAI" || added[2].Group != "" {
		t.Fatalf("unexpected imported groups: %+v", added)
	}
	reloaded, err := reopen().List()
	if err != nil || !reflect.DeepEqual(added, reloaded) {
		t.Fatal("imported default groups were not persisted")
	}
}

func legacyGroupRecords(t *testing.T) []record {
	t.Helper()
	var result []record
	for i, item := range []struct{ issuer, group, secret, id string }{
		{"OpenAI", "", "JBSWY3DPEHPK3PXP", "00112233445566778899aabbccddeeff"},
		{"openai", "我的工作", "MZXW6YTBOI", "112233445566778899aabbccddeeff00"},
		{"Example", "", "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "2233445566778899aabbccddeeff0011"},
	} {
		input := testInput("legacy-fixture@example.com")
		input.Issuer, input.Group, input.Secret = item.issuer, item.group, item.secret
		input, err := normalize(input)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, record{ID: item.id, Favorite: i == 0, TokenInput: input})
	}
	return result
}

func writeLegacyGroupVault(t *testing.T, dir string, records []record, protect func([]byte) ([]byte, error)) []byte {
	t.Helper()
	plain, err := json.Marshal(document{Version: 1, Tokens: records})
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	saved := append([]byte(vaultMagic), encrypted...)
	if err := os.WriteFile(filepath.Join(dir, vaultFileName), saved, 0600); err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestOpenAIGroupMigrationPreservesMetadataAndPersists(t *testing.T) {
	dir := t.TempDir()
	protect, unprotect := testProtection(t)
	original := legacyGroupRecords(t)
	before := writeLegacyGroupVault(t, dir, original, protect)
	store, err := openWithProtection(dir, protect, unprotect)
	if err != nil {
		t.Fatal(err)
	}
	expected := append([]record(nil), original...)
	expected[0].Group = "OpenAI"
	if !reflect.DeepEqual(store.records, expected) || store.Warning() != "" {
		t.Fatalf("migration changed other token data or warned: %+v", store.records)
	}
	after := readFile(t, store.path)
	if bytes.Equal(before, after) || bytes.Contains(after, []byte(original[0].Secret)) || bytes.Contains(after, []byte(original[0].Account)) {
		t.Fatal("migration not persisted as encrypted data")
	}
	store, err = openWithStorage(dir, protect, unprotect, func(string, []byte) error {
		t.Fatal("completed migration unexpectedly rewrote vault")
		return nil
	})
	if err != nil || !reflect.DeepEqual(store.records, expected) {
		t.Fatalf("migration did not survive reopening: %v", err)
	}
}

func TestOpenAIGroupMigrationFailureKeepsVaultUsableAndRetries(t *testing.T) {
	for _, failure := range []string{"encryption", "write"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			protect, unprotect := testProtection(t)
			original := legacyGroupRecords(t)
			before := writeLegacyGroupVault(t, dir, original, protect)
			failingProtect, failingWrite := protect, atomicWrite
			if failure == "encryption" {
				failingProtect = func([]byte) ([]byte, error) { return nil, errors.New("synthetic encryption failure") }
			} else {
				failingWrite = func(string, []byte) error { return errors.New("synthetic write failure") }
			}
			store, err := openWithStorage(dir, failingProtect, unprotect, failingWrite)
			if err != nil || store == nil || store.Warning() == "" {
				t.Fatalf("migration failure blocked vault or omitted warning: %v", err)
			}
			tokens, err := store.List()
			if err != nil || len(tokens) != 3 || tokens[0].Group != "OpenAI" || len(tokens[0].Code) != 6 {
				t.Fatalf("existing tokens unusable after migration failure: %+v, %v", tokens, err)
			}
			if !bytes.Equal(before, readFile(t, store.path)) {
				t.Fatal("failed migration changed original encrypted vault")
			}
			expected := append([]record(nil), original...)
			expected[0].Group = "OpenAI"
			if !reflect.DeepEqual(store.records, expected) {
				t.Fatal("failed migration changed fields other than derived group")
			}
			store.protect, store.write = protect, atomicWrite
			if err := store.ToggleFavorite(original[0].ID); err != nil || store.Warning() != "" {
				t.Fatalf("subsequent save did not recover: %v", err)
			}
			reopened, err := openWithProtection(dir, protect, unprotect)
			if err != nil || reopened.records[0].Group != "OpenAI" || reopened.records[0].Favorite {
				t.Fatalf("recovered save did not persist group and edit: %v", err)
			}
		})
	}
}

func TestOpenAIGroupMigrationValidatesEntireVaultBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	protect, unprotect := testProtection(t)
	records := legacyGroupRecords(t)
	records[2].Period = 0
	before := writeLegacyGroupVault(t, dir, records, protect)
	store, err := openWithStorage(dir, protect, unprotect, func(string, []byte) error {
		t.Fatal("invalid vault reached migration write")
		return nil
	})
	if store != nil || !errors.Is(err, ErrInvalidVault) || !bytes.Equal(before, readFile(t, filepath.Join(dir, vaultFileName))) {
		t.Fatalf("invalid vault was migrated or changed: %v", err)
	}
}

func TestAccountLookupIsIndependentOfCodeGeneration(t *testing.T) {
	store, _ := newTestStore(t)
	added, err := store.Add(testInput("account-fixture@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return time.Unix(-1, 0) }
	if _, err := store.List(); err == nil {
		t.Fatal("synthetic invalid clock did not prevent code generation")
	}
	account, err := store.Account(added.ID)
	if err != nil || account != "account-fixture@example.com" {
		t.Fatalf("metadata lookup required a valid code: %q, %v", account, err)
	}
	if account, err := store.Account("missing"); !errors.Is(err, ErrNotFound) || account != "" {
		t.Fatalf("invalid metadata lookup returned data: %q, %v", account, err)
	}
}
