package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"LumaAuthenticator/internal/platform"
	"LumaAuthenticator/internal/updateinstall"
	"LumaAuthenticator/internal/updater"
)

type fakeUpdateService struct {
	check    func(context.Context) (*updater.Release, error)
	download func(context.Context, *updater.Release, string, func(updater.Progress)) (*updater.Download, error)
}

func (s *fakeUpdateService) Check(ctx context.Context) (*updater.Release, error) {
	if s.check == nil {
		return nil, nil
	}
	return s.check(ctx)
}

func (s *fakeUpdateService) Download(ctx context.Context, r *updater.Release, dir string, progress func(updater.Progress)) (*updater.Download, error) {
	if s.download == nil {
		return nil, errors.New("unexpected download")
	}
	return s.download(ctx, r, dir, progress)
}

func testUpdateApp(t *testing.T) (*App, *fakeUpdateService) {
	t.Helper()
	a := testSessionApp(t)
	fake := &fakeUpdateService{}
	a.updates.service = fake
	a.updates.readLastResult = func() (*updateinstall.Result, error) { return nil, nil }
	a.settings.CheckUpdatesAutomatically = false
	a.settings.UpdateAutomatically = false
	t.Cleanup(a.updates.stop)
	return a, fake
}

func availableUpdate() *updater.Release {
	return &updater.Release{Version: "0.4.0", AssetSize: 100, URL: "https://github.com/MengStar-L/2FAManager/releases/tag/v0.4.0"}
}

func fakeDownloadedUpdate(t *testing.T) *updater.Download {
	t.Helper()
	return &updater.Download{Path: filepath.Join(t.TempDir(), "staged.exe"), SHA256: strings.Repeat("a", 64), Version: "0.4.0"}
}

func waitUpdatePhase(t *testing.T, a *App, phase string) UpdateStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if status := a.GetUpdateStatus(); status.Phase == phase {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("wanted update phase %s, got %#v", phase, a.GetUpdateStatus())
	return UpdateStatus{}
}

func waitUpdateSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("update operation did not signal completion")
	}
}

func TestApplicationUpdateCheckDownloadAndStatus(t *testing.T) {
	a, service := testUpdateApp(t)
	release := availableUpdate()
	download := fakeDownloadedUpdate(t)
	service.check = func(context.Context) (*updater.Release, error) { return release, nil }
	service.download = func(ctx context.Context, got *updater.Release, dir string, progress func(updater.Progress)) (*updater.Download, error) {
		if got != release || dir != filepath.Join(a.dataPath, "updates", "downloads") {
			t.Errorf("incorrect download target %p %s", got, dir)
		}
		progress(updater.Progress{Downloaded: 60, Total: 100, Percent: 60})
		progress(updater.Progress{Downloaded: 100, Total: 100, Percent: 100})
		return download, nil
	}
	if status, err := a.CheckForUpdates(); err != nil || status.Phase != "checking" {
		t.Fatalf("check start: %#v %v", status, err)
	}
	status := waitUpdatePhase(t, a, "available")
	if status.LatestVersion != "0.4.0" || status.TotalBytes != 100 || status.LastChecked == "" || status.ReleaseURL != release.URL {
		t.Fatalf("incomplete release status %#v", status)
	}
	if status, err := a.DownloadUpdate(); err != nil || status.Phase != "downloading" {
		t.Fatalf("download start: %#v %v", status, err)
	}
	status = waitUpdatePhase(t, a, "ready")
	if status.Progress != 100 || status.DownloadedBytes != 100 || a.updates.download != download {
		t.Fatalf("download not ready: %#v", status)
	}
	if status, err := a.CheckForUpdates(); err != nil || status.Phase != "ready" {
		t.Fatalf("check discarded staged update: %#v %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(a.dataPath, "vault.dat")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("update operations changed token storage")
	}
}

func TestApplicationUpdateCheckFailuresAndRecovery(t *testing.T) {
	a, service := testUpdateApp(t)
	var checkNumber atomic.Int32
	service.check = func(context.Context) (*updater.Release, error) {
		if checkNumber.Add(1) == 1 {
			return nil, errors.New("network unavailable")
		}
		return nil, nil
	}
	if _, err := a.CheckForUpdates(); err != nil {
		t.Fatal(err)
	}
	if status := waitUpdatePhase(t, a, "error"); !strings.Contains(status.Message, "network unavailable") {
		t.Fatalf("lost error: %#v", status)
	}
	if _, err := a.CheckForUpdates(); err != nil {
		t.Fatal(err)
	}
	if status := waitUpdatePhase(t, a, "upToDate"); status.LatestVersion != applicationVersion() {
		t.Fatalf("incorrect current version: %#v", status)
	}
	if _, err := a.DownloadUpdate(); err == nil {
		t.Fatal("allowed download without a candidate")
	}
}

func TestApplicationUpdateConcurrentOperationsRejected(t *testing.T) {
	a, service := testUpdateApp(t)
	checking, checkContinue := make(chan struct{}), make(chan struct{})
	downloading, downloadContinue := make(chan struct{}), make(chan struct{})
	download := fakeDownloadedUpdate(t)
	service.check = func(ctx context.Context) (*updater.Release, error) {
		close(checking)
		select {
		case <-checkContinue:
			return availableUpdate(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	service.download = func(ctx context.Context, _ *updater.Release, _ string, _ func(updater.Progress)) (*updater.Download, error) {
		close(downloading)
		select {
		case <-downloadContinue:
			return download, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if _, err := a.CheckForUpdates(); err != nil {
		t.Fatal(err)
	}
	waitUpdateSignal(t, checking)
	if _, err := a.CheckForUpdates(); err == nil {
		t.Fatal("duplicate check accepted")
	}
	if _, err := a.DownloadUpdate(); err == nil {
		t.Fatal("download allowed during check")
	}
	close(checkContinue)
	waitUpdatePhase(t, a, "available")
	if _, err := a.DownloadUpdate(); err != nil {
		t.Fatal(err)
	}
	waitUpdateSignal(t, downloading)
	if _, err := a.CheckForUpdates(); err == nil {
		t.Fatal("check allowed during download")
	}
	if _, err := a.DownloadUpdate(); err == nil {
		t.Fatal("duplicate download accepted")
	}
	close(downloadContinue)
	waitUpdatePhase(t, a, "ready")
}

func TestApplicationUpdateAutomaticPreferences(t *testing.T) {
	for _, automaticDownload := range []bool{false, true} {
		t.Run(map[bool]string{false: "notify only", true: "download automatically"}[automaticDownload], func(t *testing.T) {
			a, service := testUpdateApp(t)
			var checks, downloads atomic.Int32
			download := fakeDownloadedUpdate(t)
			service.check = func(context.Context) (*updater.Release, error) { checks.Add(1); return availableUpdate(), nil }
			service.download = func(context.Context, *updater.Release, string, func(updater.Progress)) (*updater.Download, error) {
				downloads.Add(1)
				return download, nil
			}
			a.updates.runAutomatic()
			if checks.Load() != 0 {
				t.Fatal("automatic check ran while disabled")
			}
			a.mu.Lock()
			a.settings.CheckUpdatesAutomatically, a.settings.UpdateAutomatically = true, automaticDownload
			a.mu.Unlock()
			a.updates.runAutomatic()
			if automaticDownload {
				waitUpdatePhase(t, a, "ready")
			} else {
				waitUpdatePhase(t, a, "available")
			}
			if checks.Load() != 1 || (automaticDownload && downloads.Load() != 1) || (!automaticDownload && downloads.Load() != 0) {
				t.Fatalf("unexpected automatic actions checks=%d downloads=%d", checks.Load(), downloads.Load())
			}
		})
	}
}

func TestApplicationUpdateDisablingAutoWhileCheckingPreventsDownload(t *testing.T) {
	a, service := testUpdateApp(t)
	checking, continueCheck := make(chan struct{}), make(chan struct{})
	var downloads atomic.Int32
	service.check = func(ctx context.Context) (*updater.Release, error) {
		close(checking)
		select {
		case <-continueCheck:
			return availableUpdate(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	service.download = func(context.Context, *updater.Release, string, func(updater.Progress)) (*updater.Download, error) {
		downloads.Add(1)
		return nil, errors.New("must not download")
	}
	a.settings.CheckUpdatesAutomatically, a.settings.UpdateAutomatically = true, true
	a.updates.runAutomatic()
	waitUpdateSignal(t, checking)
	a.mu.Lock()
	a.settings.CheckUpdatesAutomatically, a.settings.UpdateAutomatically = false, false
	a.mu.Unlock()
	close(continueCheck)
	waitUpdatePhase(t, a, "available")
	// Wait for the completion goroutine to make its automatic-download decision.
	time.Sleep(10 * time.Millisecond)
	if downloads.Load() != 0 {
		t.Fatal("download ignored changed preferences")
	}
}

func TestApplicationUpdateShutdownCancelsAndPreventsFollowup(t *testing.T) {
	a, service := testUpdateApp(t)
	started, cancelled := make(chan struct{}), make(chan struct{})
	service.check = func(ctx context.Context) (*updater.Release, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}
	if _, err := a.CheckForUpdates(); err != nil {
		t.Fatal(err)
	}
	waitUpdateSignal(t, started)
	a.updates.stop()
	waitUpdateSignal(t, cancelled)
	if _, err := a.CheckForUpdates(); err == nil {
		t.Fatal("check accepted after shutdown")
	}
	if a.updates.eventContext() != nil {
		t.Fatal("shutdown can still emit runtime events")
	}
	if err := a.updates.prepareInstall(true); err == nil {
		t.Fatal("installation accepted after shutdown")
	}
}

func TestApplicationUpdateDownloadFailureCanRetry(t *testing.T) {
	for _, invalidResult := range []bool{false, true} {
		t.Run(map[bool]string{false: "download error", true: "missing result"}[invalidResult], func(t *testing.T) {
			a, service := testUpdateApp(t)
			service.check = func(context.Context) (*updater.Release, error) { return availableUpdate(), nil }
			service.download = func(context.Context, *updater.Release, string, func(updater.Progress)) (*updater.Download, error) {
				if invalidResult {
					return nil, nil
				}
				return nil, errors.New("checksum mismatch")
			}
			a.CheckForUpdates()
			waitUpdatePhase(t, a, "available")
			a.DownloadUpdate()
			waitUpdatePhase(t, a, "error")
			if a.updates.download != nil {
				t.Fatal("failed download became an installation candidate")
			}
			if _, err := a.DownloadUpdate(); err != nil {
				t.Fatalf("verified candidate could not be retried: %v", err)
			}
			waitUpdatePhase(t, a, "error")
			if _, err := a.CheckForUpdates(); err != nil {
				t.Fatal(err)
			}
			waitUpdatePhase(t, a, "available")
		})
	}
}

func TestApplicationUpdateNewCheckDoesNotRetainFailedCandidate(t *testing.T) {
	a, service := testUpdateApp(t)
	a.updates.release = availableUpdate()
	a.updates.status.Phase, a.updates.status.LatestVersion = "error", "0.4.0"
	service.check = func(context.Context) (*updater.Release, error) { return nil, errors.New("offline") }
	if _, err := a.CheckForUpdates(); err != nil {
		t.Fatal(err)
	}
	status := waitUpdatePhase(t, a, "error")
	if status.LatestVersion != "" || a.updates.release != nil {
		t.Fatal("new failed check retained old candidate")
	}
	if _, err := a.DownloadUpdate(); err == nil {
		t.Fatal("new failed check still permitted a stale download")
	}
}

func TestApplicationUpdateStopDiscardsUninstalledStage(t *testing.T) {
	a, _ := testUpdateApp(t)
	prepareTestDownload(t, a)
	a.updates.stop()
	if a.updates.download != nil {
		t.Fatal("shutdown retained an uninstalled download")
	}
}

func prepareTestDownload(t *testing.T, a *App) {
	t.Helper()
	a.updates.status.Phase = "ready"
	a.updates.download = fakeDownloadedUpdate(t)
	a.windowReady = true
	a.window.capture = func() (platform.WindowState, error) { return testPlacement(), nil }
}

func TestApplicationUpdateManualInstallSavesWindowBeforeRelaunch(t *testing.T) {
	a, _ := testUpdateApp(t)
	prepareTestDownload(t, a)
	var installed, quit atomic.Int32
	a.updates.install = func(ctx context.Context, path, hash string, relaunch bool) error {
		if !relaunch || path != a.updates.download.Path || hash != a.updates.download.SHA256 {
			t.Error("manual installation lost relaunch or verified download")
		}
		saved, err := loadSession(a.dataPath)
		if err != nil || saved.Window != testPlacement() {
			t.Errorf("window was not saved before installation: %v", err)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 31*time.Second {
			t.Error("installation preparation has no deadline")
		}
		installed.Add(1)
		return nil
	}
	a.window.quit = func() {
		if installed.Load() == 0 {
			t.Error("quit before installer READY")
		}
		quit.Add(1)
	}
	if err := a.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	if installed.Load() != 1 || quit.Load() != 1 || !a.quitRequested {
		t.Fatal("manual installation did not exit exactly once after preparation")
	}
	// The close hook must not schedule a second helper after manual preparation.
	a.settings.CheckUpdatesAutomatically, a.settings.UpdateAutomatically = true, true
	a.installAutomaticUpdateOnExit()
	if installed.Load() != 1 {
		t.Fatal("automatic exit prepared a duplicate installer")
	}
}

func TestApplicationUpdateInstallFailuresKeepCurrentApplication(t *testing.T) {
	for _, windowFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "helper cannot start", true: "window cannot save"}[windowFails], func(t *testing.T) {
			a, _ := testUpdateApp(t)
			prepareTestDownload(t, a)
			var installed, quit atomic.Int32
			if windowFails {
				a.window.capture = func() (platform.WindowState, error) { return platform.WindowState{}, errors.New("capture unavailable") }
			}
			a.window.quit = func() { quit.Add(1) }
			a.updates.install = func(context.Context, string, string, bool) error {
				installed.Add(1)
				return errors.New("directory is read only")
			}
			if err := a.InstallUpdate(); err == nil {
				t.Fatal("failed installation reported success")
			}
			if quit.Load() != 0 || a.quitRequested || a.updates.prepared || a.updates.installing {
				t.Fatal("failed preparation quit or stranded installer state")
			}
			if windowFails && installed.Load() != 0 {
				t.Fatal("installer started before failed window save")
			}
			if !windowFails && (installed.Load() != 1 || !strings.Contains(a.GetUpdateStatus().Message, "directory is read only")) {
				t.Fatal("installation failure was not retained")
			}
		})
	}
}

func TestApplicationUpdatePreparationDefersCloseAndRejectsDuplicates(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "failed"}[fail], func(t *testing.T) {
			a, _ := testUpdateApp(t)
			prepareTestDownload(t, a)
			entered, proceed, closed := make(chan struct{}), make(chan struct{}), make(chan struct{}, 2)
			a.window.quit = func() { closed <- struct{}{} }
			a.updates.install = func(context.Context, string, string, bool) error {
				close(entered)
				<-proceed
				if fail {
					return errors.New("unable to prepare")
				}
				return nil
			}
			done := make(chan error, 1)
			go func() { done <- a.updates.prepareInstall(true) }()
			waitUpdateSignal(t, entered)
			if err := a.updates.prepareInstall(true); err == nil {
				t.Fatal("concurrent installer was treated as ready")
			}
			if !a.updates.deferCloseWhilePreparing() {
				t.Fatal("close was allowed before READY")
			}
			select {
			case <-closed:
				t.Fatal("close ran during preparation")
			default:
			}
			close(proceed)
			if err := <-done; (fail && err == nil) || (!fail && err != nil) {
				t.Fatalf("prepare result: %v", err)
			}
			waitUpdateSignal(t, closed)
			if a.updates.deferCloseWhilePreparing() {
				t.Fatal("close still blocked after preparation")
			}
		})
	}
}

func TestApplicationUpdateAutomaticExitOnlyInstallsWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "manual", true: "automatic"}[enabled], func(t *testing.T) {
			a, _ := testUpdateApp(t)
			prepareTestDownload(t, a)
			a.settings.CheckUpdatesAutomatically, a.settings.UpdateAutomatically = true, enabled
			var installs atomic.Int32
			a.updates.install = func(_ context.Context, _, _ string, relaunch bool) error {
				if relaunch {
					t.Error("automatic exit must not relaunch")
				}
				installs.Add(1)
				return nil
			}
			a.tray = &fakeTray{ready: true}
			a.window.hide = func() {}
			if prevent := a.beforeClose(context.Background()); !prevent || installs.Load() != 0 {
				t.Fatal("closing to tray installed an update")
			}
			a.mu.Lock()
			a.quitRequested = true
			a.mu.Unlock()
			if prevent := a.beforeClose(context.Background()); prevent {
				t.Fatal("explicit exit blocked")
			}
			if (enabled && installs.Load() != 1) || (!enabled && installs.Load() != 0) {
				t.Fatalf("automatic exit ignored preference: %d", installs.Load())
			}
		})
	}
}

func TestApplicationUpdateResultNotificationDeduplicates(t *testing.T) {
	a, _ := testUpdateApp(t)
	result := &updateinstall.Result{Status: "installed", TargetPath: filepath.Join(t.TempDir(), "Luma.exe"), UpdatedAt: "2026-10-07T12:00:00Z"}
	a.updates.readLastResult = func() (*updateinstall.Result, error) { return result, nil }
	a.consumeUpdateNotice()
	if !strings.Contains(a.GetUpdateStatus().Message, applicationVersion()) {
		t.Fatal("successful installation was not announced")
	}
	a.updates.publish(func(s *UpdateStatus) { s.Message = "" })
	a.consumeUpdateNotice()
	if a.GetUpdateStatus().Message != "" {
		t.Fatal("same result was announced twice")
	}
	result.Status, result.UpdatedAt = "failed", "2026-10-07T13:00:00Z"
	a.consumeUpdateNotice()
	if !strings.Contains(a.GetUpdateStatus().Message, "当前版本仍可使用") {
		t.Fatal("failed installation not explained")
	}
	a.recordUpdateNotice("上次退出时无法准备更新")
	a.consumeUpdateNotice()
	if a.GetUpdateStatus().Message != "上次退出时无法准备更新" {
		t.Fatal("local preparation error not retained")
	}
	if _, err := os.Stat(filepath.Join(a.dataPath, "update-notice.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("consumed local notice was not removed")
	}
}

func TestApplicationUpdateRejectsCallsBeforeDataInitialization(t *testing.T) {
	a := NewApp()
	defer a.updates.stop()
	var calls atomic.Int32
	a.updates.service = &fakeUpdateService{check: func(context.Context) (*updater.Release, error) { calls.Add(1); return nil, nil }}
	if _, err := a.CheckForUpdates(); err == nil {
		t.Fatal("check accepted before initialization")
	}
	if _, err := a.DownloadUpdate(); err == nil {
		t.Fatal("download accepted before initialization")
	}
	if err := a.InstallUpdate(); err == nil {
		t.Fatal("install accepted before initialization")
	}
	if calls.Load() != 0 {
		t.Fatal("uninitialized application used update service")
	}
}
