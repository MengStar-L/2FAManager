//go:build windows

package updateinstall

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFileProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

func targetDirectory(target string, create bool) (string, error) {
	target, err := safePath(target)
	if err != nil {
		return "", err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(strings.ToLower(target)))
	path, err := safePath(filepath.Join(cache, "LumaAuthenticator", "update-installer", hex.EncodeToString(key[:16])))
	if err != nil {
		return "", err
	}
	if create {
		if err := os.MkdirAll(path, 0700); err != nil {
			return "", err
		}
		// Go mode bits do not restrict Windows ACLs. Remove inherited access
		// and grant only this user and SYSTEM, including newly created children.
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return "", err
		}
		sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + user.User.Sid.String() + ")")
		if err != nil {
			return "", err
		}
		acl, _, err := sd.DACL()
		if err != nil {
			return "", err
		}
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
			return "", err
		}
	}
	return path, nil
}

func processStart(handle windows.Handle) (uint64, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return 0, err
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), nil
}

// Start prepares a hidden installer and waits for its explicit readiness.
// It never exits the caller, elevates privileges, or launches a shell.
// Once it succeeds the original download may be removed by the caller.
func Start(ctx context.Context, stagedEXE, expectedSHA256 string, relaunch bool) (*Job, error) {
	if ctx == nil {
		return nil, errors.New("更新上下文不能为空")
	}
	target, err := os.Executable()
	if err != nil {
		return nil, err
	}
	target, err = safePath(target)
	if err != nil {
		return nil, err
	}
	if strings.ToLower(filepath.Ext(target)) != ".exe" {
		return nil, errors.New("只有独立 EXE 版本支持自动安装更新")
	}
	if err := checkHash(stagedEXE, expectedSHA256); err != nil {
		return nil, err
	}
	oldHash, err := fileHash(target)
	if err != nil {
		return nil, err
	}
	machine, err := checkPE(target, 0)
	if err != nil {
		return nil, err
	}
	if _, err := checkPE(stagedEXE, machine); err != nil {
		return nil, err
	}
	directory, err := targetDirectory(target, true)
	if err != nil {
		return nil, fmt.Errorf("无法创建私有更新目录：%w", err)
	}
	cleanupCompleted(target, directory)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	jobDirectory := filepath.Join(directory, "job-"+hex.EncodeToString(nonce[:]))
	if err := os.Mkdir(jobDirectory, 0700); err != nil {
		return nil, err
	}
	helper := filepath.Join(jobDirectory, "helper.exe")
	if err := copyVerified(target, helper, oldHash); err != nil {
		return nil, err
	}
	if err := copyVerified(stagedEXE, filepath.Join(jobDirectory, "candidate.exe"), expectedSHA256); err != nil {
		return nil, err
	}
	start, err := processStart(windows.CurrentProcess())
	if err != nil {
		return nil, err
	}
	p := plan{Version: 1, Target: target, ParentPID: uint32(os.Getpid()), ParentStart: start, OldHash: oldHash, NewHash: strings.ToLower(expectedSHA256), Relaunch: relaunch}
	manifest := filepath.Join(jobDirectory, "manifest.json")
	if err := writeJSON(manifest, p); err != nil {
		return nil, err
	}
	cmd := exec.Command(helper, helperArgument, manifest)
	cmd.Dir = jobDirectory
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("无法启动更新助手：%w", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	var cancelOnce sync.Once
	var cancelErr error
	job := &Job{cancel: func() error {
		cancelOnce.Do(func() {
			select {
			case <-done:
				return
			default:
			}
			cancelErr = os.WriteFile(filepath.Join(jobDirectory, "cancel"), []byte("cancel"), 0600)
			if cancelErr == nil {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					cancelErr = errors.New("更新助手取消超时")
				}
			}
		})
		return cancelErr
	}}
	readyContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-readyContext.Done():
			_ = job.Cancel()
			return nil, fmt.Errorf("更新助手准备超时或已取消：%w", readyContext.Err())
		case <-done:
			var result Result
			if readJSON(filepath.Join(jobDirectory, "result.json"), &result) == nil && result.Message != "" {
				return nil, errors.New(result.Message)
			}
			return nil, errors.New("更新助手未能完成准备，程序未修改")
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(jobDirectory, "ready")); err == nil {
				return job, nil
			}
		}
	}
}

func validatePlan(p plan, helper, manifest string) (string, error) {
	if p.Version != 1 || p.ParentPID == 0 || p.ParentPID == uint32(os.Getpid()) || p.ParentStart == 0 || !validHash(p.OldHash) || !validHash(p.NewHash) {
		return "", errors.New("更新计划无效")
	}
	if _, err := safePath(p.Target); err != nil {
		return "", err
	}
	if strings.ToLower(filepath.Ext(p.Target)) != ".exe" || samePath(p.Target, helper) {
		return "", errors.New("更新目标无效")
	}
	directory, err := targetDirectory(p.Target, false)
	if err != nil {
		return "", err
	}
	jobDirectory := filepath.Dir(helper)
	id := filepath.Base(jobDirectory)
	if len(id) != 36 || !strings.HasPrefix(id, "job-") {
		return "", errors.New("更新目录无效")
	}
	if _, err := hex.DecodeString(id[4:]); err != nil {
		return "", errors.New("更新目录标识无效")
	}
	if !samePath(filepath.Dir(jobDirectory), directory) || !samePath(helper, filepath.Join(jobDirectory, "helper.exe")) || !samePath(manifest, filepath.Join(jobDirectory, "manifest.json")) {
		return "", errors.New("更新助手只能使用自身私有目录中的计划")
	}
	if _, err := safePath(helper); err != nil {
		return "", err
	}
	if err := checkHash(helper, p.OldHash); err != nil {
		return "", err
	}
	return jobDirectory, nil
}

// HandleHelper must run at the very beginning of main, before Wails, the
// single-instance lock, or any vault initialization. It blocks only in helper
// mode; regular application starts immediately return (false, nil).
func HandleHelper(args []string) (bool, error) {
	if len(args) == 0 || args[0] != helperArgument {
		return false, nil
	}
	if len(args) != 2 {
		return true, errors.New("更新助手参数无效")
	}
	helper, err := os.Executable()
	if err != nil {
		return true, err
	}
	if !samePath(args[1], filepath.Join(filepath.Dir(helper), "manifest.json")) {
		return true, errors.New("更新计划必须位于助手所在目录")
	}
	var p plan
	if err := readJSON(args[1], &p); err != nil {
		return true, err
	}
	jobDirectory, err := validatePlan(p, helper, args[1])
	if err != nil {
		return true, err
	}
	err = runHelper(p, jobDirectory)
	return true, err
}

func runHelper(p plan, directory string) (resultErr error) {
	id := filepath.Base(directory)
	replacement := filepath.Join(filepath.Dir(p.Target), ".luma-update-"+id+".new.exe")
	backup := filepath.Join(filepath.Dir(p.Target), ".luma-update-"+id+".backup.exe")
	writeResult := func(status, message string) {
		result := Result{Status: status, Message: message, TargetPath: p.Target, BackupPath: backup, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
		_ = writeJSON(filepath.Join(directory, "result.json"), result)
		_ = writeJSON(filepath.Join(filepath.Dir(directory), "last-result.json"), result)
	}
	defer func() {
		// Only exact paths derived from the validated private job are touched.
		os.Remove(replacement)
		if resultErr != nil {
			writeResult("failed", resultErr.Error())
		}
	}()
	release, err := installerLock(p.Target)
	if err != nil {
		return err
	}
	defer release()
	parent, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, p.ParentPID)
	if err != nil {
		return fmt.Errorf("无法确认待更新程序进程：%w", err)
	}
	defer windows.CloseHandle(parent)
	start, err := processStart(parent)
	if err != nil || start != p.ParentStart {
		return errors.New("待更新程序进程标识已变化")
	}
	name := make([]uint16, 32768)
	length := uint32(len(name))
	if err := windows.QueryFullProcessImageName(parent, 0, &name[0], &length); err != nil || !samePath(windows.UTF16ToString(name[:length]), p.Target) {
		return errors.New("更新目标不是发起更新的实际程序")
	}
	if state, _ := windows.WaitForSingleObject(parent, 0); state != uint32(windows.WAIT_TIMEOUT) {
		return errors.New("待更新程序已退出，更新计划已失效")
	}
	if err := checkHash(p.Target, p.OldHash); err != nil {
		return err
	}
	machine, err := checkPE(p.Target, 0)
	if err != nil {
		return err
	}
	candidate := filepath.Join(directory, "candidate.exe")
	if _, err := checkPE(candidate, machine); err != nil {
		return err
	}
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		return errors.New("更新备份路径已存在或不可访问")
	}
	// Preparing the replacement next to the target both checks directory write
	// permission before Quit and guarantees the same volume for ReplaceFileW.
	if err := copyVerified(candidate, replacement, p.NewHash); err != nil {
		return fmt.Errorf("程序所在文件夹不可写或更新文件校验失败，请手动更新：%w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "ready"), []byte("ready"), 0600); err != nil {
		return err
	}
	deadline := time.Now().Add(120 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(directory, "cancel")); err == nil {
			writeResult("cancelled", "更新已取消，旧版本未改变。")
			return nil
		}
		state, err := windows.WaitForSingleObject(parent, 100)
		if err != nil {
			return err
		}
		if state == windows.WAIT_OBJECT_0 {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("等待程序退出超时，未安装更新。下次可以重新尝试。")
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "cancel")); err == nil {
		writeResult("cancelled", "更新已取消，旧版本未改变。")
		return nil
	}
	ops := nativeTransactionOps()
	ops.launch = func(target string) error {
		// Persist before launching so either the new or rolled-back application
		// can report its installation status during startup.
		if checkHash(target, p.NewHash) == nil {
			writeResult("installed", "更新已安装。")
		} else {
			writeResult("failed", "新版本未能启动，已恢复旧版本。")
		}
		return launchApplication(target)
	}
	if err := installTransaction(p, replacement, backup, ops); err != nil {
		return err
	}
	if !p.Relaunch {
		writeResult("installed", "更新已安装。")
	}
	return nil
}

func nativeTransactionOps() transactionOps {
	return transactionOps{
		replace: replaceFile,
		restore: moveReplace,
		launch:  launchApplication,
		retry: func(err error) bool {
			return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
		},
		deadline: time.Now().Add(30 * time.Second),
	}
}

func replaceFile(target, replacement, backup string) error {
	a, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(replacement)
	if err != nil {
		return err
	}
	c, err := windows.UTF16PtrFromString(backup)
	if err != nil {
		return err
	}
	ok, _, err := replaceFileProc.Call(uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(c)), 0, 0, 0)
	if ok == 0 {
		return err
	}
	return nil
}

func moveReplace(source, destination string) error {
	a, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func launchApplication(target string) error {
	cmd := exec.Command(target)
	cmd.Dir = filepath.Dir(target)
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".update-json-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return moveReplace(temp.Name(), path)
}

// ReadLastResult returns nil when this portable installation has no record.
// Records contain no token data. Reading does not delete backups or consume the
// result, so an interrupted startup can still explain an earlier failed update.
func ReadLastResult() (*Result, error) {
	target, err := os.Executable()
	if err != nil {
		return nil, err
	}
	directory, err := targetDirectory(target, false)
	if err != nil {
		return nil, err
	}
	var result Result
	if err := readJSON(filepath.Join(directory, "last-result.json"), &result); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !samePath(result.TargetPath, target) {
		return nil, errors.New("更新结果不属于当前程序")
	}
	cleanupCompleted(target, directory)
	return &result, nil
}

// The mutex also prevents startup cleanup from removing a helper that has
// recorded success immediately before launching the replacement application.
func installerLock(target string) (func(), error) {
	runtime.LockOSThread() // A Windows mutex belongs to the acquiring OS thread.
	key := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(target))))
	name, _ := windows.UTF16PtrFromString(`Local\Luma.UpdateInstall.` + hex.EncodeToString(key[:16]))
	mutex, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		runtime.UnlockOSThread()
		return nil, err
	}
	state, err := windows.WaitForSingleObject(mutex, 0)
	if err != nil || (state != windows.WAIT_OBJECT_0 && state != windows.WAIT_ABANDONED) {
		windows.CloseHandle(mutex)
		runtime.UnlockOSThread()
		return nil, errors.New("另一个更新助手仍在运行")
	}
	return func() { windows.ReleaseMutex(mutex); windows.CloseHandle(mutex); runtime.UnlockOSThread() }, nil
}

// Remove only recognized, completed jobs' verified executable copies. Keep
// metadata and the newest successful backup; failed recovery backups are
// always preserved. Unknown files/directories are never recursively removed.
func cleanupCompleted(target, directory string) {
	release, err := installerLock(target)
	if err != nil {
		return
	}
	defer release()
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	type completed struct {
		directory, backup string
		p                 plan
		result            Result
	}
	var jobs []completed
	latestBackup, latestTime := "", ""
	for _, entry := range entries {
		id := entry.Name()
		if !entry.IsDir() || len(id) != 36 || !strings.HasPrefix(id, "job-") {
			continue
		}
		if _, err := hex.DecodeString(id[4:]); err != nil {
			continue
		}
		jobDir := filepath.Join(directory, id)
		if _, err := safePath(jobDir); err != nil {
			continue
		}
		var p plan
		var result Result
		if readJSON(filepath.Join(jobDir, "manifest.json"), &p) != nil || readJSON(filepath.Join(jobDir, "result.json"), &result) != nil {
			continue
		}
		if p.Version != 1 || !validHash(p.OldHash) || !validHash(p.NewHash) || !samePath(p.Target, target) || !samePath(result.TargetPath, target) {
			continue
		}
		if result.Status != "installed" && result.Status != "failed" && result.Status != "cancelled" {
			continue
		}
		backup := filepath.Join(filepath.Dir(target), ".luma-update-"+id+".backup.exe")
		if !samePath(result.BackupPath, backup) {
			continue
		}
		if result.Status == "installed" && checkHash(backup, p.OldHash) == nil && result.UpdatedAt >= latestTime {
			latestBackup, latestTime = backup, result.UpdatedAt
		}
		jobs = append(jobs, completed{jobDir, backup, p, result})
	}
	for _, job := range jobs {
		for filename, hash := range map[string]string{"helper.exe": job.p.OldHash, "candidate.exe": job.p.NewHash} {
			path := filepath.Join(job.directory, filename)
			if checkHash(path, hash) == nil {
				_ = os.Remove(path)
			}
		}
		if job.result.Status == "installed" && latestBackup != "" && !samePath(job.backup, latestBackup) && checkHash(job.backup, job.p.OldHash) == nil {
			_ = os.Remove(job.backup)
		}
	}
}
