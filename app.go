package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"LumaAuthenticator/internal/platform"
	"LumaAuthenticator/internal/vault"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx               context.Context
	mu                sync.RWMutex
	store             *vault.Store
	settings          Settings
	dataPath          string
	initErr           error
	settingsErr       error
	settingsWarning   string
	pendingBackground Settings
	session           SessionState
	windowState       platform.WindowState
	sessionErr        error
	sessionWarning    string
	trayWarning       string
	tray              trayHandle
	window            windowActions
	quitRequested     bool
	windowReady       bool
	startupDone       chan struct{}
	domReadyOnce      sync.Once
	closeMu           sync.Mutex
	updates           *applicationUpdater
}

type State struct {
	Tokens   []vault.Token `json:"tokens"`
	Settings Settings      `json:"settings"`
	DataPath string        `json:"dataPath"`
	Warning  string        `json:"warning,omitempty"`
	Session  SessionState  `json:"session"`
}

func NewApp() *App {
	a := &App{settings: defaultSettings(), session: defaultSession(), startupDone: make(chan struct{})}
	a.window = a.nativeWindowActions()
	a.updates = newApplicationUpdater(a)
	return a
}

func (a *App) shutdown(context.Context) {
	if a.updates != nil {
		a.updates.stop()
	}
	a.mu.Lock()
	a.removeUnusedMediaLocked(a.pendingBackground.BackgroundURL)
	tray := a.tray
	a.tray = nil
	a.mu.Unlock()
	if tray != nil {
		tray.Close()
	}
	platform.ReleaseWindowIcons()
}

func (a *App) startup(ctx context.Context) {
	defer close(a.startupDone)
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	dir, err := os.UserConfigDir()
	if err != nil {
		a.mu.Lock()
		a.initErr = fmt.Errorf("无法找到应用数据目录：%w", err)
		a.mu.Unlock()
		return
	}
	a.init(filepath.Join(dir, "LumaAuthenticator"))
	a.startTray()
}

func (a *App) init(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dataPath = dir
	if err := os.MkdirAll(dir, 0700); err != nil {
		a.initErr = err
		return
	}
	a.store, a.initErr = vault.Open(dir)
	a.settings, a.settingsErr = loadSettings(dir)
	if a.settingsErr != nil {
		a.settings = defaultSettings()
		a.settingsWarning = "外观设置无法读取，暂用默认外观；令牌不受影响，原设置文件已保留。"
	}
	var saved sessionDocument
	saved, a.sessionErr = loadSession(dir)
	a.session, a.windowState = saved.SessionState, saved.Window
	if a.sessionErr != nil {
		a.session, a.windowState = defaultSession(), platform.WindowState{}
		a.sessionWarning = "上次界面状态无法读取，暂用默认状态；令牌不受影响，原状态文件已保留。"
	}
}

func (a *App) ready() error {
	if a.initErr != nil {
		return fmt.Errorf("本地数据无法读取，原文件已保留：%w", a.initErr)
	}
	if a.store == nil {
		return errors.New("应用尚未准备好，请稍后重试")
	}
	return nil
}

func (a *App) GetState() (State, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.ready(); err != nil {
		return State{}, err
	}
	tokens, err := a.store.List()
	var warnings []string
	for _, warning := range []string{a.settingsWarning, a.sessionWarning, a.trayWarning} {
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	return State{Tokens: tokens, Settings: a.settings, Session: a.session, DataPath: a.dataPath, Warning: strings.Join(warnings, "\n")}, err
}

func (a *App) GetTokens() ([]vault.Token, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a.store.List()
}

func (a *App) AddToken(input vault.TokenInput) (vault.Token, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.ready(); err != nil {
		return vault.Token{}, err
	}
	return a.store.Add(input)
}

func (a *App) UpdateToken(id string, input vault.TokenInput) (vault.Token, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.ready(); err != nil {
		return vault.Token{}, err
	}
	return a.store.Update(id, input)
}

func (a *App) DeleteToken(id string) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.ready(); err != nil {
		return err
	}
	return a.store.Delete(id)
}

func (a *App) ToggleFavorite(id string) error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.ready(); err != nil {
		return err
	}
	return a.store.ToggleFavorite(id)
}

func (a *App) ImportTokens(uris []string) ([]vault.Token, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.ready(); err != nil {
		return nil, err
	}
	if len(uris) == 0 || len(uris) > 100 {
		return nil, errors.New("每次请选择 1–100 个令牌")
	}
	return a.store.ImportURIs(uris)
}

func (a *App) CopyCode(id string) error {
	tokens, err := a.GetTokens()
	if err != nil {
		return err
	}
	for _, token := range tokens {
		if token.ID == id {
			return runtime.ClipboardSetText(a.ctx, token.Code)
		}
	}
	return errors.New("令牌不存在")
}

func (a *App) PreviewClipboard() ([]ImportPreview, error) {
	data, err := platform.ReadClipboard()
	if err != nil {
		return nil, err
	}
	if len(data.Image) > 0 {
		previews, imageErr := previewImageBytes(data.Image)
		if imageErr == nil {
			return previews, nil
		}
		if data.Text == "" {
			return nil, imageErr
		}
	}
	if data.Text != "" {
		return a.PreviewText(data.Text)
	}
	return nil, errors.New("剪贴板里没有二维码图片或 otpauth 链接，请先复制截图")
}
