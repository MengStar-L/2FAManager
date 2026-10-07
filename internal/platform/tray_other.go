//go:build !windows

package platform

type Tray struct{}

func NewTray([]byte, func(), func(), func()) (*Tray, error) { return nil, ErrUnsupported }
func (*Tray) Ready() bool                                   { return false }
func (*Tray) Close()                                        {}
