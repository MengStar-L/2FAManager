// Package updateinstall applies verified portable executable updates without
// opening or modifying the application's token/settings directories.
package updateinstall

import (
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const helperArgument = "--luma-apply-update"
const maximumEXESize = 512 << 20

type Result struct {
	Status     string `json:"status"` // installed, failed, or cancelled
	Message    string `json:"message"`
	TargetPath string `json:"targetPath"`
	BackupPath string `json:"backupPath,omitempty"`
	UpdatedAt  string `json:"updatedAt"`
}

// Job is ready only after the helper has validated the plan and acquired a
// handle to the current process. The caller may then shut down gracefully.
type Job struct{ cancel func() error }

// Cancel stops a prepared update while the current process is still alive.
func (j *Job) Cancel() error {
	if j == nil || j.cancel == nil {
		return nil
	}
	return j.cancel()
}

type plan struct {
	Version     int    `json:"version"`
	Target      string `json:"target"`
	ParentPID   uint32 `json:"parentPID"`
	ParentStart uint64 `json:"parentStart"`
	OldHash     string `json:"oldHash"`
	NewHash     string `json:"newHash"`
	Relaunch    bool   `json:"relaunch"`
}

func validHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

// safePath rejects device/UNC paths, alternate streams, and every existing
// symlink/reparse directory. A portable EXE reached through links can still be
// upgraded manually; silently replacing a redirected path is not safe.
func safePath(path string) (string, error) {
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.ContainsRune(path, 0) {
		return "", errors.New("更新路径必须是本地绝对路径")
	}
	path = filepath.Clean(path)
	if strings.Contains(strings.TrimPrefix(path, filepath.VolumeName(path)), ":") {
		return "", errors.New("更新路径不能包含文件流")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err == nil && info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return "", errors.New("更新路径不能经过链接或特殊文件")
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return path, nil
}

func samePath(a, b string) bool { return strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) }

func fileHash(path string) (string, error) {
	if _, err := safePath(path); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumEXESize {
		return "", errors.New("更新文件大小或类型无效")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maximumEXESize+1))
	if err != nil || n != info.Size() || n > maximumEXESize {
		return "", errors.New("更新文件读取失败或已发生变化")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func checkHash(path, expected string) error {
	if !validHash(expected) {
		return errors.New("更新文件校验值无效")
	}
	actual, err := fileHash(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, expected) {
		return errors.New("更新文件校验失败，未修改程序")
	}
	return nil
}

func checkPE(path string, machine uint16) (uint16, error) {
	f, err := pe.Open(path)
	if err != nil {
		return 0, errors.New("更新文件不是有效的 Windows 程序")
	}
	defer f.Close()
	if f.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || f.Characteristics&pe.IMAGE_FILE_DLL != 0 || f.OptionalHeader == nil || (machine != 0 && f.Machine != machine) {
		return 0, errors.New("更新文件不是兼容的 Windows 可执行程序")
	}
	return f.Machine, nil
}

func copyVerified(source, destination, expected string) (err error) {
	if _, err = safePath(source); err != nil {
		return err
	}
	if _, err = safePath(destination); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		out.Close()
		if err != nil {
			os.Remove(destination)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, maximumEXESize+1))
	if err != nil {
		return err
	}
	if n == 0 || n > maximumEXESize || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), expected) {
		return errors.New("复制更新文件时校验失败")
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return out.Close()
}

func readJSON(path string, value any) error {
	if _, err := safePath(path); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 32<<10 {
		return errors.New("更新计划过大或无法读取")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("更新计划格式无效")
	}
	return nil
}

type transactionOps struct {
	replace  func(target, replacement, backup string) error
	restore  func(backup, target string) error
	launch   func(target string) error
	retry    func(error) bool
	deadline time.Time
}

// installTransaction is also exercised using disposable synthetic files.
// ReplaceFile can fail after moving the original to the backup (error 1177),
// so recovery verifies actual hashes rather than assuming every error is inert.
func installTransaction(p plan, replacement, backup string, ops transactionOps) error {
	if err := checkHash(p.Target, p.OldHash); err != nil {
		return fmt.Errorf("原程序已变化，更新已停止：%w", err)
	}
	if err := checkHash(replacement, p.NewHash); err != nil {
		return err
	}
	for {
		err := ops.replace(p.Target, replacement, backup)
		if err == nil {
			break
		}
		if checkHash(p.Target, p.OldHash) != nil {
			if restoreErr := recoverOriginal(p, backup, ops); restoreErr != nil {
				return fmt.Errorf("更新失败：%v；恢复失败：%w；旧版备份：%s", err, restoreErr, backup)
			}
			return fmt.Errorf("更新失败，已恢复旧版本：%w", err)
		}
		if !ops.retry(err) || time.Now().After(ops.deadline) {
			return fmt.Errorf("无法替换程序（可能无写入权限或文件仍被占用），旧版本未改变：%w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := checkHash(p.Target, p.NewHash); err != nil {
		return rollbackError(p, backup, ops, err)
	}
	if p.Relaunch {
		if err := ops.launch(p.Target); err != nil {
			return rollbackError(p, backup, ops, fmt.Errorf("新版本未能启动：%w", err))
		}
	}
	return nil
}

func recoverOriginal(p plan, backup string, ops transactionOps) error {
	if checkHash(p.Target, p.OldHash) == nil {
		return nil
	}
	if err := checkHash(backup, p.OldHash); err != nil {
		return err
	}
	if err := ops.restore(backup, p.Target); err != nil {
		return err
	}
	return checkHash(p.Target, p.OldHash)
}

func rollbackError(p plan, backup string, ops transactionOps, cause error) error {
	if err := recoverOriginal(p, backup, ops); err != nil {
		return fmt.Errorf("%v；恢复旧版失败：%w；旧版备份：%s", cause, err, backup)
	}
	if p.Relaunch {
		if err := ops.launch(p.Target); err != nil {
			return fmt.Errorf("%v；已恢复旧版本，请手动打开程序：%w", cause, err)
		}
	}
	return fmt.Errorf("%v；已恢复旧版本", cause)
}
