//go:build !windows

package vault

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
