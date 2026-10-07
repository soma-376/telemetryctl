//go:build !windows

package main

import "os"

func removeTemporaryExecutable(exe, dir string) {
	_ = os.Remove(exe)
	_ = os.Remove(dir)
}
