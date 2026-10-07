package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"LumaAuthenticator/internal/platform"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var mainWindowClass = fmt.Sprintf("LumaAuthenticatorWindow-%d", os.Getpid())

//go:embed build/windows/icon.ico
var trayIcon []byte

type trayHandle interface {
	Ready() bool
	Close()
}

type windowActions struct {
	capture func() (platform.WindowState, error)
	restore func(platform.WindowState) error
	show    func()
	hide    func()
	quit    func()
	error   func(string)
}

func (a *App) context() context.Context {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ctx
}

func (a *App) nativeWindowActions() windowActions {
	return windowActions{
		capture: func() (platform.WindowState, error) { return platform.CaptureWindow(mainWindowClass) },
		restore: func(s platform.WindowState) error { return platform.RestoreWindow(mainWindowClass, s) },
		show: func() {
			if ctx := a.context(); ctx != nil {
				minimized := runtime.WindowIsMinimised(ctx)
				placement, _ := platform.CaptureWindow(mainWindowClass)
				runtime.WindowShow(ctx)
				if minimized {
					runtime.WindowUnminimise(ctx)
					if placement.Maximized {
						runtime.WindowMaximise(ctx)
					}
				}
				platform.FocusWindow(mainWindowClass)
			}
		},
		hide: func() {
			if ctx := a.context(); ctx != nil {
				runtime.WindowHide(ctx)
			}
		},
		quit: func() {
			if ctx := a.context(); ctx != nil {
				runtime.Quit(ctx)
			}
		},
		error: func(message string) {
			if ctx := a.context(); ctx != nil {
				runtime.EventsEmit(ctx, "app:warning", message)
				platform.ShowWindowError(mainWindowClass, message)
			}
		},
	}
}

func (a *App) startTray() {
	tray, err := platform.NewTray(trayIcon, a.showMainWindow, a.QuitApp, a.trayUnavailable)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.trayWarning = "系统托盘不可用，关闭窗口时将正常退出：" + err.Error()
		return
	}
	a.tray = tray
}

func (a *App) trayUnavailable() {
	a.mu.Lock()
	a.trayWarning = "系统托盘暂时不可用，已恢复显示窗口。"
	a.mu.Unlock()
	a.showMainWindow()
	if ctx := a.context(); ctx != nil {
		runtime.EventsEmit(ctx, "app:warning", "系统托盘暂时不可用，已恢复显示窗口。")
	}
}

func (a *App) showMainWindow() { a.window.show() }

func (a *App) domReady(ctx context.Context) {
	select {
	case <-a.startupDone:
	case <-ctx.Done():
		return
	}
	a.domReadyOnce.Do(func() {
		_ = platform.SetWindowIcon(mainWindowClass, trayIcon)
		if err := platform.EnableRoundedWindow(mainWindowClass); err != nil {
			runtime.EventsEmit(ctx, "app:warning", "窗口圆角未能启用："+err.Error())
		}
		a.mu.RLock()
		saved := a.windowState
		a.mu.RUnlock()
		if saved.Valid {
			if err := a.window.restore(saved); err != nil {
				a.mu.Lock()
				a.sessionWarning = "窗口位置恢复失败，已使用默认位置：" + err.Error()
				a.mu.Unlock()
				runtime.EventsEmit(ctx, "app:warning", "窗口位置恢复失败，已使用默认位置。")
			}
		}
		a.mu.Lock()
		a.windowReady = true
		a.mu.Unlock()
		a.updates.start(ctx)
	})
}

func (a *App) saveCurrentWindow() error {
	a.mu.RLock()
	ready := a.windowReady
	a.mu.RUnlock()
	if !ready {
		return nil
	}
	placement, err := a.window.capture()
	if err != nil {
		return err
	}
	if err := platform.ValidateWindowState(placement); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.writeSessionLocked(a.session, placement); err != nil {
		a.sessionWarning = "窗口状态保存失败，令牌不受影响：" + err.Error()
		return err
	}
	a.windowState = placement
	return nil
}

// beforeClose handles the custom close button, Alt+F4 and native WM_CLOSE.
// Wails also invokes it for runtime.Quit; QuitApp sets an explicit exit flag.
func (a *App) beforeClose(context.Context) (prevent bool) {
	// Let an in-flight installer finish its READY handshake before exiting.
	// Its completion resumes this close without spinning the GUI message loop.
	if a.updates != nil && a.updates.deferCloseWhilePreparing() {
		return true
	}
	// WM_CLOSE can run on the GUI thread while a bridge/tray close is waiting
	// for that same thread. Never block the GUI thread on another close handler.
	if !a.closeMu.TryLock() {
		return true
	}
	defer func() {
		a.closeMu.Unlock()
		a.mu.RLock()
		quit := a.quitRequested
		a.mu.RUnlock()
		// An explicit quit that arrived during a hide/save must not be lost to
		// the reentry guard. Retry after releasing the active close handler.
		if prevent && quit {
			go a.window.quit()
		}
	}()
	err := a.saveCurrentWindow()
	a.mu.RLock()
	quit, closeToTray, tray := a.quitRequested, a.settings.CloseToTray, a.tray
	a.mu.RUnlock()
	if err != nil {
		a.window.show()
		a.window.error("未能保存界面状态，下次将使用上次保存的状态。令牌数据不受影响。\n" + err.Error())
		if !quit && closeToTray {
			return true
		}
	}
	if !quit && closeToTray {
		if tray == nil || !tray.Ready() {
			a.window.error("系统托盘不可用，应用将正常退出；令牌和已保存的界面状态不会丢失。")
			if err == nil {
				a.installAutomaticUpdateOnExit()
			}
			return false
		}
		a.window.hide()
		// Explorer can disappear between the initial readiness check and the
		// native Hide call. Re-check after hiding so its earlier recovery
		// callback cannot leave a hidden window without a reachable tray icon.
		if !tray.Ready() {
			a.window.show()
		}
		return true
	}
	if err == nil {
		a.installAutomaticUpdateOnExit()
	}
	return false
}

func (a *App) CloseWindow() { a.window.quit() }

func (a *App) QuitApp() {
	a.mu.Lock()
	a.quitRequested = true
	a.mu.Unlock()
	a.window.quit()
}

// A different configuration directory is an independent installation, which
// also keeps isolated native QA from interacting with a running personal vault.
func singleInstanceID() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "58f35a56-9a82-4f06-9069-21b9ae5e2fab"
	}
	// Keep the existing application mutex for the real Windows profile so an
	// older release cannot concurrently write the same vault during an upgrade.
	if normal, err := platform.DefaultConfigDirectory(); err == nil && strings.EqualFold(filepath.Clean(normal), filepath.Clean(dir)) {
		return "58f35a56-9a82-4f06-9069-21b9ae5e2fab"
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(dir))))
	return fmt.Sprintf("luma-%x", sum[:16])
}
