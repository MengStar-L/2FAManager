package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"LumaAuthenticator/internal/updateinstall"
	"LumaAuthenticator/internal/updater"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type UpdateStatus struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	Phase           string `json:"phase"`
	Progress        int    `json:"progress"`
	ReleaseURL      string `json:"releaseUrl"`
	Message         string `json:"message"`
	LastChecked     string `json:"lastChecked"`
	DownloadedBytes int64  `json:"downloadedBytes"`
	TotalBytes      int64  `json:"totalBytes"`
}

type updateService interface {
	Check(context.Context) (*updater.Release, error)
	Download(context.Context, *updater.Release, string, func(updater.Progress)) (*updater.Download, error)
}

type applicationUpdater struct {
	mu             sync.Mutex
	app            *App
	service        updateService
	status         UpdateStatus
	release        *updater.Release
	download       *updater.Download
	ctx            context.Context
	cancel         context.CancelFunc
	wake           chan struct{}
	started        bool
	stopped        bool
	installing     bool
	prepared       bool
	deferredClose  bool
	install        func(context.Context, string, string, bool) error
	readLastResult func() (*updateinstall.Result, error)
}

func newApplicationUpdater(a *App) *applicationUpdater {
	owner, repo := updateRepository()
	ctx, cancel := context.WithCancel(context.Background())
	u := &applicationUpdater{
		app: a, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1),
		status: UpdateStatus{CurrentVersion: applicationVersion(), Phase: "idle", ReleaseURL: "https://github.com/" + owner + "/" + repo + "/releases/latest"},
		install: func(ctx context.Context, path, digest string, relaunch bool) error {
			_, err := updateinstall.Start(ctx, path, digest, relaunch)
			return err
		},
		readLastResult: updateinstall.ReadLastResult,
	}
	service, err := updater.New(updater.Config{Owner: owner, Repo: repo, CurrentVersion: applicationVersion()})
	if err != nil {
		u.status.Phase, u.status.Message = "error", "更新服务配置无效："+err.Error()
	} else {
		u.service = service
	}
	return u
}

func (u *applicationUpdater) snapshot() UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

func (u *applicationUpdater) publish(change func(*UpdateStatus)) UpdateStatus {
	u.mu.Lock()
	change(&u.status)
	s := u.status
	u.mu.Unlock()
	u.emit(s)
	return s
}

func (u *applicationUpdater) emit(s UpdateStatus) {
	if ctx := u.eventContext(); ctx != nil {
		runtime.EventsEmit(ctx, "app:update", s)
	}
}

func (u *applicationUpdater) eventContext() context.Context {
	u.mu.Lock()
	stopped, loopContext := u.stopped, u.ctx
	u.mu.Unlock()
	if stopped || (loopContext != nil && loopContext.Err() != nil) {
		return nil
	}
	ctx := u.app.context()
	if ctx == nil || ctx.Err() != nil {
		return nil
	}
	return ctx
}

func (u *applicationUpdater) applicationReady() (string, error) {
	u.app.mu.RLock()
	defer u.app.mu.RUnlock()
	if err := u.app.ready(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(u.app.dataPath) {
		return "", errors.New("应用数据目录尚未准备好")
	}
	return u.app.dataPath, nil
}

func (u *applicationUpdater) preferences() Settings {
	u.app.mu.RLock()
	defer u.app.mu.RUnlock()
	return u.app.settings
}

func (u *applicationUpdater) start(ctx context.Context) {
	u.mu.Lock()
	if u.started || u.stopped {
		u.mu.Unlock()
		return
	}
	u.started = true
	loopContext := u.ctx
	u.mu.Unlock()
	u.app.consumeUpdateNotice()
	go func() {
		initial := time.NewTimer(5 * time.Second)
		defer initial.Stop()
		periodic := time.NewTicker(6 * time.Hour)
		defer periodic.Stop()
		for {
			select {
			case <-ctx.Done():
				u.stop()
				return
			case <-loopContext.Done():
				return
			case <-initial.C:
			case <-periodic.C:
			case <-u.wake:
			}
			u.runAutomatic()
		}
	}()
}

func (u *applicationUpdater) runAutomatic() {
	s := u.preferences()
	if !s.CheckUpdatesAutomatically {
		return
	}
	status := u.snapshot()
	if s.UpdateAutomatically && status.Phase == "available" {
		_, _ = u.beginDownloadWithMode(true)
	} else if status.Phase != "ready" {
		_, _ = u.beginCheckWithMode(true)
	}
}

func (u *applicationUpdater) stop() {
	u.mu.Lock()
	u.stopped = true
	cancel := u.cancel
	var discard *updater.Download
	if !u.installing && !u.prepared {
		discard, u.download = u.download, nil
	}
	u.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if discard != nil {
		_ = discard.Cleanup()
	}
}

func (u *applicationUpdater) preferencesChanged() {
	select {
	case u.wake <- struct{}{}:
	default:
	}
}

func (u *applicationUpdater) beginCheck() (UpdateStatus, error) {
	return u.beginCheckWithMode(false)
}

func (u *applicationUpdater) beginCheckWithMode(automatic bool) (UpdateStatus, error) {
	if _, err := u.applicationReady(); err != nil {
		return u.snapshot(), err
	}
	u.mu.Lock()
	if u.stopped || u.ctx.Err() != nil {
		s := u.status
		u.mu.Unlock()
		return s, errors.New("应用正在退出")
	}
	if automatic && !u.preferences().CheckUpdatesAutomatically {
		s := u.status
		u.mu.Unlock()
		return s, nil
	}
	if u.service == nil {
		s := u.status
		u.mu.Unlock()
		return s, errors.New(s.Message)
	}
	if u.status.Phase == "checking" || u.status.Phase == "downloading" || u.installing {
		s := u.status
		u.mu.Unlock()
		return s, errors.New("更新操作正在进行中")
	}
	if u.status.Phase == "ready" {
		s := u.status
		u.mu.Unlock()
		return s, nil
	}
	u.release = nil
	u.status.LatestVersion = ""
	u.status.Phase, u.status.Message, u.status.Progress = "checking", "正在检查更新…", 0
	u.status.DownloadedBytes, u.status.TotalBytes = 0, 0
	s, ctx := u.status, u.ctx
	u.mu.Unlock()
	u.emit(s)
	go func() {
		checkContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		release, err := u.service.Check(checkContext)
		u.mu.Lock()
		if u.stopped || ctx.Err() != nil {
			u.mu.Unlock()
			return
		}
		u.status.LastChecked = time.Now().UTC().Format(time.RFC3339)
		if err != nil {
			u.status.Phase, u.status.Message = "error", "检查更新失败："+err.Error()
		} else if release == nil {
			u.release = nil
			u.status.Phase, u.status.LatestVersion, u.status.Message = "upToDate", u.status.CurrentVersion, "已是最新版本"
		} else {
			u.release = release
			u.status.Phase, u.status.LatestVersion, u.status.ReleaseURL = "available", release.Version, release.URL
			u.status.Message = "发现新版本 " + release.Version
			u.status.TotalBytes = release.AssetSize
		}
		result := u.status
		u.mu.Unlock()
		u.emit(result)
		if err == nil && release != nil {
			_, _ = u.beginDownloadWithMode(true)
		}
	}()
	return s, nil
}

func (u *applicationUpdater) beginDownload() (UpdateStatus, error) {
	return u.beginDownloadWithMode(false)
}

func (u *applicationUpdater) beginDownloadWithMode(automatic bool) (UpdateStatus, error) {
	dir, err := u.applicationReady()
	if err != nil {
		return u.snapshot(), err
	}
	u.mu.Lock()
	if u.stopped || u.ctx.Err() != nil {
		s := u.status
		u.mu.Unlock()
		return s, errors.New("应用正在退出")
	}
	if automatic {
		settings := u.preferences()
		if !settings.CheckUpdatesAutomatically || !settings.UpdateAutomatically {
			s := u.status
			u.mu.Unlock()
			return s, nil
		}
	}
	if u.status.Phase == "ready" {
		s := u.status
		u.mu.Unlock()
		return s, nil
	}
	if (u.status.Phase != "available" && u.status.Phase != "error") || u.release == nil || u.installing {
		s := u.status
		u.mu.Unlock()
		return s, errors.New("请先检查更新，确认有可下载的版本")
	}
	release, ctx := u.release, u.ctx
	u.status.Phase, u.status.Progress, u.status.Message = "downloading", 0, "正在下载更新…"
	u.status.DownloadedBytes, u.status.TotalBytes = 0, release.AssetSize
	s := u.status
	u.mu.Unlock()
	u.emit(s)
	staging := filepath.Join(dir, "updates", "downloads")
	go func() {
		downloadContext, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		lastProgress := time.Time{}
		download, err := u.service.Download(downloadContext, release, staging, func(p updater.Progress) {
			if time.Since(lastProgress) < 150*time.Millisecond && p.Percent < 100 {
				return
			}
			lastProgress = time.Now()
			u.mu.Lock()
			if u.stopped || ctx.Err() != nil || u.status.Phase != "downloading" {
				u.mu.Unlock()
				return
			}
			u.status.Progress, u.status.DownloadedBytes, u.status.TotalBytes = p.Percent, p.Downloaded, p.Total
			s := u.status
			u.mu.Unlock()
			u.emit(s)
		})
		if err == nil && (download == nil || download.Path == "" || download.SHA256 == "") {
			err = errors.New("下载服务未返回已校验的更新文件")
		}
		if err != nil && download != nil {
			_ = download.Cleanup()
		}
		u.mu.Lock()
		if u.stopped || ctx.Err() != nil {
			u.mu.Unlock()
			if download != nil {
				_ = download.Cleanup()
			}
			return
		}
		if err != nil {
			u.status.Phase, u.status.Message = "error", "下载更新失败："+err.Error()
		} else {
			u.download = download
			u.status.Phase, u.status.Progress, u.status.Message = "ready", 100, "更新已下载并校验完成"
		}
		result := u.status
		u.mu.Unlock()
		u.emit(result)
	}()
	return s, nil
}

func (u *applicationUpdater) prepareInstall(relaunch bool) error {
	if _, err := u.applicationReady(); err != nil {
		return err
	}
	u.mu.Lock()
	if u.stopped || u.ctx.Err() != nil {
		u.mu.Unlock()
		return errors.New("应用正在退出")
	}
	if u.prepared {
		u.mu.Unlock()
		return nil
	}
	if u.installing {
		u.mu.Unlock()
		return errors.New("正在准备安装更新，请稍候")
	}
	if u.status.Phase != "ready" || u.download == nil {
		u.mu.Unlock()
		return errors.New("请先下载更新")
	}
	download := *u.download
	u.installing = true
	u.mu.Unlock()
	// This deadline only covers preparation. Once the helper acknowledges READY,
	// the main process exits; cancelling the update loop must not cancel installation.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := u.install(ctx, download.Path, download.SHA256, relaunch)
	u.mu.Lock()
	u.installing, u.prepared = false, err == nil
	stopped := u.stopped
	deferredClose := u.deferredClose
	u.deferredClose = false
	if err != nil {
		u.status.Message = "安装更新失败：" + err.Error()
	}
	s := u.status
	u.mu.Unlock()
	if err == nil || stopped {
		_ = download.Cleanup()
	}
	if err != nil {
		u.emit(s)
	}
	if deferredClose {
		go u.app.window.quit()
	}
	return err
}

// beforeClose calls this before taking its own reentry lock. A tray/native close
// must not end the process until the helper has acknowledged its prepared plan.
func (u *applicationUpdater) deferCloseWhilePreparing() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.installing {
		return false
	}
	u.deferredClose = true
	return true
}

func (a *App) GetUpdateStatus() UpdateStatus          { return a.updates.snapshot() }
func (a *App) CheckForUpdates() (UpdateStatus, error) { return a.updates.beginCheck() }
func (a *App) DownloadUpdate() (UpdateStatus, error)  { return a.updates.beginDownload() }

func (a *App) InstallUpdate() error {
	if _, err := a.updates.applicationReady(); err != nil {
		return err
	}
	if err := a.saveCurrentWindow(); err != nil {
		return fmt.Errorf("无法保存当前窗口状态：%w", err)
	}
	if err := a.updates.prepareInstall(true); err != nil {
		return err
	}
	a.QuitApp()
	return nil
}

func (a *App) OpenReleasePage() {
	owner, repo := updateRepository()
	if ctx := a.context(); ctx != nil {
		runtime.BrowserOpenURL(ctx, "https://github.com/"+owner+"/"+repo+"/releases/latest")
	}
}

func (a *App) installAutomaticUpdateOnExit() {
	if a.updates == nil || !a.updates.preferences().UpdateAutomatically || a.updates.snapshot().Phase != "ready" {
		return
	}
	if err := a.updates.prepareInstall(false); err != nil {
		a.recordUpdateNotice("上次退出时未能安装更新：" + err.Error() + "。可在设置中重试，令牌数据不受影响。")
	}
}

func (a *App) recordUpdateNotice(message string) {
	a.mu.RLock()
	dir := a.dataPath
	a.mu.RUnlock()
	if !filepath.IsAbs(dir) {
		return
	}
	data, _ := json.Marshal(struct{ Message string }{message})
	_ = os.WriteFile(filepath.Join(dir, "update-notice.json"), data, 0600)
}

func (a *App) consumeUpdateNotice() {
	a.mu.RLock()
	dir := a.dataPath
	a.mu.RUnlock()
	if !filepath.IsAbs(dir) {
		return
	}
	message := ""
	path := filepath.Join(dir, "update-notice.json")
	if data, err := os.ReadFile(path); err == nil && len(data) <= 16*1024 {
		var notice struct{ Message string }
		if json.Unmarshal(data, &notice) == nil && notice.Message != "" {
			message = notice.Message
		}
		_ = os.Remove(path)
	}
	// Helper results are scoped to the current portable executable. Remember the
	// exact result so reopening the same version does not repeat its notification.
	result, err := a.updates.readLastResult()
	if err == nil && result != nil {
		type seenResult struct {
			TargetPath string
			UpdatedAt  string
		}
		seenPath := filepath.Join(dir, "update-result-seen.json")
		var seen seenResult
		if data, err := os.ReadFile(seenPath); err == nil && len(data) <= 16*1024 {
			_ = json.Unmarshal(data, &seen)
		}
		if seen.TargetPath != result.TargetPath || seen.UpdatedAt != result.UpdatedAt {
			if message == "" {
				switch result.Status {
				case "installed":
					message = "已更新至 " + applicationVersion()
				case "failed":
					message = "上次更新未完成，当前版本仍可使用，令牌数据未改动。可在设置中重试。"
				}
			}
			data, _ := json.Marshal(seenResult{TargetPath: result.TargetPath, UpdatedAt: result.UpdatedAt})
			_ = os.WriteFile(seenPath, data, 0600)
		}
	}
	if message != "" {
		a.updates.publish(func(s *UpdateStatus) { s.Message = message })
		if ctx := a.updates.eventContext(); ctx != nil {
			runtime.EventsEmit(ctx, "app:warning", message)
		}
	}
}
