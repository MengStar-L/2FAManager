package platform

import (
	"encoding/binary"
	"testing"
)

func TestWindowPlacementFitsChangedMonitorLayout(t *testing.T) {
	area := workArea{left: -1920, top: 40, right: 0, bottom: 1080}
	s := fitWindow(WindowState{X: 5000, Y: -500, Width: 4000, Height: 3000, Maximized: true, Valid: true}, area, 760, 560)
	if s.X != -1920 || s.Y != 40 || s.Width != 1920 || s.Height != 1040 || !s.Maximized {
		t.Fatalf("offscreen restoration: %+v", s)
	}
	s = fitWindow(WindowState{X: -1800, Y: 80, Width: 1000, Height: 800, Valid: true}, area, 760, 560)
	if s.X != -1800 || s.Y != 80 || s.Width != 1000 || s.Height != 800 {
		t.Fatal("valid secondary monitor position moved")
	}
	s = fitWindow(s, workArea{0, 0, 640, 480}, 760, 560)
	if s.Width != 640 || s.Height != 480 || s.X != 0 || s.Y != 0 {
		t.Fatal("small screen cannot contain restored window")
	}
}

func TestMaximizedPlacementOnlyUsesRestoreFlagWhileMinimized(t *testing.T) {
	for _, tc := range []struct {
		show, flags uint32
		want        bool
	}{{3, 0, true}, {2, 2, true}, {6, 2, true}, {7, 2, true}, {1, 2, false}, {1, 0, false}, {2, 0, false}} {
		if placementMaximized(tc.show, tc.flags) != tc.want {
			t.Fatalf("wrong placement state: %+v", tc)
		}
	}
}

func TestWindowStateRejectsInvalidBounds(t *testing.T) {
	for _, s := range []WindowState{{Valid: true, Width: 0, Height: 500}, {Valid: true, Width: 500, Height: 32769}, {Valid: true, Width: 900, Height: 700, X: 1000001}} {
		if ValidateWindowState(s) == nil {
			t.Fatal("invalid native window bounds accepted")
		}
	}
}

func TestICOSelectionAndBounds(t *testing.T) {
	data := make([]byte, 6+32+8)
	binary.LittleEndian.PutUint16(data[2:4], 1)
	binary.LittleEndian.PutUint16(data[4:6], 2)
	for i, size := range []byte{16, 48} {
		e := data[6+i*16 : 6+(i+1)*16]
		e[0], e[1] = size, size
		binary.LittleEndian.PutUint32(e[8:12], 4)
		binary.LittleEndian.PutUint32(e[12:16], uint32(38+i*4))
		data[38+i*4] = size
	}
	for _, size := range []int{16, 32, 48} {
		selected, err := iconResource(data, size)
		want := byte(48)
		if size == 16 {
			want = 16
		}
		if err != nil || selected[0] != want {
			t.Fatalf("ICO selection failed for %d", size)
		}
	}
	binary.LittleEndian.PutUint32(data[18:22], 0xffffffff)
	if _, err := iconResource(data, 16); err == nil {
		t.Fatal("out-of-bounds ICO accepted")
	}
	if _, err := iconResource(nil, 16); err == nil {
		t.Fatal("empty ICO accepted")
	}
}
