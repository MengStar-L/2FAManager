//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const trayMessage = 0x8001

type trayWindowClass struct {
	Size        uint32
	Style       uint32
	WindowProc  uintptr
	ClassExtra  int32
	WindowExtra int32
	Instance    uintptr
	Icon        uintptr
	Cursor      uintptr
	Background  uintptr
	MenuName    *uint16
	ClassName   *uint16
	SmallIcon   uintptr
}

type trayMessageData struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   winPoint
	Private uint32
}

type notifyIconData struct {
	Size             uint32
	Window           uintptr
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             uintptr
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUID             windows.GUID
	BalloonIcon      uintptr
}

var (
	registerClassExProc       = user32.NewProc("RegisterClassExW")
	unregisterClassProc       = user32.NewProc("UnregisterClassW")
	createWindowExProc        = user32.NewProc("CreateWindowExW")
	defWindowProc             = user32.NewProc("DefWindowProcW")
	destroyWindowProc         = user32.NewProc("DestroyWindow")
	getMessageProc            = user32.NewProc("GetMessageW")
	translateMessageProc      = user32.NewProc("TranslateMessage")
	dispatchMessageProc       = user32.NewProc("DispatchMessageW")
	postMessageProc           = user32.NewProc("PostMessageW")
	postQuitMessageProc       = user32.NewProc("PostQuitMessage")
	registerWindowMessageProc = user32.NewProc("RegisterWindowMessageW")
	createIconResourceProc    = user32.NewProc("CreateIconFromResourceEx")
	destroyIconProc           = user32.NewProc("DestroyIcon")
	createPopupMenuProc       = user32.NewProc("CreatePopupMenu")
	appendMenuProc            = user32.NewProc("AppendMenuW")
	trackPopupMenuProc        = user32.NewProc("TrackPopupMenu")
	destroyMenuProc           = user32.NewProc("DestroyMenu")
	getCursorPosProc          = user32.NewProc("GetCursorPos")
	setTimerProc              = user32.NewProc("SetTimer")
	killTimerProc             = user32.NewProc("KillTimer")
	getModuleHandleProc       = kernel32.NewProc("GetModuleHandleW")
	shellNotifyIconProc       = windows.NewLazySystemDLL("shell32.dll").NewProc("Shell_NotifyIconW")
)

// Tray owns a hidden Win32 message window on one OS thread. Callbacks run on Go
// goroutines, keeping the native tray message loop responsive to shell events.
type Tray struct {
	hwnd           atomic.Uintptr
	ready          atomic.Bool
	closing        atomic.Bool
	done           chan struct{}
	icon           uintptr
	callback       uintptr
	taskbarCreated uint32
	onShow         func()
	onQuit         func()
	onLost         func()
}

func NewTray(icon []byte, onShow, onQuit, onLost func()) (*Tray, error) {
	t := &Tray{done: make(chan struct{}), onShow: onShow, onQuit: onQuit, onLost: onLost}
	started := make(chan error, 1)
	go t.run(icon, started)
	if err := <-started; err != nil {
		<-t.done
		return nil, err
	}
	return t, nil
}

func (t *Tray) Ready() bool { return t != nil && t.ready.Load() && !t.closing.Load() }

func (t *Tray) Close() {
	if t == nil {
		return
	}
	if t.closing.CompareAndSwap(false, true) {
		t.ready.Store(false)
		if hwnd := t.hwnd.Load(); hwnd != 0 {
			postMessageProc.Call(hwnd, 0x10, 0, 0)
		}
	}
	select {
	case <-t.done:
	case <-time.After(3 * time.Second):
	}
}

func loadEmbeddedIcon(icon []byte, size int) (uintptr, error) {
	resource, err := iconResource(icon, size)
	if err != nil {
		return 0, err
	}
	handle, _, err := createIconResourceProc.Call(uintptr(unsafe.Pointer(&resource[0])), uintptr(len(resource)), 1, 0x30000, uintptr(size), uintptr(size), 0)
	runtime.KeepAlive(resource)
	if handle == 0 {
		return 0, winError("无法加载应用图标", err)
	}
	return handle, nil
}

func (t *Tray) run(icon []byte, started chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)
	defer t.ready.Store(false)
	var err error
	t.icon, err = loadEmbeddedIcon(icon, 32)
	if err != nil {
		started <- err
		return
	}
	defer destroyIconProc.Call(t.icon)
	className, _ := windows.UTF16PtrFromString(fmt.Sprintf("LumaTrayWindow-%d", os.Getpid()))
	instance, _, _ := getModuleHandleProc.Call(0)
	t.callback = syscall.NewCallback(t.windowProc)
	wc := trayWindowClass{Size: uint32(unsafe.Sizeof(trayWindowClass{})), WindowProc: t.callback, Instance: instance, ClassName: className}
	atom, _, err := registerClassExProc.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		started <- winError("无法注册系统托盘窗口", err)
		return
	}
	defer unregisterClassProc.Call(uintptr(unsafe.Pointer(className)), instance)
	// An invisible top-level window (not HWND_MESSAGE) receives Explorer's
	// TaskbarCreated broadcast when the notification area is reconstructed.
	hwnd, _, err := createWindowExProc.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		started <- winError("无法创建系统托盘窗口", err)
		return
	}
	t.hwnd.Store(hwnd)
	defer t.hwnd.Store(0)
	defer destroyWindowProc.Call(hwnd)
	messageName, _ := windows.UTF16PtrFromString("TaskbarCreated")
	message, _, _ := registerWindowMessageProc.Call(uintptr(unsafe.Pointer(messageName)))
	t.taskbarCreated = uint32(message)
	if !t.notify(0) {
		started <- errors.New("Windows 通知区域暂不可用")
		return
	}
	defer t.notify(2)
	t.ready.Store(true)
	started <- nil
	var msg trayMessageData
	for {
		result, _, _ := getMessageProc.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		translateMessageProc.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessageProc.Call(uintptr(unsafe.Pointer(&msg)))
	}
	if !t.closing.Load() {
		t.lost()
	}
}

func (t *Tray) notify(operation uintptr) bool {
	data := notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: t.hwnd.Load(), ID: 1, Flags: 1 | 2 | 4, CallbackMessage: trayMessage, Icon: t.icon}
	tip, _ := windows.UTF16FromString("Luma · 拾光验证器")
	copy(data.Tip[:], tip)
	ok, _, _ := shellNotifyIconProc.Call(operation, uintptr(unsafe.Pointer(&data)))
	return ok != 0
}

func (t *Tray) lost() {
	t.ready.Store(false)
	if t.onLost != nil {
		go t.onLost()
	}
}

func (t *Tray) windowProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	if t.taskbarCreated != 0 && message == t.taskbarCreated {
		t.ready.Store(false)
		if t.notify(0) {
			t.ready.Store(true)
		} else {
			t.lost()
			setTimerProc.Call(hwnd, 1, 1000, 0)
		}
		return 0
	}
	switch message {
	case trayMessage:
		switch uint32(lParam) {
		case 0x202, 0x203: // WM_LBUTTONUP / WM_LBUTTONDBLCLK
			if t.onShow != nil {
				go t.onShow()
			}
		case 0x205, 0x7b: // WM_RBUTTONUP / WM_CONTEXTMENU
			t.showMenu(hwnd)
		}
		return 0
	case 0x113: // WM_TIMER: retry after an Explorer restart.
		if wParam == 1 && t.notify(0) {
			t.ready.Store(true)
			killTimerProc.Call(hwnd, 1)
		}
		return 0
	case 0x10: // WM_CLOSE
		t.ready.Store(false)
		t.notify(2)
		destroyWindowProc.Call(hwnd)
		return 0
	case 0x2: // WM_DESTROY
		postQuitMessageProc.Call(0)
		return 0
	}
	result, _, _ := defWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
	return result
}

func (t *Tray) showMenu(hwnd uintptr) {
	menu, _, _ := createPopupMenuProc.Call()
	if menu == 0 {
		return
	}
	defer destroyMenuProc.Call(menu)
	show, _ := windows.UTF16PtrFromString("显示 Luma")
	quit, _ := windows.UTF16PtrFromString("退出")
	appendMenuProc.Call(menu, 0, 1, uintptr(unsafe.Pointer(show)))
	appendMenuProc.Call(menu, 0x800, 0, 0)
	appendMenuProc.Call(menu, 0, 2, uintptr(unsafe.Pointer(quit)))
	var point winPoint
	getCursorPosProc.Call(uintptr(unsafe.Pointer(&point)))
	setForegroundWindowProc.Call(hwnd)
	command, _, _ := trackPopupMenuProc.Call(menu, 0x100|0x2, uintptr(point.X), uintptr(point.Y), 0, hwnd, 0)
	postMessageProc.Call(hwnd, 0, 0, 0)
	if command == 1 && t.onShow != nil {
		go t.onShow()
	}
	if command == 2 && t.onQuit != nil {
		go t.onQuit()
	}
}
