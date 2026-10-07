package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"LumaAuthenticator/internal/platform"
)

type fakeTray struct {
	ready  bool
	closed bool
}

func (t *fakeTray) Ready() bool { return t.ready }
func (t *fakeTray) Close()      { t.closed = true }

func testSessionApp(t *testing.T) *App {
	t.Helper()
	a := NewApp()
	a.init(t.TempDir())
	return a
}

func testPlacement() platform.WindowState {
	return platform.WindowState{X: -1260, Y: 70, Width: 1120, Height: 720, Maximized: true, Valid: true}
}

func TestSessionPersistenceAndIndependentWindowMerge(t *testing.T) {
	a := testSessionApp(t)
	a.windowReady = true
	a.window.capture = func() (platform.WindowState, error) { return testPlacement(), nil }
	state := SessionState{SidebarCollapsed: true, Filter: "group:工作", Search: "example"}
	if err := a.SaveSession(state); err != nil {
		t.Fatal(err)
	}
	if err := a.saveCurrentWindow(); err != nil {
		t.Fatal(err)
	}
	state.Search = "updated search"
	if err := a.SaveSession(state); err != nil {
		t.Fatal(err)
	}
	b := NewApp()
	b.init(a.dataPath)
	if b.session != state || b.windowState != testPlacement() {
		t.Fatal("window save and UI save overwrote one another")
	}
	public, err := b.GetState()
	if err != nil || public.Session != state {
		t.Fatalf("GetState did not restore session: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.dataPath, "vault.dat")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("session persistence unexpectedly wrote token storage")
	}
}

func TestConcurrentSessionAndWindowSavesPreserveBoth(t *testing.T) {
	a := testSessionApp(t)
	a.windowReady = true
	a.window.capture = func() (platform.WindowState, error) { return testPlacement(), nil }
	var wait sync.WaitGroup
	var failed atomic.Bool
	for i := 0; i < 10; i++ {
		wait.Add(2)
		go func() {
			defer wait.Done()
			if a.SaveSession(SessionState{SidebarCollapsed: true, Filter: "favorites", Search: "parallel"}) != nil {
				failed.Store(true)
			}
		}()
		go func() {
			defer wait.Done()
			if a.saveCurrentWindow() != nil {
				failed.Store(true)
			}
		}()
	}
	wait.Wait()
	if failed.Load() {
		t.Fatal("concurrent state save failed")
	}
	saved, err := loadSession(a.dataPath)
	if err != nil || saved.Window != testPlacement() || saved.Filter != "favorites" || saved.Search != "parallel" || !saved.SidebarCollapsed {
		t.Fatalf("concurrent save lost fields: %v", err)
	}
}

func TestCorruptSessionPreservedWithoutBlockingTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	broken := []byte("broken-session")
	if err := os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	a.init(dir)
	state, err := a.GetState()
	if err != nil || state.Warning == "" || state.Session != defaultSession() {
		t.Fatalf("bad session blocked vault/defaults: %v", err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, broken) {
		t.Fatal("unreadable session overwritten on load")
	}
	if err := a.SaveSession(SessionState{Filter: "all", SidebarCollapsed: true}); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "session-unreadable-*.json"))
	if len(backups) != 1 {
		t.Fatal("unreadable session not preserved")
	}
	if got, _ := os.ReadFile(backups[0]); !bytes.Equal(got, broken) {
		t.Fatal("session backup changed")
	}
}

func TestSessionValidationAndFailureDoesNotMutateMemory(t *testing.T) {
	a := testSessionApp(t)
	for _, state := range []SessionState{{Filter: "unknown"}, {Filter: "group:"}, {Filter: "group:" + strings.Repeat("界", 81)}, {Filter: "all", Search: strings.Repeat("x", 513)}, {Filter: "all", Search: "a\nb"}} {
		if err := a.SaveSession(state); err == nil {
			t.Fatal("invalid session accepted")
		}
	}
	if err := os.Mkdir(filepath.Join(a.dataPath, "session.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveSession(SessionState{Filter: "favorites"}); err == nil {
		t.Fatal("write failure hidden")
	}
	if a.session != defaultSession() || a.sessionWarning == "" {
		t.Fatal("failed write changed state or lost warning")
	}
}

func TestSessionRejectsMalformedWindowAndOversizeFiles(t *testing.T) {
	for _, data := range [][]byte{
		[]byte(`{"version":2,"filter":"all"}`),
		[]byte(`{"version":1,"filter":"all","window":{"valid":true,"width":-2,"height":800}}`),
		[]byte(`{"version":1,"filter":"all"} {}`),
		bytes.Repeat([]byte(" "), (32<<10)+1),
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "session.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadSession(dir); err == nil {
			t.Fatal("bad session file accepted")
		}
	}
}

func TestCloseModesSaveBeforeHidingAndNeverLoseAccess(t *testing.T) {
	for _, tc := range []struct {
		name        string
		tray        bool
		closeToTray bool
		quit        bool
		prevent     bool
		hides       int
		errors      int
	}{
		{"tray close", true, true, false, true, 1, 0},
		{"explicit quit", true, true, true, false, 0, 0},
		{"normal close", true, false, false, false, 0, 0},
		{"unavailable tray", false, true, false, false, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testSessionApp(t)
			a.windowReady, a.quitRequested, a.settings.CloseToTray = true, tc.quit, tc.closeToTray
			a.tray = &fakeTray{ready: tc.tray}
			hides, notices := 0, 0
			a.window.capture = func() (platform.WindowState, error) { return testPlacement(), nil }
			a.window.hide = func() {
				hides++
				if saved, err := loadSession(a.dataPath); err != nil || !saved.Window.Valid {
					t.Error("hidden before placement was saved")
				}
			}
			a.window.error = func(string) { notices++ }
			if prevent := a.beforeClose(context.Background()); prevent != tc.prevent || hides != tc.hides || notices != tc.errors {
				t.Fatal("incorrect close/tray behavior")
			}
		})
	}
}

func TestFailedWindowSaveIsVisibleAndDoesNotHide(t *testing.T) {
	a := testSessionApp(t)
	a.windowReady = true
	a.tray = &fakeTray{ready: true}
	a.window.capture = func() (platform.WindowState, error) { return testPlacement(), nil }
	if err := os.Mkdir(filepath.Join(a.dataPath, "session.json"), 0700); err != nil {
		t.Fatal(err)
	}
	shows, notices, hides := 0, 0, 0
	a.window.show = func() { shows++ }
	a.window.error = func(string) { notices++ }
	a.window.hide = func() { hides++ }
	if !a.beforeClose(context.Background()) || shows != 1 || notices != 1 || hides != 0 {
		t.Fatal("window disappeared after a failed session write")
	}
	if a.windowState.Valid {
		t.Fatal("failed save published an unsaved placement")
	}
	a.quitRequested = true
	if a.beforeClose(context.Background()) {
		t.Fatal("failed cosmetic save prevents explicit exit")
	}
}

func TestQuitAndShutdownReleaseTray(t *testing.T) {
	a := testSessionApp(t)
	tray := &fakeTray{ready: true}
	a.tray = tray
	quits := 0
	a.window.quit = func() { quits++ }
	a.QuitApp()
	if !a.quitRequested || quits != 1 {
		t.Fatal("explicit exit did not bypass tray close")
	}
	a.shutdown(context.Background())
	if !tray.closed || a.tray != nil {
		t.Fatal("tray not released on shutdown")
	}
}

func TestSettingsThemeAndTrayMigration(t *testing.T) {
	dir := t.TempDir()
	legacy := []byte(`{"backgroundType":"pattern","pattern":"dots","opacity":0.18,"motion":true}`)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := loadSettings(dir)
	if err != nil || s.Theme != "regular" || !s.CloseToTray {
		t.Fatalf("old preferences did not migrate safely: %v", err)
	}
	s.Theme, s.CloseToTray = "anime", false
	if err := writeSettings(dir, s); err != nil {
		t.Fatal(err)
	}
	restored, err := loadSettings(dir)
	if err != nil || restored.Theme != "anime" || restored.CloseToTray {
		t.Fatalf("explicit preference lost: %v", err)
	}
	for _, theme := range []string{"", "other", "ANIME"} {
		s.Theme = theme
		if validateSettings(s) == nil {
			t.Fatal("invalid theme accepted")
		}
	}
	encoded, _ := json.Marshal(defaultSettings())
	if !bytes.Contains(encoded, []byte(`"closeToTray":true`)) {
		t.Fatal("default tray preference missing from API")
	}
}

func TestCloseReentryDoesNotBlockAndExplicitQuitIsRetried(t *testing.T) {
	a := testSessionApp(t)
	a.tray = &fakeTray{ready: true}
	hiding, releaseHide, firstFinished, exited := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	a.window.hide = func() { close(hiding); <-releaseHide }
	a.window.quit = func() {
		calls.Add(1)
		if !a.beforeClose(context.Background()) {
			close(exited)
		}
	}
	go func() { a.beforeClose(context.Background()); close(firstFinished) }()
	<-hiding
	// This models a GUI-thread close arriving while a bridge close is in a
	// synchronous window operation. It must return without waiting on closeMu.
	returned := make(chan struct{})
	go func() { a.QuitApp(); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		close(releaseHide)
		t.Fatal("reentrant GUI close blocked on the active close handler")
	}
	close(releaseHide)
	<-firstFinished
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("explicit exit was lost during a close/hide")
	}
	if calls.Load() != 2 {
		t.Fatal("explicit quit was not retried exactly once")
	}
}

func TestTrayLostBetweenReadyCheckAndHideRestoresWindow(t *testing.T) {
	a := testSessionApp(t)
	tray := &fakeTray{ready: true}
	a.tray = tray
	shown := 0
	a.window.hide = func() { tray.ready = false }
	a.window.show = func() { shown++ }
	if !a.beforeClose(context.Background()) || shown != 1 {
		t.Fatal("tray loss during hide left the window inaccessible")
	}
}
