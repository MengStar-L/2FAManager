//go:build windows

package updateinstall

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestMain(m *testing.M) {
	if handled, err := HandleHelper(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("LUMA_INSTALL_FIXTURE") == "1" {
		marker := os.Getenv("LUMA_INSTALL_MARKER")
		if len(os.Args) == 2 && os.Args[1] == "--fixture-parent" {
			ctx, cancel := context.WithCancel(context.Background())
			job, err := Start(ctx, os.Getenv("LUMA_INSTALL_STAGE"), os.Getenv("LUMA_INSTALL_HASH"), os.Getenv("LUMA_INSTALL_MODE") == "relaunch")
			cancel() // Shutdown's context cancellation must not cancel a ready job.
			if err == nil && os.Getenv("LUMA_INSTALL_MODE") == "cancel" {
				err = job.Cancel()
			}
			if err != nil {
				_ = os.WriteFile(marker+".error", []byte(err.Error()), 0600)
				os.Exit(2)
			}
			_ = os.WriteFile(marker+".prepared", []byte("ready"), 0600)
			os.Exit(0)
		}
		exe, _ := os.Executable()
		hash, _ := fileHash(exe)
		_ = os.WriteFile(marker, []byte(hash), 0600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func putFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func syntheticPlan(t *testing.T) (plan, string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "中文 空格 & $ 更新")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target, replacement, backup := filepath.Join(dir, "应用.exe"), filepath.Join(dir, "候选.exe"), filepath.Join(dir, "备份.exe")
	return plan{Target: target, OldHash: putFile(t, target, []byte("original executable fixture")), NewHash: putFile(t, replacement, []byte("new executable fixture"))}, replacement, backup
}

func TestNativeTransactionReplacementAndRollback(t *testing.T) {
	for _, mode := range []string{"success", "locked_then_success", "permission", "partial_failure", "restart_failure", "rollback_failure", "changed_target"} {
		t.Run(mode, func(t *testing.T) {
			p, replacement, backup := syntheticPlan(t)
			ops := nativeTransactionOps()
			launches := 0
			ops.launch = func(string) error { launches++; return nil }
			wantError := false
			switch mode {
			case "locked_then_success":
				calls := 0
				ops.replace = func(a, b, c string) error {
					calls++
					if calls < 3 {
						return windows.ERROR_SHARING_VIOLATION
					}
					return replaceFile(a, b, c)
				}
			case "permission":
				wantError = true
				ops.deadline = time.Now().Add(-time.Second)
				ops.replace = func(string, string, string) error { return windows.ERROR_ACCESS_DENIED }
			case "partial_failure":
				wantError = true
				ops.replace = func(a, b, c string) error {
					if err := moveReplace(a, c); err != nil {
						return err
					}
					return syscall.Errno(1177)
				}
			case "restart_failure", "rollback_failure":
				wantError, p.Relaunch = true, true
				ops.launch = func(string) error {
					launches++
					if launches == 1 {
						return errors.New("synthetic CreateProcess failure")
					}
					return nil
				}
				if mode == "rollback_failure" {
					ops.restore = func(string, string) error { return errors.New("synthetic restore lock") }
				}
			case "changed_target":
				wantError = true
				putFile(t, p.Target, []byte("external edit"))
			}
			err := installTransaction(p, replacement, backup, ops)
			if (err != nil) != wantError {
				t.Fatalf("unexpected result: %v", err)
			}
			if mode == "changed_target" {
				if data, _ := os.ReadFile(p.Target); string(data) != "external edit" {
					t.Fatal("overwrote externally changed target")
				}
				return
			}
			if mode == "rollback_failure" {
				if checkHash(backup, p.OldHash) != nil || !strings.Contains(err.Error(), backup) {
					t.Fatal("lost recoverable backup or its location")
				}
				return
			}
			wantHash := p.NewHash
			if wantError {
				wantHash = p.OldHash
			}
			if err := checkHash(p.Target, wantHash); err != nil {
				t.Fatal(err)
			}
			if mode == "restart_failure" && launches != 2 {
				t.Fatal("old version was not relaunched after rollback")
			}
			if !p.Relaunch && launches != 0 {
				t.Fatal("silent update launched application")
			}
			if !wantError && checkHash(backup, p.OldHash) != nil {
				t.Fatal("successful update did not retain original backup")
			}
		})
	}
}

func TestValidationRejectsPathsHashesAndNonPE(t *testing.T) {
	for _, path := range []string{"relative.exe", `\\server\share\app.exe`, `\\?\C:\app.exe`, `C:\app.exe:stream`, `C:\app\nul` + string(rune(0))} {
		if _, err := safePath(path); err == nil {
			t.Fatalf("unsafe path accepted: %q", path)
		}
	}
	p, replacement, _ := syntheticPlan(t)
	if err := checkHash(replacement, p.OldHash); err == nil {
		t.Fatal("wrong hash accepted")
	}
	if _, err := checkPE(replacement, 0); err == nil {
		t.Fatal("non-PE accepted")
	}
	if _, err := checkPE(os.Args[0], 0xffff); err == nil {
		t.Fatal("wrong machine accepted")
	}
	destination := filepath.Join(filepath.Dir(replacement), "existing.exe")
	putFile(t, destination, []byte("preserve me"))
	if err := copyVerified(replacement, destination, p.NewHash); err == nil {
		t.Fatal("existing destination overwritten")
	}
	if data, _ := os.ReadFile(destination); string(data) != "preserve me" {
		t.Fatal("existing data damaged")
	}
	if handled, err := HandleHelper([]string{helperArgument, filepath.Join(t.TempDir(), "manifest.json")}); !handled || err == nil {
		t.Fatal("arbitrary external manifest accepted")
	}
}

func TestHiddenHelperInstallsOnlyIsolatedPortableFixture(t *testing.T) {
	// Test copies are GUI-subsystem EXEs: neither the helper nor the synthetic
	// relaunch can open a console, and no running user application is involved.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	peOffset := int(binary.LittleEndian.Uint32(image[0x3c:0x40]))
	binary.LittleEndian.PutUint16(image[peOffset+4+20+68:], 2) // IMAGE_SUBSYSTEM_WINDOWS_GUI
	for _, mode := range []string{"relaunch", "silent", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "真实 helper 中文 路径")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			target, stage, marker := filepath.Join(dir, "portable app.exe"), filepath.Join(dir, "stage.exe"), filepath.Join(dir, "launched")
			oldHash := putFile(t, target, image)
			newImage := append(append([]byte(nil), image...), []byte("\nnew fixture build\n")...)
			newHash := putFile(t, stage, newImage)
			vault := filepath.Join(dir, "vault-sentinel")
			putFile(t, vault, []byte("private data must remain untouched"))
			cache := filepath.Join(dir, "private-cache")
			cmd := exec.Command(target, "--fixture-parent")
			cmd.Env = append(os.Environ(), "LOCALAPPDATA="+cache, "LUMA_INSTALL_FIXTURE=1", "LUMA_INSTALL_STAGE="+stage, "LUMA_INSTALL_HASH="+newHash, "LUMA_INSTALL_MARKER="+marker, "LUMA_INSTALL_MODE="+mode)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
			if err := cmd.Run(); err != nil {
				detail, _ := os.ReadFile(marker + ".error")
				t.Fatalf("parent: %v, %s", err, detail)
			}
			if _, err := os.Stat(marker + ".prepared"); err != nil {
				t.Fatal("no READY handshake")
			}
			key := sha256.Sum256([]byte(strings.ToLower(target)))
			resultPath := filepath.Join(cache, "LumaAuthenticator", "update-installer", hex.EncodeToString(key[:16]), "last-result.json")
			var result Result
			deadline := time.Now().Add(15 * time.Second)
			for readJSON(resultPath, &result) != nil || (result.Status != "installed" && result.Status != "cancelled" && result.Status != "failed") {
				if time.Now().After(deadline) {
					t.Fatal("helper result timeout")
				}
				time.Sleep(50 * time.Millisecond)
			}
			if result.Status == "failed" {
				t.Fatal(result.Message)
			}
			want := newHash
			if mode == "cancel" {
				want = oldHash
				if result.Status != "cancelled" {
					t.Fatal(result)
				}
			}
			if err := checkHash(target, want); err != nil {
				t.Fatal(err)
			}
			if mode == "relaunch" {
				for {
					data, err := os.ReadFile(marker)
					if err == nil {
						if string(data) != newHash {
							t.Fatal("restarted wrong version")
						}
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("new app did not launch")
					}
					time.Sleep(50 * time.Millisecond)
				}
			} else if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("silent/cancelled update launched app")
			}
			if data, _ := os.ReadFile(vault); string(data) != "private data must remain untouched" {
				t.Fatal("vault sentinel changed")
			}
			if mode != "cancel" && checkHash(result.BackupPath, oldHash) != nil {
				t.Fatal("backup missing")
			}
			// Wait briefly for the exact copied helper to close its own image;
			// t.TempDir cleanup never targets a user's application or vault.
			time.Sleep(200 * time.Millisecond)
		})
	}
}

func ExampleHandleHelper() {
	handled, err := HandleHelper(nil)
	fmt.Println(handled, err)
	// Output: false <nil>
}

func TestCleanupPreservesActiveUnknownAndRecoveryFiles(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "app.exe")
	putFile(t, target, []byte("current"))
	var oldBackup, newestBackup, failedBackup, activeHelper, unknownHelper string
	for i, status := range []string{"installed", "installed", "failed", ""} {
		id := fmt.Sprintf("job-%032x", i+1)
		jobDir := filepath.Join(directory, id)
		if err := os.Mkdir(jobDir, 0700); err != nil {
			t.Fatal(err)
		}
		helper, candidate := filepath.Join(jobDir, "helper.exe"), filepath.Join(jobDir, "candidate.exe")
		p := plan{Version: 1, Target: target, OldHash: putFile(t, helper, []byte("old executable")), NewHash: putFile(t, candidate, []byte("new executable"))}
		if err := writeJSON(filepath.Join(jobDir, "manifest.json"), p); err != nil {
			t.Fatal(err)
		}
		backup := filepath.Join(directory, ".luma-update-"+id+".backup.exe")
		putFile(t, backup, []byte("old executable"))
		if status != "" {
			if err := writeJSON(filepath.Join(jobDir, "result.json"), Result{Status: status, TargetPath: target, BackupPath: backup, UpdatedAt: fmt.Sprintf("2026-10-07T12:00:0%dZ", i)}); err != nil {
				t.Fatal(err)
			}
		} else {
			activeHelper = helper
		}
		switch i {
		case 0:
			oldBackup = backup
		case 1:
			newestBackup = backup
		case 2:
			failedBackup = backup
			unknownHelper = helper
			putFile(t, helper, []byte("unrecognized replacement"))
		}
	}
	unknown := filepath.Join(directory, "unrecognized.exe")
	putFile(t, unknown, []byte("do not remove"))
	cleanupCompleted(target, directory)
	if _, err := os.Stat(oldBackup); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("obsolete successful backup retained")
	}
	for _, path := range []string{newestBackup, failedBackup, activeHelper, unknownHelper, unknown} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("removed preserved file %s: %v", path, err)
		}
	}
	for i := 1; i <= 2; i++ {
		for _, name := range []string{"helper.exe", "candidate.exe"} {
			path := filepath.Join(directory, fmt.Sprintf("job-%032x", i), name)
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("completed executable copy retained: %s", path)
			}
		}
	}
}
