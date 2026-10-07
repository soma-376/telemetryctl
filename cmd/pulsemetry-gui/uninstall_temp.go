package main

import (
	"os"
	"path/filepath"
	"strings"
)

// 생성한 임시 실행 파일 하나와 빈 디렉터리만 정리한다.
func cleanupFinalizer() {
	exe, err := removalExecutable()
	if err != nil {
		return
	}
	dir := filepath.Dir(exe)
	temp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil || parent != temp || !strings.HasPrefix(filepath.Base(dir), "pulsemetry-uninstall-") {
		return
	}
	if name := filepath.Base(exe); name != "finish" && name != "finish.exe" && name != "finish.AppImage" {
		return
	}
	removeTemporaryExecutable(exe, dir)
}
