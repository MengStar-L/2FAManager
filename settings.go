package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type Settings struct {
	Theme                     string  `json:"theme"`
	AccentColor               string  `json:"accentColor"`
	CloseToTray               bool    `json:"closeToTray"`
	BackgroundType            string  `json:"backgroundType"`
	Pattern                   string  `json:"pattern"`
	BackgroundURL             string  `json:"backgroundUrl"`
	BackgroundName            string  `json:"backgroundName"`
	Opacity                   float64 `json:"opacity"`
	Motion                    bool    `json:"motion"`
	CheckUpdatesAutomatically bool    `json:"checkUpdatesAutomatically"`
	UpdateAutomatically       bool    `json:"updateAutomatically"`
}

func defaultSettings() Settings {
	return Settings{Theme: "regular", AccentColor: "#8773b7", CloseToTray: true, BackgroundType: "pattern", Pattern: "dots", Opacity: .18, Motion: true, CheckUpdatesAutomatically: true}
}

func validateSettings(s Settings) error {
	if s.UpdateAutomatically && !s.CheckUpdatesAutomatically {
		return errors.New("自动更新需要开启自动检查更新")
	}
	if len(s.AccentColor) != 7 || s.AccentColor[0] != '#' {
		return errors.New("点缀色需要使用 #RRGGBB 格式")
	}
	if _, err := hex.DecodeString(s.AccentColor[1:]); err != nil {
		return errors.New("点缀色需要使用 #RRGGBB 格式")
	}
	if s.Theme != "regular" && s.Theme != "anime" {
		return errors.New("主题无效")
	}
	if s.BackgroundType != "pattern" && s.BackgroundType != "image" && s.BackgroundType != "video" {
		return errors.New("背景类型无效")
	}
	if s.Pattern != "dots" && s.Pattern != "grid" && s.Pattern != "waves" {
		return errors.New("背景纹理无效")
	}
	if math.IsNaN(s.Opacity) || math.IsInf(s.Opacity, 0) || s.Opacity < 0 || s.Opacity > 1 {
		return errors.New("背景透明度必须在 0–100% 之间")
	}
	if s.BackgroundURL != "" && !validMediaURL(s.BackgroundURL) {
		return errors.New("背景文件地址无效")
	}
	if s.BackgroundType != "pattern" && s.BackgroundURL == "" {
		return errors.New("请先选择背景文件")
	}
	if len(s.BackgroundName) > 1024 {
		return errors.New("背景文件名过长")
	}
	return nil
}

func validMediaURL(url string) bool {
	if !strings.HasPrefix(url, "/media/bg-") {
		return false
	}
	name := strings.TrimPrefix(url, "/media/")
	return name == filepath.Base(name) && !strings.ContainsAny(name, "\\/?%:") && mediaKind(filepath.Ext(name)) != ""
}

func loadSettings(dir string) (Settings, error) {
	s := defaultSettings()
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, errors.New("外观设置文件损坏")
	}
	if err := validateSettings(s); err != nil {
		return s, err
	}
	if s.BackgroundURL != "" {
		if _, err := os.Stat(filepath.Join(dir, filepath.Base(s.BackgroundURL))); errors.Is(err, os.ErrNotExist) {
			s.BackgroundType, s.BackgroundURL, s.BackgroundName = "pattern", "", ""
		}
	}
	return s, nil
}

func writeSettings(dir string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".settings-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "settings.json"))
}

func (a *App) SaveSettings(s Settings) (Settings, error) {
	a.mu.Lock()
	preferencesChanged := false
	defer func() {
		a.mu.Unlock()
		if preferencesChanged && a.updates != nil {
			a.updates.preferencesChanged()
		}
	}()
	if err := a.ready(); err != nil {
		return a.settings, err
	}
	if err := validateSettings(s); err != nil {
		return a.settings, err
	}
	if s.BackgroundURL != "" && s.BackgroundURL != a.settings.BackgroundURL && s.BackgroundURL != a.pendingBackground.BackgroundURL {
		return a.settings, errors.New("请通过文件选择器导入背景")
	}
	if s.BackgroundURL != "" && mediaKind(filepath.Ext(s.BackgroundURL)) != s.BackgroundType && s.BackgroundType != "pattern" {
		return a.settings, errors.New("背景媒体类型不匹配")
	}
	if err := a.writeSettingsLocked(s); err != nil {
		return a.settings, fmt.Errorf("保存外观失败：%w", err)
	}
	old, pending := a.settings.BackgroundURL, a.pendingBackground.BackgroundURL
	preferencesChanged = a.settings.CheckUpdatesAutomatically != s.CheckUpdatesAutomatically || a.settings.UpdateAutomatically != s.UpdateAutomatically
	a.settings = s
	a.pendingBackground = Settings{}
	a.removeUnusedMediaLocked(old)
	a.removeUnusedMediaLocked(pending)
	return s, nil
}

func (a *App) removeUnusedMediaLocked(url string) {
	if url != "" && url != a.settings.BackgroundURL && validMediaURL(url) {
		_ = os.Remove(filepath.Join(a.dataPath, filepath.Base(url)))
	}
}

// Preserve unreadable appearance preferences before a user explicitly saves new ones.
// Cosmetic corruption must never prevent access to the token vault.
func (a *App) writeSettingsLocked(s Settings) error {
	if a.settingsErr != nil {
		path := filepath.Join(a.dataPath, "settings.json")
		backup := filepath.Join(a.dataPath, fmt.Sprintf("settings-unreadable-%d.json", time.Now().UnixNano()))
		if err := os.Rename(path, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("无法保留原外观设置：%w", err)
		}
		a.settingsErr = nil
	}
	if err := writeSettings(a.dataPath, s); err != nil {
		return err
	}
	a.settingsWarning = ""
	return nil
}

func mediaKind(ext string) string {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return "image"
	case ".mp4", ".webm":
		return "video"
	default:
		return ""
	}
}

func (a *App) PickBackground() (*Settings, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择图片或视频背景", Filters: []runtime.FileFilter{{DisplayName: "图片和视频", Pattern: "*.png;*.jpg;*.jpeg;*.webp;*.gif;*.mp4;*.webm"}}})
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}
	s, err := a.importBackground(path)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (a *App) importBackground(path string) (Settings, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ready(); err != nil {
		return a.settings, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	kind := mediaKind(ext)
	if kind == "" {
		return a.settings, errors.New("请选择 PNG、JPEG、WebP、GIF、MP4 或 WebM 文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return a.settings, errors.New("无法打开背景文件")
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() == 0 || stat.Size() > 100<<20 {
		return a.settings, errors.New("背景文件必须是 100 MB 以内的普通文件")
	}
	if kind == "image" {
		cfg, _, err := image.DecodeConfig(f)
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 50_000_000 {
			return a.settings, errors.New("图片无效或分辨率过大")
		}
	} else {
		header := make([]byte, 32)
		n, _ := f.Read(header)
		if (ext == ".mp4" && (n < 12 || string(header[4:8]) != "ftyp")) || (ext == ".webm" && (n < 4 || string(header[:4]) != "\x1a\x45\xdf\xa3")) {
			return a.settings, errors.New("视频文件格式无效")
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return a.settings, err
	}
	name := fmt.Sprintf("bg-%d%s", time.Now().UnixNano(), ext)
	dest := filepath.Join(a.dataPath, name)
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return a.settings, err
	}
	written, copyErr := io.Copy(out, io.LimitReader(f, (100<<20)+1))
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || written > 100<<20 {
		os.Remove(dest)
		return a.settings, errors.New("复制背景失败")
	}
	s := a.settings
	s.BackgroundType, s.BackgroundURL, s.BackgroundName = kind, "/media/"+name, filepath.Base(path)
	// Selection previews the copied media. The user's Save action commits it.
	a.removeUnusedMediaLocked(a.pendingBackground.BackgroundURL)
	a.pendingBackground = s
	return s, nil
}

func (a *App) mediaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.RLock()
		url, pending, dir := a.settings.BackgroundURL, a.pendingBackground.BackgroundURL, a.dataPath
		a.mu.RUnlock()
		if r.URL.Path == pending && pending != "" {
			url = pending
		}
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || url == "" || r.URL.Path != url || !validMediaURL(url) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(filepath.Join(dir, filepath.Base(url)))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)
	})
}
