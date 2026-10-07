package platform

import "errors"

// WindowState describes the normal window rectangle in physical screen pixels.
// Maximized is independent of this rectangle, including while minimized.
type WindowState struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized"`
	Valid     bool `json:"valid"`
}

func ValidateWindowState(s WindowState) error {
	if !s.Valid {
		return nil
	}
	if s.Width < 100 || s.Height < 100 || s.Width > 32768 || s.Height > 32768 || s.X < -1000000 || s.X > 1000000 || s.Y < -1000000 || s.Y > 1000000 {
		return errors.New("窗口位置或大小无效")
	}
	return nil
}

type workArea struct{ left, top, right, bottom int }

func placementMaximized(show, flags uint32) bool {
	return show == 3 || (show == 2 || show == 6 || show == 7) && flags&2 != 0
}

func fitWindow(s WindowState, area workArea, minimumWidth, minimumHeight int) WindowState {
	availableWidth, availableHeight := area.right-area.left, area.bottom-area.top
	if availableWidth <= 0 || availableHeight <= 0 {
		return s
	}
	s.Width = min(max(s.Width, minimumWidth), availableWidth)
	s.Height = min(max(s.Height, minimumHeight), availableHeight)
	s.X = min(max(s.X, area.left), area.right-s.Width)
	s.Y = min(max(s.Y, area.top), area.bottom-s.Height)
	return s
}
