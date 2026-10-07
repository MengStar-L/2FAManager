package main

import (
	"bytes"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBackgroundPersistenceAndRange(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("vault uses Windows DPAPI")
	}
	dir := t.TempDir()
	a := NewApp()
	a.init(dir)
	if err := a.ready(); err != nil {
		t.Fatal(err)
	}
	picture := pngBytes(t, qrImage(t, "synthetic-background"))
	source := filepath.Join(t.TempDir(), "background.png")
	if err := os.WriteFile(source, picture, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := a.importBackground(source)
	if err != nil {
		t.Fatal(err)
	}
	if a.settings.BackgroundType != "pattern" {
		t.Fatal("selecting background committed without Save")
	}
	if _, err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	b := NewApp()
	b.init(dir)
	if err := b.ready(); err != nil {
		t.Fatal(err)
	}
	if b.settings.BackgroundURL != s.BackgroundURL {
		t.Fatal("background not persisted")
	}
	req := httptest.NewRequest("GET", s.BackgroundURL, nil)
	req.Header.Set("Range", "bytes=0-15")
	w := httptest.NewRecorder()
	b.mediaHandler().ServeHTTP(w, req)
	if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), picture[:16]) {
		t.Fatalf("bad range response %d", w.Code)
	}
	for _, path := range []string{"/media/../vault.dat", "/media/settings.json", "/media/bg-missing.png"} {
		w := httptest.NewRecorder()
		b.mediaHandler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatal("unexpected asset access")
		}
	}
	bad := s
	bad.BackgroundURL = "https://example.com/remote.png"
	if _, err := b.SaveSettings(bad); err == nil {
		t.Fatal("remote background accepted")
	}
	if b.settings != s {
		t.Fatal("failed save changed state")
	}
}

func TestSettingsCorruptionPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := []byte("broken-json")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSettings(dir); err == nil {
		t.Fatal("corrupt settings silently ignored")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if !bytes.Equal(got, original) {
		t.Fatal("corrupt original overwritten")
	}
}

func TestCorruptAppearanceDoesNotBlockVault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	a.init(dir)
	state, err := a.GetState()
	if err != nil || state.Warning == "" {
		t.Fatalf("vault blocked or warning missing: %v", err)
	}
	if _, err := a.GetTokens(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveSettings(defaultSettings()); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "settings-unreadable-*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal("original settings not backed up")
	}
	old, err := os.ReadFile(files[0])
	if err != nil || string(old) != "broken" {
		t.Fatal("backup content changed")
	}
	if _, err := loadSettings(dir); err != nil {
		t.Fatal(err)
	}
}

func TestAccentSettingsMigrationPersistenceAndValidation(t *testing.T) {
	dir := t.TempDir()
	legacy := []byte(`{"theme":"anime","closeToTray":false,"backgroundType":"pattern","pattern":"waves","opacity":0.35,"motion":false}`)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	a.init(dir)
	if a.settings.AccentColor != defaultSettings().AccentColor || a.settings.Theme != "anime" || a.settings.CloseToTray || a.settings.Motion || a.settings.Pattern != "waves" {
		t.Fatal("adding an accent preference changed existing settings")
	}
	if !a.settings.CheckUpdatesAutomatically || a.settings.UpdateAutomatically {
		t.Fatal("legacy settings must check for updates without enabling unattended installation")
	}
	for _, color := range []string{"#15806B", "#ffffff", "#000000", "#ffff00"} {
		s := a.settings
		s.AccentColor = color
		if _, err := a.SaveSettings(s); err != nil {
			t.Fatal(err)
		}
		restored, err := loadSettings(dir)
		if err != nil || restored != s {
			t.Fatalf("custom accent did not persist: %v", err)
		}
	}
	before, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	original := a.settings
	for _, color := range []string{"", "#abc", "red", "#ffffff00", "#gggggg", "1234567", "#12345\n", "var(--color)"} {
		s := original
		s.AccentColor = color
		if _, err := a.SaveSettings(s); err == nil {
			t.Fatalf("accepted invalid accent %q", color)
		}
		if a.settings != original {
			t.Fatal("invalid accent mutated current settings")
		}
	}
	after, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("invalid accent changed persisted settings")
	}
}

func TestAutomaticUpdatePreferencesPersist(t *testing.T) {
	a := NewApp()
	a.init(t.TempDir())
	s := a.settings
	s.CheckUpdatesAutomatically, s.UpdateAutomatically = false, true
	if _, err := a.SaveSettings(s); err == nil {
		t.Fatal("automatic installation accepted without update checks")
	}
	s.CheckUpdatesAutomatically = true
	if _, err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSettings(a.dataPath)
	if err != nil || !loaded.CheckUpdatesAutomatically || !loaded.UpdateAutomatically {
		t.Fatalf("update preferences did not persist: %v", err)
	}
}
