package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/draw"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Fixtures use public, invented credentials. No captured transfer QR or real
// account is needed to exercise Google's protobuf-in-QR transport.
type migrationFixtureAccount struct {
	name, issuer, secret    string
	algorithm, digits, kind uint64
}

func migrationFixtureVarint(dst []byte, field, value uint64) []byte {
	dst = binary.AppendUvarint(dst, field<<3)
	return binary.AppendUvarint(dst, value)
}

func migrationFixtureBytes(dst []byte, field uint64, value []byte) []byte {
	dst = binary.AppendUvarint(dst, field<<3|2)
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func migrationFixtureURI(size, index, id uint64, accounts ...migrationFixtureAccount) string {
	var payload []byte
	for _, account := range accounts {
		var token []byte
		token = migrationFixtureBytes(token, 1, []byte(account.secret))
		token = migrationFixtureBytes(token, 2, []byte(account.name))
		token = migrationFixtureBytes(token, 3, []byte(account.issuer))
		token = migrationFixtureVarint(token, 4, account.algorithm)
		token = migrationFixtureVarint(token, 5, account.digits)
		token = migrationFixtureVarint(token, 6, account.kind)
		payload = migrationFixtureBytes(payload, 1, token)
	}
	payload = migrationFixtureVarint(payload, 2, 1)
	payload = migrationFixtureVarint(payload, 3, size)
	payload = migrationFixtureVarint(payload, 4, index)
	payload = migrationFixtureVarint(payload, 5, id)
	return "otpauth-migration://offline?data=" + url.QueryEscape(base64.StdEncoding.EncodeToString(payload))
}

func migrationFixtureAccounts() []migrationFixtureAccount {
	return []migrationFixtureAccount{
		{"OpenAI:migration-one@example.test", "OpenAI", "migration-test-key-01", 1, 1, 2},
		{"Google:migration-two@example.test", "Google", "migration-test-key-02", 2, 2, 2},
	}
}

func migrationPairImage(t *testing.T, first, second string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 800, 400))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(10, 10, 330, 330), qrImage(t, first), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(460, 10, 780, 330), qrImage(t, second), image.Point{}, draw.Src)
	return pngBytes(t, img)
}

func TestMigrationPreviewExpandsAccountsAndPreservesBatchSource(t *testing.T) {
	accounts := migrationFixtureAccounts()
	uri := migrationFixtureURI(1, 0, 2026100801, accounts...)
	a := NewApp()
	for _, fromImage := range []bool{false, true} {
		var previews []ImportPreview
		var err error
		if fromImage {
			previews, err = a.PreviewImage(base64.StdEncoding.EncodeToString(pngBytes(t, qrImage(t, uri))))
		} else {
			previews, err = a.PreviewText(uri)
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(previews) != 2 {
			t.Fatalf("expected two migration previews; count=%d", len(previews))
		}
		for i, preview := range previews {
			if preview.Issuer != accounts[i].issuer || preview.Migration == nil || preview.Migration.Size != 1 || preview.Migration.Index != 0 || preview.Migration.ID != "2026100801" || preview.Account != strings.TrimPrefix(accounts[i].name, accounts[i].issuer+":") {
				t.Fatal("migration preview metadata was lost")
			}
			if preview.URI != uri {
				t.Fatal("migration preview was split into individual token links")
			}
		}
	}
	standard, err := a.PreviewText(testURI)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(standard)
	if err != nil || bytes.Contains(encoded, []byte(`"migration"`)) {
		t.Fatal("standard QR acquired migration metadata")
	}
}

func TestMigrationPreviewMixedAndPartialPages(t *testing.T) {
	accounts := migrationFixtureAccounts()
	first := migrationFixtureURI(2, 0, 2026100802, accounts[0])
	second := migrationFixtureURI(2, 1, 2026100802, accounts[1])
	for name, uris := range map[string][]string{
		"partial page":           {first},
		"out of order pages":     {second, first},
		"same page again":        {first, first},
		"standard and migration": {testURI, first},
	} {
		t.Run(name, func(t *testing.T) {
			previews, err := NewApp().PreviewText(strings.Join(uris, "\n"))
			want := len(uris)
			if name == "same page again" {
				want = 1
			}
			if err != nil || len(previews) != want {
				t.Fatalf("migration preview failed: count=%d err=%v", len(previews), err)
			}
		})
	}
	previews, err := previewImageBytes(migrationPairImage(t, testURI, first))
	if err != nil || len(previews) != 2 {
		t.Fatalf("mixed screenshot failed: count=%d err=%v", len(previews), err)
	}
	previews, err = previewImageBytes(migrationPairImage(t, first, second))
	if err != nil || len(previews) != 2 {
		t.Fatalf("two-page screenshot failed: count=%d err=%v", len(previews), err)
	}
}

func TestMigrationPreviewRejectsWholeInvalidScreenshotAndLimits(t *testing.T) {
	accounts := migrationFixtureAccounts()
	unsupported := accounts[1]
	unsupported.kind = 1 // HOTP cannot be represented by this TOTP vault.
	invalid := migrationFixtureURI(1, 0, 2026100803, accounts[0], unsupported)
	for _, raw := range []string{invalid, "otpauth-migration://offline?data=not-base64", "otpauth://totp/broken?secret=invalid!"} {
		if previews, err := previewImageBytes(migrationPairImage(t, testURI, raw)); err == nil || len(previews) != 0 {
			t.Fatal("invalid OTP QR was silently dropped from the screenshot")
		}
	}
	if _, err := NewApp().PreviewText(strings.Repeat(testURI+"\n", 101)); err == nil {
		t.Fatal("over 100 text links were accepted")
	}
	tooMany := make([]migrationFixtureAccount, 101)
	for i := range tooMany {
		tooMany[i] = accounts[0]
	}
	if _, err := NewApp().PreviewText(migrationFixtureURI(1, 0, 2026100804, tooMany...)); err == nil {
		t.Fatal("over 100 expanded accounts were accepted")
	}
}

func TestMigrationAppImportIsCompleteAtomicAndPreviewIsReadOnly(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("vault uses Windows DPAPI")
	}
	a := NewApp()
	dir := t.TempDir()
	a.init(dir)
	if err := a.ready(); err != nil {
		t.Fatal(err)
	}
	accounts := migrationFixtureAccounts()
	first := migrationFixtureURI(2, 0, 2026100805, accounts[0])
	second := migrationFixtureURI(2, 1, 2026100805, accounts[1])
	if _, err := a.PreviewText(first); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PreviewImage(base64.StdEncoding.EncodeToString(pngBytes(t, qrImage(t, second)))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vault.dat")); !os.IsNotExist(err) {
		t.Fatal("preview wrote a vault before confirmation")
	}
	if _, err := a.ImportTokens([]string{first, first}); err == nil {
		t.Fatal("incomplete batch was imported")
	}
	if tokens, err := a.GetTokens(); err != nil || len(tokens) != 0 {
		t.Fatal("incomplete batch changed the vault")
	}
	added, err := a.ImportTokens([]string{second, first, first})
	if err != nil || len(added) != 2 {
		t.Fatalf("complete batch failed: count=%d err=%v", len(added), err)
	}
	for _, token := range added {
		if token.Period != 30 || (token.Issuer == "OpenAI" && (token.Group != "OpenAI" || token.Digits != 6 || token.Algorithm != "SHA1")) || (token.Issuer == "Google" && (token.Digits != 8 || token.Algorithm != "SHA256")) {
			t.Fatal("migration token parameters were not preserved")
		}
	}
	before, err := os.ReadFile(filepath.Join(dir, "vault.dat"))
	if err != nil {
		t.Fatal(err)
	}
	unsupported := accounts[0]
	unsupported.kind = 1
	invalid := migrationFixtureURI(1, 0, 2026100806, unsupported)
	for _, uris := range [][]string{{second, first}, {testURI, invalid}, {testURI, first}} {
		if _, err := a.ImportTokens(uris); err == nil {
			t.Fatal("duplicate, unsupported or incomplete batch unexpectedly imported")
		}
		after, err := os.ReadFile(filepath.Join(dir, "vault.dat"))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("failed import changed the encrypted vault")
		}
		if tokens, err := a.GetTokens(); err != nil || len(tokens) != 2 {
			t.Fatal("failed import changed in-memory tokens")
		}
	}
	reopened := NewApp()
	reopened.init(dir)
	if tokens, err := reopened.GetTokens(); err != nil || len(tokens) != 2 {
		t.Fatal("migration tokens did not survive vault reopen")
	}
}

func TestMigrationVersionTwoImagePreview(t *testing.T) {
	uri := migrationFixtureURI(1, 0, 2026100801, migrationFixtureAccounts()...)
	parsed, _ := url.Parse(uri)
	payload, _ := base64.StdEncoding.DecodeString(parsed.Query().Get("data"))
	payload = bytes.Replace(payload, migrationFixtureVarint(nil, 2, 1), migrationFixtureVarint(nil, 2, 2), 1)
	uri = "otpauth-migration://offline?data=" + url.QueryEscape(base64.StdEncoding.EncodeToString(payload))
	previews, err := NewApp().PreviewImage(base64.StdEncoding.EncodeToString(pngBytes(t, qrImage(t, uri))))
	if err != nil || len(previews) != 2 {
		t.Fatalf("version 2 image preview failed: %v", err)
	}
}
