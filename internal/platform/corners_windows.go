//go:build windows

package platform

import (
	"errors"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const windowProcedureIndex = ^uintptr(3) // GWLP_WNDPROC (-4)

var (
	getWindowLongPointerProc = pointerWindowProc("GetWindowLong")
	setWindowLongPointerProc = pointerWindowProc("SetWindowLong")
	callWindowProcedureProc  = user32.NewProc("CallWindowProcW")
	getWindowRectProc        = user32.NewProc("GetWindowRect")
	isIconicProc             = user32.NewProc("IsIconic")
	isZoomedProc             = user32.NewProc("IsZoomed")
	setWindowRegionProc      = user32.NewProc("SetWindowRgn")
	createRoundRegionProc    = windows.NewLazySystemDLL("gdi32.dll").NewProc("CreateRoundRectRgn")
	deleteRegionProc         = windows.NewLazySystemDLL("gdi32.dll").NewProc("DeleteObject")
	dwmSetAttributeProc      = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
	roundedWindows           sync.Map // HWND -> *roundedWindow
	roundedCallback          = syscall.NewCallback(roundedWindowProcedure)
	cornerMessageOnce        sync.Once
	cornerMessage            uintptr
)

func pointerWindowProc(name string) *windows.LazyProc {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		name += "Ptr"
	}
	return user32.NewProc(name + "W")
}

type cornerShape struct {
	width, height, radius int
	square                bool
}

type roundedWindow struct {
	original atomic.Uintptr
	callback uintptr
	// These fields are used only on the owning window thread. In particular,
	// no mutex is held while calling Wails or synchronous Win32 functions.
	applying bool
	applied  bool
	shape    cornerShape
}

// EnableRoundedWindow clips the actual HWND, including its child WebView. Wails
// removes the non-client area, so DWM's round-corner hint alone is not reliable.
// A region guarantees the same borderless outline on Windows 10 and 11.
func EnableRoundedWindow(className string) error {
	hwnd, err := applicationWindow(className)
	if err != nil {
		return err
	}
	cornerMessageOnce.Do(func() {
		name, _ := windows.UTF16PtrFromString("Luma.RoundedWindow.Refresh")
		cornerMessage, _, _ = registerWindowMessageProc.Call(uintptr(unsafe.Pointer(name)))
	})
	if cornerMessage == 0 {
		return errors.New("无法注册窗口圆角更新消息")
	}
	old, _, err := getWindowLongPointerProc.Call(hwnd, windowProcedureIndex)
	if old == 0 {
		return winError("无法读取窗口消息处理器", err)
	}
	c := &roundedWindow{callback: roundedCallback}
	c.original.Store(old)
	if _, loaded := roundedWindows.LoadOrStore(hwnd, c); loaded {
		return nil
	}
	// Use raw same-process subclassing: the common-controls SetWindowSubclass
	// helper cannot be installed from Wails' background OnDomReady goroutine.
	// Store the original first, because the GUI thread can receive a message
	// as soon as SetWindowLongPtr installs our callback.
	previous, _, err := setWindowLongPointerProc.Call(hwnd, windowProcedureIndex, c.callback)
	if previous == 0 {
		roundedWindows.Delete(hwnd)
		return winError("无法安装窗口圆角处理器", err)
	}
	c.original.Store(previous)
	// Marshal the first update to the native window thread, just like all
	// subsequent resize/DPI updates. SendMessage also completes initialization
	// before the saved placement is restored by the caller.
	ok, _, _ := sendMessageProc.Call(hwnd, cornerMessage, 0, 0)
	if ok == 0 {
		return errors.New("无法设置窗口圆角")
	}
	return nil
}

func roundedWindowProcedure(hwnd, message, wparam, lparam uintptr) uintptr {
	entry, ok := roundedWindows.Load(hwnd)
	if !ok {
		result, _, _ := defWindowProc.Call(hwnd, message, wparam, lparam)
		return result
	}
	c := entry.(*roundedWindow)
	if message == cornerMessage {
		if c.update(hwnd) == nil {
			return 1
		}
		return 0
	}
	if message == 0x82 { // WM_NCDESTROY: detach while the HWND is still valid.
		current, _, _ := getWindowLongPointerProc.Call(hwnd, windowProcedureIndex)
		if current == c.callback {
			setWindowLongPointerProc.Call(hwnd, windowProcedureIndex, c.original.Load())
		}
		result, _, _ := callWindowProcedureProc.Call(c.original.Load(), hwnd, message, wparam, lparam)
		roundedWindows.Delete(hwnd)
		return result
	}
	// Wails retains ownership of hit testing, resize gestures, client sizing,
	// DPI suggestions, and fullscreen styles. Refresh only after its handler.
	result, _, _ := callWindowProcedureProc.Call(c.original.Load(), hwnd, message, wparam, lparam)
	switch message {
	case 0x5, 0x47, 0x7d, 0x2e0, 0x7e: // SIZE, WINDOWPOSCHANGED, STYLECHANGED, DPICHANGED, DISPLAYCHANGE
		_ = c.update(hwnd)
	case 0x31e: // WM_DWMCOMPOSITIONCHANGED
		c.applied = false
		_ = c.update(hwnd)
	}
	return result
}

func (c *roundedWindow) update(hwnd uintptr) error {
	// SetWindowRgn synchronously sends window-position messages. Keep those
	// nested callbacks from allocating another region or recursing forever.
	if c.applying {
		return nil
	}
	c.applying = true
	defer func() { c.applying = false }()
	if minimized, _, _ := isIconicProc.Call(hwnd); minimized != 0 {
		return nil
	}
	var bounds winRect
	if ok, _, err := getWindowRectProc.Call(hwnd, uintptr(unsafe.Pointer(&bounds))); ok == 0 {
		return winError("无法读取窗口轮廓", err)
	}
	width, height := int(bounds.Right-bounds.Left), int(bounds.Bottom-bounds.Top)
	if width <= 0 || height <= 0 {
		return nil
	}
	maximized, _, _ := isZoomedProc.Call(hwnd)
	square := maximized != 0
	if !square {
		monitor, _, _ := monitorFromWindowProc.Call(hwnd, 2)
		if info, err := monitorDetails(monitor); err == nil {
			// This is also how Wails identifies a fullscreen native rectangle.
			square = bounds == info.Monitor
		}
	}
	dpi := 96
	if getDPIForWindowProc.Find() == nil {
		if value, _, _ := getDPIForWindowProc.Call(hwnd); value != 0 {
			dpi = int(value)
		}
	}
	shape := cornerShape{width: width, height: height, radius: cornerRadius(width, height, dpi), square: square}
	if c.applied && c.shape == shape {
		return nil
	}
	setDWMCornerPreference(hwnd, square)
	var region uintptr
	if !square {
		var err error
		region, err = newCornerRegion(shape)
		if err != nil {
			return err
		}
	}
	ok, _, err := setWindowRegionProc.Call(hwnd, region, 1)
	if ok == 0 {
		if region != 0 {
			deleteRegionProc.Call(region)
		}
		return winError("无法更新窗口圆角", err)
	}
	// On success Windows owns the region and deletes it on replacement or
	// window destruction. Deleting it here would corrupt the live outline.
	c.shape, c.applied = shape, true
	return nil
}

func cornerRadius(width, height, dpi int) int {
	if dpi <= 0 {
		dpi = 96
	}
	return min(max(1, (12*dpi+48)/96), max(1, min(width, height)/2))
}

func newCornerRegion(shape cornerShape) (uintptr, error) {
	// GDI's rounded rectangle excludes the final right/bottom scan line;
	// include one extra coordinate to retain the window's last pixel row.
	region, _, err := createRoundRegionProc.Call(0, 0, uintptr(shape.width+1), uintptr(shape.height+1), uintptr(shape.radius*2), uintptr(shape.radius*2))
	if region == 0 {
		return 0, winError("无法创建窗口圆角", err)
	}
	return region, nil
}

func setDWMCornerPreference(hwnd uintptr, square bool) {
	if dwmSetAttributeProc.Find() != nil {
		return
	}
	preference := uint32(2) // DWMWCP_ROUND
	if square {
		preference = 1 // DWMWCP_DONOTROUND
	}
	border := uint32(0xfffffffe) // DWMWA_COLOR_NONE: never add native borders.
	// These attributes are Windows 11 hints. Older versions may reject them;
	// the region remains the authoritative outline on every supported version.
	dwmSetAttributeProc.Call(hwnd, 33, uintptr(unsafe.Pointer(&preference)), 4)
	dwmSetAttributeProc.Call(hwnd, 34, uintptr(unsafe.Pointer(&border)), 4)
}
