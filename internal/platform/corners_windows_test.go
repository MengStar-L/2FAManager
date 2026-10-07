//go:build windows

package platform

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestCornerRegionScalesWithoutClippingEdges(t *testing.T) {
	pointInRegion := windows.NewLazySystemDLL("gdi32.dll").NewProc("PtInRegion")
	for _, tc := range []struct{ dpi, radius int }{{96, 12}, {120, 15}, {144, 18}, {192, 24}, {0, 12}} {
		shape := cornerShape{width: 800, height: 600, radius: cornerRadius(800, 600, tc.dpi)}
		if shape.radius != tc.radius {
			t.Fatalf("DPI %d: radius %d, expected %d", tc.dpi, shape.radius, tc.radius)
		}
		region, err := newCornerRegion(shape)
		if err != nil {
			t.Fatal(err)
		}
		for _, point := range []struct {
			x, y int
			in   bool
		}{
			{0, 0, false}, {799, 0, false}, {0, 599, false}, {799, 599, false},
			{400, 0, true}, {400, 599, true}, {0, 300, true}, {799, 300, true}, {400, 300, true},
			{800, 300, false}, {400, 600, false},
		} {
			inside, _, _ := pointInRegion.Call(region, uintptr(point.x), uintptr(point.y))
			if (inside != 0) != point.in {
				deleteRegionProc.Call(region)
				t.Fatalf("DPI %d: point (%d,%d) inclusion %t, expected %t", tc.dpi, point.x, point.y, inside != 0, point.in)
			}
		}
		deleteRegionProc.Call(region)
	}
}

func TestRoundedHiddenWindowResizeFullscreenAndDestroy(t *testing.T) {
	// This window is never shown, activated, or associated with a real vault.
	// Native callbacks execute synchronously on this isolated test thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	className := fmt.Sprintf("LumaCornersTest-%d", os.Getpid())
	name, _ := windows.UTF16PtrFromString(className)
	instance, _, _ := getModuleHandleProc.Call(0)
	class := trayWindowClass{Size: uint32(unsafe.Sizeof(trayWindowClass{})), Instance: instance, WindowProc: defWindowProc.Addr(), ClassName: name}
	if atom, _, err := registerClassExProc.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		t.Fatal(winError("register test window", err))
	}
	defer unregisterClassProc.Call(uintptr(unsafe.Pointer(name)), instance)
	hwnd, _, err := createWindowExProc.Call(0, uintptr(unsafe.Pointer(name)), 0, 0x80000000, 40, 40, 800, 600, 0, 0, instance, 0)
	if hwnd == 0 {
		t.Fatal(winError("create test window", err))
	}
	defer func() {
		if hwnd != 0 {
			destroyWindowProc.Call(hwnd)
		}
	}()
	if err := EnableRoundedWindow(className); err != nil {
		t.Fatal(err)
	}
	if err := EnableRoundedWindow(className); err != nil {
		t.Fatal("duplicate initialization: ", err)
	}
	assertNativeCornerRegion(t, hwnd, 800, 600, true)
	setPosition := user32.NewProc("SetWindowPos")
	move := func(rect winRect) {
		t.Helper()
		if ok, _, err := setPosition.Call(hwnd, 0, uintptr(rect.Left), uintptr(rect.Top), uintptr(rect.Right-rect.Left), uintptr(rect.Bottom-rect.Top), 0x14); ok == 0 {
			t.Fatal(winError("resize test window", err))
		}
	}
	move(winRect{50, 50, 950, 700})
	assertNativeCornerRegion(t, hwnd, 900, 650, true)
	monitor, _, _ := monitorFromWindowProc.Call(hwnd, 2)
	info, err := monitorDetails(monitor)
	if err != nil {
		t.Fatal(err)
	}
	move(info.Monitor)
	assertNativeCornerRegion(t, hwnd, 0, 0, false)
	move(winRect{50, 50, 950, 700})
	assertNativeCornerRegion(t, hwnd, 900, 650, true)
	// Toggle the native maximized style on this hidden fixture; unlike
	// ShowWindow(SW_MAXIMIZE), this cannot flash a test window on the desktop.
	styleIndex := ^uintptr(15) // GWL_STYLE (-16)
	style, _, _ := getWindowLongPointerProc.Call(hwnd, styleIndex)
	setWindowLongPointerProc.Call(hwnd, styleIndex, style|0x1000000) // WS_MAXIMIZE
	assertNativeCornerRegion(t, hwnd, 0, 0, false)
	setWindowLongPointerProc.Call(hwnd, styleIndex, style)
	assertNativeCornerRegion(t, hwnd, 900, 650, true)
	oldHandle := hwnd
	if ok, _, err := destroyWindowProc.Call(hwnd); ok == 0 {
		t.Fatal(winError("destroy test window", err))
	}
	hwnd = 0
	if _, found := roundedWindows.Load(oldHandle); found {
		t.Fatal("destroyed window retained its subclass state")
	}
}

func assertNativeCornerRegion(t *testing.T, hwnd uintptr, width, height int, rounded bool) {
	t.Helper()
	gdi := windows.NewLazySystemDLL("gdi32.dll")
	region, _, _ := gdi.NewProc("CreateRectRgn").Call(0, 0, 0, 0)
	if region == 0 {
		t.Fatal("create inspection region")
	}
	defer deleteRegionProc.Call(region)
	kind, _, _ := user32.NewProc("GetWindowRgn").Call(hwnd, region)
	if !rounded {
		if kind != 0 {
			t.Fatalf("square native window retained region type %d", kind)
		}
		return
	}
	if kind != 3 { // COMPLEXREGION
		t.Fatalf("rounded native window region type %d, expected 3", kind)
	}
	var rect winRect
	gdi.NewProc("GetRgnBox").Call(region, uintptr(unsafe.Pointer(&rect)))
	if rect != (winRect{0, 0, int32(width), int32(height)}) {
		t.Fatalf("native region stale after resize: %+v, expected %d x %d", rect, width, height)
	}
	for _, p := range []winPoint{{0, 0}, {int32(width - 1), 0}, {0, int32(height - 1)}, {int32(width - 1), int32(height - 1)}} {
		if in, _, _ := gdi.NewProc("PtInRegion").Call(region, uintptr(p.X), uintptr(p.Y)); in != 0 {
			t.Fatalf("native corner (%d,%d) is still rectangular", p.X, p.Y)
		}
	}
}
