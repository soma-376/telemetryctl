package codexapp

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// resolveExecutable은 PATH를 우선하며 macOS 자동 시작 환경의 기본 설치 경로를 보완한다.
// 셸 설정과 인증 파일을 읽지 않고 실행 권한도 변경하지 않는다.
func resolveExecutable() (string, error) {
	return resolveExecutableFor(runtime.GOOS, exec.LookPath, executablePath)
}

func resolveExecutableFor(goos string, lookup func(string) (string, error), validate func(string) (string, error)) (string, error) {
	path, err := lookup("codex")
	if err == nil {
		return validate(path)
	}
	// 권한 오류나 현재 디렉터리 실행 거부를 다른 설치본으로 우회하지 않는다.
	if !errors.Is(err, exec.ErrNotFound) {
		return "", err
	}
	if goos == "darwin" {
		for _, candidate := range []string{"/opt/homebrew/bin/codex", "/usr/local/bin/codex"} {
			resolved, candidateErr := validate(candidate)
			if candidateErr == nil {
				return resolved, nil
			}
			if !errors.Is(candidateErr, os.ErrNotExist) {
				return "", candidateErr
			}
		}
	}
	return "", exec.ErrNotFound
}

func executablePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", os.ErrPermission
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return "", os.ErrPermission
	}
	return absolute, nil
}
