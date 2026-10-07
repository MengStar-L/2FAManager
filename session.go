package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"LumaAuthenticator/internal/platform"
)

// SessionState contains presentation state only. Token data is stored separately.
type SessionState struct {
	SidebarCollapsed bool   `json:"sidebarCollapsed"`
	Filter           string `json:"filter"`
	Search           string `json:"search"`
}

type sessionDocument struct {
	Version int `json:"version"`
	SessionState
	Window platform.WindowState `json:"window"`
}

func defaultSession() SessionState { return SessionState{Filter: "all"} }

func validateSession(s SessionState) error {
	if !utf8.ValidString(s.Filter) || !utf8.ValidString(s.Search) || strings.ContainsFunc(s.Filter, unicode.IsControl) || strings.ContainsFunc(s.Search, unicode.IsControl) {
		return errors.New("界面状态包含无效字符")
	}
	if s.Filter != "all" && s.Filter != "favorites" {
		group, ok := strings.CutPrefix(s.Filter, "group:")
		if !ok || group == "" || len([]rune(group)) > 80 {
			return errors.New("令牌筛选状态无效")
		}
	}
	if len([]rune(s.Search)) > 512 {
		return errors.New("搜索文字过长")
	}
	return nil
}

func loadSession(dir string) (sessionDocument, error) {
	saved := sessionDocument{Version: 1, SessionState: defaultSession()}
	f, err := os.Open(filepath.Join(dir, "session.json"))
	if errors.Is(err, os.ErrNotExist) {
		return saved, nil
	}
	if err != nil {
		return saved, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (32<<10)+1))
	if err != nil || len(data) > 32<<10 {
		return saved, errors.New("界面状态文件无法读取或过大")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&saved); err != nil || decoder.Decode(new(any)) != io.EOF || saved.Version != 1 {
		return saved, errors.New("界面状态文件损坏")
	}
	if err := validateSession(saved.SessionState); err != nil {
		return saved, err
	}
	if err := platform.ValidateWindowState(saved.Window); err != nil {
		return saved, err
	}
	return saved, nil
}

// SaveSession merges frontend state with the latest native window placement
// under the same mutex used by close-time capture; neither writer loses fields.
func (a *App) SaveSession(s SessionState) error {
	if err := validateSession(s); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dataPath == "" {
		return errors.New("应用尚未准备好，请稍后重试")
	}
	if err := a.writeSessionLocked(s, a.windowState); err != nil {
		a.sessionWarning = "界面状态保存失败，令牌不受影响：" + err.Error()
		return errors.New(a.sessionWarning)
	}
	a.session = s
	return nil
}

func (a *App) writeSessionLocked(s SessionState, placement platform.WindowState) error {
	if a.sessionErr != nil {
		old := filepath.Join(a.dataPath, "session.json")
		backup := filepath.Join(a.dataPath, fmt.Sprintf("session-unreadable-%d.json", time.Now().UnixNano()))
		if err := os.Rename(old, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("无法保留原界面状态：%w", err)
		}
		a.sessionErr = nil
	}
	data, err := json.MarshalIndent(sessionDocument{Version: 1, SessionState: s, Window: placement}, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(a.dataPath, ".session-*")
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
	if err := os.Rename(tmp, filepath.Join(a.dataPath, "session.json")); err != nil {
		return err
	}
	a.sessionWarning = ""
	return nil
}
