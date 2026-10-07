//go:build !windows

package updateinstall

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("自动安装更新目前仅支持 Windows")

func Start(context.Context, string, string, bool) (*Job, error) { return nil, errUnsupported }
func ReadLastResult() (*Result, error)                          { return nil, nil }
func HandleHelper(args []string) (bool, error) {
	if len(args) > 0 && args[0] == helperArgument {
		return true, errUnsupported
	}
	return false, nil
}
