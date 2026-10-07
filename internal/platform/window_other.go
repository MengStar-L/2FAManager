//go:build !windows

package platform

import "os"

func CaptureWindow(string) (WindowState, error) { return WindowState{}, ErrUnsupported }
func RestoreWindow(string, WindowState) error   { return ErrUnsupported }
func FocusWindow(string)                        {}
func ShowWindowError(string, string)            {}
func SetWindowIcon(string, []byte) error        { return ErrUnsupported }
func EnableRoundedWindow(string) error          { return ErrUnsupported }
func ReleaseWindowIcons()                       {}
func DefaultConfigDirectory() (string, error)   { return os.UserConfigDir() }
