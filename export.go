package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"LumaAuthenticator/internal/vault"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const tokenExportLifetime = 15 * time.Minute

type TokenExport struct {
	ExportID    string                   `json:"exportId"`
	Credentials []vault.ExportCredential `json:"credentials"`
	Text        string                   `json:"text"`
}

type tokenExportSession struct {
	id       string
	snapshot *vault.ExportSnapshot
	expires  time.Time
	exported bool
}

// PreviewTokenExport intentionally exposes only explicitly selected credentials.
// Its short-lived snapshot is retained in memory, never persisted in settings.
func (a *App) PreviewTokenExport(ids []string) (TokenExport, error) {
	a.exportMu.Lock()
	defer a.exportMu.Unlock()
	// Even an invalid replacement request discards the preceding selection.
	a.tokenExport = nil
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.ready(); err != nil {
		return TokenExport{}, err
	}
	snapshot, err := a.store.ExportSelection(ids)
	if err != nil {
		return TokenExport{}, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return TokenExport{}, errors.New("无法创建凭证预览，请重试")
	}
	id := hex.EncodeToString(nonce[:])
	a.tokenExport = &tokenExportSession{id: id, snapshot: snapshot, expires: time.Now().Add(tokenExportLifetime)}
	return TokenExport{ExportID: id, Credentials: snapshot.Credentials(), Text: snapshot.Text()}, nil
}

func (a *App) exportSessionLocked(id string) (*tokenExportSession, error) {
	if a.tokenExport != nil && time.Now().After(a.tokenExport.expires) {
		a.tokenExport = nil
	}
	if id == "" || a.tokenExport == nil || a.tokenExport.id != id {
		return nil, errors.New("凭证预览已失效，请重新选择账号")
	}
	return a.tokenExport, nil
}

func (a *App) CopyTokenExport(exportID string) error {
	a.exportMu.Lock()
	defer a.exportMu.Unlock()
	session, err := a.exportSessionLocked(exportID)
	if err != nil {
		return err
	}
	a.mu.RLock()
	if err := a.ready(); err != nil {
		a.mu.RUnlock()
		return err
	}
	ctx, write := a.ctx, a.clipboardWrite
	a.mu.RUnlock()
	if write == nil {
		write = runtime.ClipboardSetText
	}
	if err := write(ctx, session.snapshot.Text()); err != nil {
		return errors.New("无法复制凭证，账号已保留，请重试")
	}
	session.exported = true
	return nil
}

// SaveTokenExport reports false for a canceled dialog and marks the snapshot
// exported only once the full file has been atomically saved. UI download links
// are not evidence that a backup reached disk.
func (a *App) SaveTokenExport(exportID string) (bool, error) {
	a.exportMu.Lock()
	defer a.exportMu.Unlock()
	session, err := a.exportSessionLocked(exportID)
	if err != nil {
		return false, err
	}
	a.mu.RLock()
	if err := a.ready(); err != nil {
		a.mu.RUnlock()
		return false, err
	}
	ctx, choose, write, dataPath := a.ctx, a.exportSaveDialog, a.exportWriteFile, a.dataPath
	a.mu.RUnlock()
	if choose == nil {
		choose = runtime.SaveFileDialog
	}
	destination, err := choose(ctx, runtime.SaveDialogOptions{Title: "导出所选账号凭证", DefaultFilename: "luma-2fa-export-" + time.Now().Format("2006-01-02") + ".txt", Filters: []runtime.FileFilter{{DisplayName: "文本文件 (*.txt)", Pattern: "*.txt"}}})
	if err != nil {
		return false, errors.New("无法打开保存窗口，账号已保留")
	}
	if destination == "" {
		return false, nil
	}
	destination, err = exportDestination(destination, dataPath)
	if err != nil {
		return false, err
	}
	if write == nil {
		write = writeExportAtomically
	}
	content := []byte(session.snapshot.Text())
	defer clear(content)
	if err := write(destination, content); err != nil {
		return false, errors.New("凭证文件未保存成功，账号已保留，请重新选择位置")
	}
	session.exported = true
	return true, nil
}

func (a *App) DeleteExportedTokens(exportID string) error {
	a.exportMu.Lock()
	defer a.exportMu.Unlock()
	session, err := a.exportSessionLocked(exportID)
	if err != nil {
		return err
	}
	if !session.exported {
		return errors.New("请先成功复制或保存凭证，未删除任何账号")
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if err := a.ready(); err != nil {
		return err
	}
	if err := a.store.DeleteExportSnapshot(session.snapshot); err != nil {
		return err
	}
	a.tokenExport = nil
	return nil
}

func (a *App) DiscardTokenExport(exportID string) error {
	a.exportMu.Lock()
	defer a.exportMu.Unlock()
	if a.tokenExport != nil && a.tokenExport.id == exportID {
		a.tokenExport = nil
	}
	return nil
}

func exportDestination(destination, dataPath string) (string, error) {
	if filepath.Ext(destination) == "" {
		destination += ".txt"
	}
	if !strings.EqualFold(filepath.Ext(destination), ".txt") {
		return "", errors.New("请将凭证保存为 TXT 文件，账号已保留")
	}
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return "", errors.New("保存位置无效，账号已保留")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", errors.New("无法访问保存文件夹，账号已保留")
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	if info, statErr := os.Lstat(absolute); statErr == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return "", errors.New("不能将凭证保存到此位置，账号已保留")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", errors.New("无法访问保存位置，账号已保留")
	}
	protected, err := filepath.Abs(dataPath)
	if err != nil || dataPath == "" {
		return "", errors.New("应用数据目录无效，账号已保留")
	}
	if resolved, err := filepath.EvalSymlinks(protected); err == nil {
		protected = resolved
	}
	if strings.EqualFold(absolute, protected) || strings.HasPrefix(strings.ToLower(absolute), strings.ToLower(protected+string(filepath.Separator))) {
		return "", errors.New("请选择应用数据目录之外的位置保存凭证")
	}
	if executable, err := os.Executable(); err == nil && strings.EqualFold(absolute, executable) {
		return "", errors.New("不能覆盖正在运行的程序，账号已保留")
	}
	return absolute, nil
}

func writeExportAtomically(destination string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(destination), ".luma-export-*.tmp")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return replaceExportFile(temporary, destination)
}
