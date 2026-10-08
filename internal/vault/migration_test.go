package vault

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Fixtures are synthetic. The secrets used in code-vector cases are published
// RFC 6238 test values, never accounts or secrets obtained from a user.
func migrationBytes(field int, value []byte) []byte {
	result := binary.AppendUvarint(nil, uint64(field<<3|2))
	result = binary.AppendUvarint(result, uint64(len(value)))
	return append(result, value...)
}

func migrationNumber(field int, value uint64) []byte {
	result := binary.AppendUvarint(nil, uint64(field<<3))
	return binary.AppendUvarint(result, value)
}

func migrationFixtureToken(secret []byte, name, issuer string, algorithm, digits, kind uint64) []byte {
	var result []byte
	result = append(result, migrationBytes(1, secret)...)
	result = append(result, migrationBytes(2, []byte(name))...)
	result = append(result, migrationBytes(3, []byte(issuer))...)
	result = append(result, migrationNumber(4, algorithm)...)
	result = append(result, migrationNumber(5, digits)...)
	return append(result, migrationNumber(6, kind)...)
}

func migrationFixturePayload(size, index, id int32, tokens ...[]byte) []byte {
	var result []byte
	for _, token := range tokens {
		result = append(result, migrationBytes(1, token)...)
	}
	result = append(result, migrationNumber(2, 1)...)
	result = append(result, migrationNumber(3, uint64(int64(size)))...)
	result = append(result, migrationNumber(4, uint64(int64(index)))...)
	return append(result, migrationNumber(5, uint64(int64(id)))...)
}

func migrationFixtureURI(payload []byte) string {
	return "otpauth-migration://offline?data=" + url.QueryEscape(base64.StdEncoding.EncodeToString(payload))
}

func migrationSyntheticToken(n int) []byte {
	return migrationFixtureToken([]byte(fmt.Sprintf("synthetic-unique-secret-%d", n)), fmt.Sprintf("account-%d@example.invalid", n), "OpenAI", 1, 1, 2)
}

func TestMigrationDefaultAndExplicitOTPParameters(t *testing.T) {
	for _, tc := range []struct {
		name      string
		secret    string
		algorithm uint64
		digits    uint64
		kind      uint64
		wantAlgo  string
		wantCode  string
	}{
		{"protobuf-defaults", "12345678901234567890", 0, 0, 0, "SHA1", "287082"},
		{"sha1-eight", "12345678901234567890", 1, 2, 2, "SHA1", "94287082"},
		{"sha256-eight", "12345678901234567890123456789012", 2, 2, 2, "SHA256", "46119246"},
		{"sha512-eight", "1234567890123456789012345678901234567890123456789012345678901234", 3, 2, 2, "SHA512", "90693936"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := migrationFixtureToken([]byte(tc.secret), "OpenAI: demo@example.invalid", "OpenAI", tc.algorithm, tc.digits, tc.kind)
			chunk, err := ParseImportURI(migrationFixtureURI(migrationFixturePayload(1, 0, 42, token)))
			if err != nil || len(chunk.Inputs) != 1 {
				t.Fatalf("parse: %v", err)
			}
			input := chunk.Inputs[0]
			if input.Issuer != "OpenAI" || input.Account != "demo@example.invalid" || input.Algorithm != tc.wantAlgo || input.Period != 30 {
				t.Fatal("parsed metadata/algorithm mismatch")
			}
			code, _, err := codeAt(input, 59)
			if err != nil || code != tc.wantCode {
				t.Fatalf("imported secret failed the RFC 6238 reference vector: %v", err)
			}
			if chunk.Migration.ID != "42" || chunk.Migration.Size != 1 || chunk.Migration.Index != 0 {
				t.Fatal("metadata mismatch")
			}
		})
	}
	// A protobuf encoder omits default values entirely. Accept those as well.
	token := append(migrationBytes(1, []byte("12345678901234567890")), migrationBytes(2, []byte("Example:demo@example.invalid"))...)
	chunk, err := ParseImportURI(migrationFixtureURI(migrationBytes(1, token)))
	if err != nil || chunk.Migration.Size != 1 || chunk.Migration.Index != 0 || chunk.Inputs[0].Issuer != "Example" || chunk.Inputs[0].Algorithm != "SHA1" || chunk.Inputs[0].Digits != 6 {
		t.Fatalf("omitted defaults: %v", err)
	}
	encoded, _ := json.Marshal(chunk.Migration)
	if !bytes.Contains(encoded, []byte(`"id"`)) || !bytes.Contains(encoded, []byte(`"size":1`)) || !bytes.Contains(encoded, []byte(`"index":0`)) {
		t.Fatal("migration metadata must use the frontend's lowercase JSON fields")
	}
}

func TestMigrationBase64AndCanonicalPageDeduplication(t *testing.T) {
	token := migrationFixtureToken([]byte{0xfb, 0xef, 0xff, 0xfb, 0xef, 0xff}, "demo@example.invalid", "Example", 1, 1, 2)
	payload := migrationFixturePayload(1, 0, -123, token)
	standard := base64.StdEncoding.EncodeToString(payload)
	if !strings.ContainsAny(standard, "+/") {
		t.Fatal("fixture does not exercise the standard Base64 alphabet")
	}
	var uris []string
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		for _, escape := range []bool{true, false} {
			data := encoding.EncodeToString(payload)
			if escape {
				data = url.QueryEscape(data)
			}
			uris = append(uris, "otpauth-migration://offline?data="+data)
		}
	}
	chunks, err := ParseImportURIs(uris, true)
	if err != nil || len(chunks) != 1 || chunks[0].Migration.ID != "-123" || chunks[0].URI != migrationFixtureURI(payload) {
		t.Fatalf("canonical deduplication: %v", err)
	}
	standardChunk, err := ParseImportURI("  " + testURI + "  ")
	if err != nil || standardChunk.URI != testURI || standardChunk.Migration != nil || len(standardChunk.Inputs) != 1 {
		t.Fatalf("standard URI compatibility: %v", err)
	}
}

func TestMigrationBatchesCompletePartialConflictsAndIndependentExports(t *testing.T) {
	page0 := migrationFixtureURI(migrationFixturePayload(2, 0, 123, migrationSyntheticToken(1)))
	page1 := migrationFixtureURI(migrationFixturePayload(2, 1, 123, migrationSyntheticToken(2)))
	if chunks, err := ParseImportURIs([]string{page1}, false); err != nil || len(chunks) != 1 {
		t.Fatalf("partial preview rejected: %v", err)
	}
	if _, err := ParseImportURIs([]string{page0}, true); err == nil || !strings.Contains(err.Error(), "1/2") {
		t.Fatal("incomplete import must explain the missing pages")
	}
	if chunks, err := ParseImportURIs([]string{page1, page0, page1}, true); err != nil || len(chunks) != 2 || chunks[0].Migration.Index != 1 {
		t.Fatalf("out-of-order complete pages: %v", err)
	}
	conflicting := migrationFixtureURI(migrationFixturePayload(2, 0, 123, migrationSyntheticToken(3)))
	wrongSize := migrationFixtureURI(migrationFixturePayload(3, 1, 123, migrationSyntheticToken(2)))
	for _, other := range []string{conflicting, wrongSize} {
		if _, err := ParseImportURIs([]string{page0, other}, false); err == nil {
			t.Fatal("conflicting page or batch size was accepted")
		}
	}
	other := migrationFixtureURI(migrationFixturePayload(1, 0, 456, migrationSyntheticToken(4)))
	if chunks, err := ParseImportURIs([]string{other, page0, page1, testURI}, true); err != nil || len(chunks) != 4 {
		t.Fatalf("multiple independent batches: %v", err)
	}
	var legacy []string
	for i := 1; i <= 2; i++ {
		legacy = append(legacy, migrationFixtureURI(migrationBytes(1, migrationSyntheticToken(i))))
	}
	if chunks, err := ParseImportURIs(legacy, true); err != nil || len(chunks) != 2 || chunks[0].Migration.ID == chunks[1].Migration.ID {
		t.Fatalf("independent metadata-less pages collided: %v", err)
	}
}

func TestMigrationRejectsUnsupportedMalformedAndOversizeInputs(t *testing.T) {
	validToken := migrationSyntheticToken(1)
	validPayload := migrationFixturePayload(1, 0, 42, validToken)
	validURI := migrationFixtureURI(validPayload)
	cases := map[string]string{
		"invalid-base64":      "otpauth-migration://offline?data=%%invalid",
		"invalid-base64-bits": "otpauth-migration://offline?data=Zh==",
		"empty-data":          "otpauth-migration://offline?data=",
		"duplicate-data":      validURI + "&data=AA==",
		"unknown-query":       validURI + "&period=60",
		"fragment":            validURI + "#fragment",
		"userinfo":            strings.Replace(validURI, "//offline", "//user@offline", 1),
		"wrong-host":          strings.Replace(validURI, "//offline", "//example.invalid", 1),
		"port":                strings.Replace(validURI, "//offline", "//offline:443", 1),
		"path":                strings.Replace(validURI, "offline?", "offline/path?", 1),
		"whitespace-data":     "otpauth-migration://offline?data=%20" + base64.StdEncoding.EncodeToString(validPayload),
		"uri-limit":           "otpauth-migration://offline?data=" + strings.Repeat("A", maxMigrationURI),
	}
	invalidPayloads := map[string][]byte{
		"empty-payload":             {},
		"missing-tokens":            migrationNumber(2, 1),
		"zero-tag":                  {0},
		"truncated-tag":             {0x80},
		"overflow-tag":              bytes.Repeat([]byte{0xff}, 11),
		"truncated-varint":          {0x10, 0x80},
		"overflow-varint":           append([]byte{0x10}, bytes.Repeat([]byte{0xff}, 11)...),
		"length-overrun":            {0x0a, 0x7f, 0},
		"length-overflow":           append([]byte{0x0a}, bytes.Repeat([]byte{0xff}, 11)...),
		"wrong-token-wire":          migrationNumber(1, 0),
		"wrong-metadata-wire":       append(migrationBytes(1, validToken), migrationBytes(2, []byte{1})...),
		"duplicate-version":         append(append([]byte(nil), validPayload...), migrationNumber(2, 1)...),
		"unsupported-version":       append(migrationBytes(1, validToken), migrationNumber(2, 2)...),
		"oversized-int32":           append(migrationBytes(1, validToken), migrationNumber(3, 1<<40)...),
		"negative-size":             migrationFixturePayload(-1, 0, 42, validToken),
		"too-many-pages":            migrationFixturePayload(101, 0, 42, validToken),
		"negative-index":            migrationFixturePayload(2, -1, 42, validToken),
		"index-out-of-range":        migrationFixturePayload(2, 2, 42, validToken),
		"unknown-truncated-fixed64": append(append([]byte(nil), validPayload...), 0x51, 1),
		"unknown-truncated-fixed32": append(append([]byte(nil), validPayload...), 0x55, 1),
		"unsupported-group":         append(append([]byte(nil), validPayload...), 0x53, 0x54),
		"invalid-wire":              append(append([]byte(nil), validPayload...), 0x57),
	}
	for name, payload := range invalidPayloads {
		cases[name] = migrationFixtureURI(payload)
	}
	for name, token := range map[string][]byte{
		"hotp":                 migrationFixtureToken([]byte("synthetic-secret"), "demo", "Example", 1, 1, 1),
		"md5":                  migrationFixtureToken([]byte("synthetic-secret"), "demo", "Example", 4, 1, 2),
		"unknown-algorithm":    migrationFixtureToken([]byte("synthetic-secret"), "demo", "Example", 100, 1, 2),
		"unknown-digits":       migrationFixtureToken([]byte("synthetic-secret"), "demo", "Example", 1, 100, 2),
		"unknown-type":         migrationFixtureToken([]byte("synthetic-secret"), "demo", "Example", 1, 1, 100),
		"empty-secret":         migrationFixtureToken(nil, "demo", "Example", 1, 1, 2),
		"oversize-secret":      migrationFixtureToken(make([]byte, 513), "demo", "Example", 1, 1, 2),
		"empty-account":        migrationFixtureToken([]byte("synthetic-secret"), "", "Example", 1, 1, 2),
		"oversize-account":     migrationFixtureToken([]byte("synthetic-secret"), strings.Repeat("a", 321), "Example", 1, 1, 2),
		"oversize-issuer":      migrationFixtureToken([]byte("synthetic-secret"), "demo", strings.Repeat("a", 201), 1, 1, 2),
		"invalid-utf8":         migrationFixtureToken([]byte("synthetic-secret"), string([]byte{0xff}), "Example", 1, 1, 2),
		"control-character":    migrationFixtureToken([]byte("synthetic-secret"), "demo\n", "Example", 1, 1, 2),
		"counter-on-totp":      append(append([]byte(nil), validToken...), migrationNumber(7, 1)...),
		"duplicate-secret":     append(append([]byte(nil), validToken...), migrationBytes(1, []byte("second-synthetic-secret"))...),
		"duplicate-type":       append(append([]byte(nil), validToken...), migrationNumber(6, 2)...),
		"wrong-secret-wire":    migrationNumber(1, 1),
		"wrong-algorithm-wire": append(migrationBytes(1, []byte("synthetic-secret")), migrationBytes(4, []byte{1})...),
	} {
		cases[name] = migrationFixtureURI(migrationFixturePayload(1, 0, 42, validToken, token))
	}
	for name, uri := range cases {
		t.Run(name, func(t *testing.T) {
			chunk, err := ParseImportURI(uri)
			if err == nil || len(chunk.Inputs) != 0 {
				t.Fatal("invalid migration exposed usable partial results")
			}
			if strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "data=") || strings.Contains(err.Error(), "account-1") {
				t.Fatal("migration error disclosed source contents")
			}
		})
	}
}

func TestMigrationImportTokenLimitAndUnknownFields(t *testing.T) {
	var tokens [][]byte
	for i := 0; i < maxImportTokens; i++ {
		tokens = append(tokens, migrationSyntheticToken(i))
	}
	full := migrationFixtureURI(migrationFixturePayload(1, 0, 42, tokens...))
	if chunks, err := ParseImportURIs([]string{full}, true); err != nil || len(chunks[0].Inputs) != 100 {
		t.Fatalf("maximum-sized batch rejected: %v", err)
	}
	if _, err := ParseImportURI(migrationFixtureURI(migrationFixturePayload(1, 0, 42, append(tokens, migrationSyntheticToken(100))...))); err == nil {
		t.Fatal("101-token page accepted")
	}
	if _, err := ParseImportURIs([]string{full, testURI}, false); err == nil {
		t.Fatal("aggregate token limit ignored")
	}
	if _, err := ParseImportURIs(make([]string, 101), false); err == nil {
		t.Fatal("source limit ignored")
	}
	if _, err := ParseImportURIs(nil, false); err == nil {
		t.Fatal("empty source accepted")
	}
	unknown := append(migrationNumber(20, 42), migrationBytes(21, []byte("future metadata"))...)
	unknown = append(unknown, 0xb1, 1, 0, 0, 0, 0, 0, 0, 0, 0) // field 22, fixed64
	unknown = append(unknown, 0xbd, 1, 0, 0, 0, 0)             // field 23, fixed32
	payload := migrationFixturePayload(1, 0, 42, append(migrationSyntheticToken(1), unknown...))
	payload = append(payload, unknown...)
	if _, err := ParseImportURI(migrationFixtureURI(payload)); err != nil {
		t.Fatalf("well-formed unknown fields rejected: %v", err)
	}
}

func TestStoreMigrationAtomicImportAndPersistence(t *testing.T) {
	store, reopen := newTestStore(t)
	first, err := store.Add(testInput("existing@example.invalid"))
	if err != nil {
		t.Fatal(err)
	}
	before := readFile(t, store.path)
	page0 := migrationFixtureURI(migrationFixturePayload(2, 0, 42, migrationSyntheticToken(1)))
	page1 := migrationFixtureURI(migrationFixturePayload(2, 1, 42, migrationSyntheticToken(2), migrationSyntheticToken(3)))
	for _, uris := range [][]string{
		{page0},
		{page0, page1, "invalid"},
		{page0, migrationFixtureURI(migrationFixturePayload(2, 1, 42, migrationSyntheticToken(1)))},
		{page0, page1, testURI}, // duplicates the existing vault's secret
	} {
		if _, err := store.ImportURIs(uris); err == nil {
			t.Fatal("invalid migration batch committed")
		}
		got, _ := store.List()
		if !reflect.DeepEqual(got, []Token{first}) || !bytes.Equal(before, readFile(t, store.path)) {
			t.Fatal("rejected migration changed memory or encrypted vault bytes")
		}
	}
	added, err := store.ImportURIs([]string{page1, page0, page1})
	if err != nil || len(added) != 3 {
		t.Fatalf("complete migration: %v", err)
	}
	for _, token := range added {
		if token.Group != "OpenAI" || token.Issuer != "OpenAI" || token.Period != 30 || len(token.Code) != 6 {
			t.Fatal("migration lost service defaults or TOTP metadata")
		}
	}
	got, err := reopen().List()
	if err != nil || !reflect.DeepEqual(got, append([]Token{first}, added...)) {
		t.Fatalf("migration did not persist the exact complete batch: %v", err)
	}
	saved := readFile(t, store.path)
	if bytes.Contains(saved, []byte("synthetic-unique-secret")) || bytes.Contains(saved, []byte("account-1@example.invalid")) {
		t.Fatal("migration persisted plaintext sensitive fields")
	}
	if _, err := store.ImportURIs([]string{page0, page1}); !errors.Is(err, ErrDuplicate) || !bytes.Equal(saved, readFile(t, store.path)) {
		t.Fatal("reimporting stored migration was not rejected atomically")
	}
}

func TestStoreMigrationStorageFailuresDoNotPublish(t *testing.T) {
	for _, failure := range []string{"encryption", "write"} {
		t.Run(failure, func(t *testing.T) {
			store, _ := newTestStore(t)
			if failure == "encryption" {
				store.protect = func([]byte) ([]byte, error) { return nil, errors.New("synthetic failure") }
			} else {
				store.write = func(string, []byte) error { return errors.New("synthetic failure") }
			}
			uri := migrationFixtureURI(migrationFixturePayload(1, 0, 42, migrationSyntheticToken(1), migrationSyntheticToken(2)))
			if _, err := store.ImportURIs([]string{uri}); err == nil {
				t.Fatal("failed storage accepted import")
			}
			got, _ := store.List()
			if len(got) != 0 {
				t.Fatal("storage failure leaked a partial in-memory import")
			}
			if _, err := os.Stat(store.path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("storage failure replaced original absent vault")
			}
		})
	}
}

func TestStoreSingleImportCannotSilentlyImportMultipleMigrationTokens(t *testing.T) {
	store, _ := newTestStore(t)
	uri := migrationFixtureURI(migrationFixturePayload(1, 0, 42, migrationSyntheticToken(1), migrationSyntheticToken(2)))
	if _, err := store.ImportURI(uri); err == nil {
		t.Fatal("single-result API silently imported multiple tokens")
	}
	got, _ := store.List()
	if len(got) != 0 {
		t.Fatal("single-result API rejected after modifying the vault")
	}
	if _, err := os.Stat(store.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("single-result API wrote an unreportable batch")
	}
	single := migrationFixtureURI(migrationFixturePayload(1, 0, 42, migrationSyntheticToken(1)))
	if added, err := store.ImportURI(single); err != nil || added.Account != "account-1@example.invalid" {
		t.Fatalf("single-token migration compatibility: %v", err)
	}
}

func FuzzMigrationPayload(f *testing.F) {
	f.Add(migrationFixturePayload(1, 0, 42, migrationSyntheticToken(1)))
	f.Add([]byte{0x0a, 0xff, 0xff, 0xff})
	f.Add([]byte{0})
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > maxMigrationPayload {
			t.Skip()
		}
		chunk, err := ParseImportURI(migrationFixtureURI(payload))
		if err != nil {
			if len(chunk.Inputs) != 0 {
				t.Fatal("failure returned partial secrets")
			}
			return
		}
		if len(chunk.Inputs) < 1 || len(chunk.Inputs) > 100 || chunk.Migration == nil || chunk.Migration.Size < 1 || chunk.Migration.Index < 0 || chunk.Migration.Index >= chunk.Migration.Size {
			t.Fatal("successful parse has invalid bounds")
		}
		for _, input := range chunk.Inputs {
			if normalized, err := normalize(input); err != nil || normalized != input {
				t.Fatal("successful parse returned a non-normalized token")
			}
		}
	})
}
