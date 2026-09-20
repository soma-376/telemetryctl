// Package instancelock은 데몬과 제거 작업의 데이터 디렉터리 사용을 직렬화한다.
package instancelock

import (
	"fmt"
	"os"
	"path/filepath"
)

type Lock struct{ file *os.File }

// Acquire는 기다리지 않는다. 파일은 재사용하므로 잠금 해제 후에도 삭제하지 않는다.
func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, ".pulsemetry.lock")
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("잠금 파일이 일반 파일이 아니다")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = lockFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("다른 데몬 또는 제거 작업이 데이터 디렉터리를 사용 중이다: %w", err)
	}
	return &Lock{f}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
