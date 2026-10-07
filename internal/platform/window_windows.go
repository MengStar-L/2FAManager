//go:build windows

package platform

import (
	"errors"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type winPoint struct{ X, Y int32 }
type winRect struct{ Left, Top, Right, Bottom int32 }
type windowPlacement struct {
	Length         uint32
	Flags          uint32
	ShowCmd        uint32
	MinPosition    winPoint
	MaxPosition    winPoint
	NormalPosition winRect
}
type monitorInfo struct {
	Size    uint32
	Monitor winRect
	Work    winRect
	Flags   uint32
}

var (
	findWindowProc          = user32.NewProc("FindWindowW")
	getWindowPIDProc        = user32.NewProc("GetWindowThreadProcessId")
	getWindowPlacementProc  = user32.NewProc("GetWindowPlacement")
	setWindowPlacementProc  = user32.NewProc("SetWindowPlacement")
	monitorFromWindowProc   = user32.NewProc("MonitorFromWindow")
	monitorFromRectProc     = user32.NewProc("MonitorFromRect")
	getMonitorInfoProc      = user32.NewProc("GetMonitorInfoW")
	getDPIForWindowProc     = user32.NewProc("GetDpiForWindow")
	setForegroundWindowProc = user32.NewProc("SetForegroundWindow")
	sendMessageProc         = user32.NewProc("SendMessageW")
	messageBoxProc          = user32.NewProc("MessageBoxW")
	windowIconsMu           sync.Mutex
	windowIcons             []uintptr
)

func applicationWindow(className string) (uintptr, error) {
	name, err := windows.UTF16PtrFromString(className)
	if err != nil {
		return 0, err
	}
	hwnd, _, err := findWindowProc.Call(uintptr(unsafe.Pointer(name)), 0)
	if hwnd == 0 {
		return 0, winError("找不到应用窗口", err)
	}
	var pid uint32
	getWindowPIDProc.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != uint32(os.Getpid()) {
		return 0, errors.New("应用窗口不属于当前进程")
	}
	return hwnd, nil
}

func monitorDetails(handle uintptr) (monitorInfo, error) {
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	ok, _, err := getMonitorInfoProc.Call(handle, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return info, winError("无法读取显示器工作区域", err)
	}
	return info, nil
}

func CaptureWindow(className string) (WindowState, error) {
	hwnd, err := applicationWindow(className)
	if err != nil {
		return WindowState{}, err
	}
	p := windowPlacement{Length: uint32(unsafe.Sizeof(windowPlacement{}))}
	ok, _, err := getWindowPlacementProc.Call(hwnd, uintptr(unsafe.Pointer(&p)))
	if ok == 0 {
		return WindowState{}, winError("无法读取窗口位置", err)
	}
	monitor, _, _ := monitorFromWindowProc.Call(hwnd, 2)
	info, err := monitorDetails(monitor)
	if err != nil {
		return WindowState{}, err
	}
	// GetWindowPlacement uses workspace coordinates for ordinary top-level
	// windows. Persist screen coordinates so taskbar changes cannot shift it.
	r := p.NormalPosition
	s := WindowState{
		X: int(r.Left + info.Work.Left - info.Monitor.Left), Y: int(r.Top + info.Work.Top - info.Monitor.Top),
		Width: int(r.Right - r.Left), Height: int(r.Bottom - r.Top),
		Maximized: placementMaximized(p.ShowCmd, p.Flags), Valid: true,
	}
	return s, ValidateWindowState(s)
}

func RestoreWindow(className string, s WindowState) error {
	if !s.Valid {
		return nil
	}
	if err := ValidateWindowState(s); err != nil {
		return err
	}
	hwnd, err := applicationWindow(className)
	if err != nil {
		return err
	}
	r := winRect{int32(s.X), int32(s.Y), int32(s.X + s.Width), int32(s.Y + s.Height)}
	monitor, _, _ := monitorFromRectProc.Call(uintptr(unsafe.Pointer(&r)), 2)
	info, err := monitorDetails(monitor)
	if err != nil {
		return err
	}
	dpi := uintptr(96)
	if getDPIForWindowProc.Find() == nil {
		if value, _, _ := getDPIForWindowProc.Call(hwnd); value != 0 {
			dpi = value
		}
	}
	s = fitWindow(s, workArea{int(info.Work.Left), int(info.Work.Top), int(info.Work.Right), int(info.Work.Bottom)}, 760*int(dpi)/96, 560*int(dpi)/96)
	dx, dy := int(info.Work.Left-info.Monitor.Left), int(info.Work.Top-info.Monitor.Top)
	p := windowPlacement{Length: uint32(unsafe.Sizeof(windowPlacement{})), ShowCmd: 1, NormalPosition: winRect{int32(s.X - dx), int32(s.Y - dy), int32(s.X - dx + s.Width), int32(s.Y - dy + s.Height)}}
	if s.Maximized {
		p.ShowCmd = 3
	}
	ok, _, err := setWindowPlacementProc.Call(hwnd, uintptr(unsafe.Pointer(&p)))
	if ok == 0 {
		return winError("无法恢复窗口位置", err)
	}
	return nil
}

func FocusWindow(className string) {
	if hwnd, err := applicationWindow(className); err == nil {
		setForegroundWindowProc.Call(hwnd)
	}
}

func ShowWindowError(className, message string) {
	hwnd, _ := applicationWindow(className)
	text, _ := windows.UTF16PtrFromString(message)
	title, _ := windows.UTF16PtrFromString("Luma · 状态保存提示")
	messageBoxProc.Call(hwnd, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x10)
}

func SetWindowIcon(className string, icon []byte) error {
	hwnd, err := applicationWindow(className)
	if err != nil {
		return err
	}
	large, err := loadEmbeddedIcon(icon, 48)
	if err != nil {
		return err
	}
	small, err := loadEmbeddedIcon(icon, 16)
	if err != nil {
		destroyIconProc.Call(large)
		return err
	}
	sendMessageProc.Call(hwnd, 0x80, 1, large)
	sendMessageProc.Call(hwnd, 0x80, 0, small)
	windowIconsMu.Lock()
	windowIcons = append(windowIcons, large, small)
	windowIconsMu.Unlock()
	return nil
}

// ReleaseWindowIcons is called after the native main window has been destroyed.
func ReleaseWindowIcons() {
	windowIconsMu.Lock()
	defer windowIconsMu.Unlock()
	for _, icon := range windowIcons {
		destroyIconProc.Call(icon)
	}
	windowIcons = nil
}

func DefaultConfigDirectory() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_RoamingAppData, 0)
}
