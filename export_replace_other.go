//go:build !windows

package main

import "os"

func replaceExportFile(source, destination string) error { return os.Rename(source, destination) }
