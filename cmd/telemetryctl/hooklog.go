package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const hookLogMaxBytes = 1 << 20

// hookDiagnostic 은 훅 프로세스의 진입과 실패 단계를 남긴다. 로깅 실패도 fail-open이다.
// 원본 오류·인자·페이로드는 받지 않는다. 크기 초과 시 오래된 진단 내용을 비운다.
type hookDiagnostic struct {
	path    string
	started time.Time
}

func newHookDiagnostic(dataDir string) *hookDiagnostic {
	return &hookDiagnostic{path: filepath.Join(dataDir, "hook-bridge.log"), started: time.Now()}
}

func (d *hookDiagnostic) record(stage, detail string) {
	if d == nil {
		return
	}
	if len(detail) > 512 {
		detail = detail[:512]
	}
	line := fmt.Sprintf("%s pid=%d elapsed_ms=%d stage=%s detail=%q\n",
		time.Now().UTC().Format(time.RFC3339Nano), os.Getpid(), time.Since(d.started).Milliseconds(), stage, detail)
	if err := os.MkdirAll(filepath.Dir(d.path), 0700); err != nil {
		return
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(d.path); err == nil && info.Size()+int64(len(line)) > hookLogMaxBytes {
		// Windows의 append 전용 핸들은 Truncate할 수 없어 열 때 비운다.
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(d.path, flags, 0600)
	if err != nil {
		return
	}
	defer f.Close() //nolint:errcheck
	_, _ = f.WriteString(line)
}
