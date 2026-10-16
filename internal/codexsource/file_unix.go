//go:build !windows

package codexsource

import (
	"fmt"
	"os"
	"syscall"
)

func openJSONL(path string) (*os.File, error) { return os.Open(path) }

func osFileID(f *os.File) (string, error) {
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("file ID unavailable")
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
