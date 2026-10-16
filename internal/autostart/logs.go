package autostart

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/your-org/pulsemetry/internal/hostenv"
)

// MaxLogBytes 는 launchd 로그 파일 하나의 상한이다.
//
// 정상 상태의 데몬 로그는 분당 몇 줄이지만, 30초 스로틀 크래시 루프는 하루 수천 줄이다.
// 상한을 두지 않으면 그 상태가 디스크를 조용히 먹는다.
const MaxLogBytes = 16 << 20

// RotateLogs 는 자동 실행 로그가 상한을 넘으면 회전한다.
//
// **LogDir(env) 가 비면 아무 일도 하지 않는다.** 리눅스는 journald 가, windows 는 아직
// 아무도 로그를 관리하지 않는다. 이 no-op 판단이 이 패키지 안에 있기 때문에
// internal/daemon 은 플랫폼을 전혀 알 필요가 없다.
//
// # 왜 rename 이 아니라 copy-truncate 인가
//
// launchd 가 StandardOutPath·StandardErrorPath 의 fd 를 **직접 잡고 있다.** rename 기반
// 로테이션은 여기서 깨진다 — 이름만 바뀌고 데몬은 계속 같은 inode 에 쓰므로 `.1` 파일이
// 무한히 자라고 새 파일은 영원히 비어 있다. copy-truncate 는 inode 를 보존하므로 fd 가
// 유효한 채로 파일만 짧아진다.
//
// 대가는 복사와 truncate 사이에 쓰인 몇 줄을 잃을 수 있다는 것이고, 크래시 진단 로그에
// 대해 그것은 받아들일 만하다. `.1` 은 하나만 보관한다.
func RotateLogs(env hostenv.Env) error {
	return rotateLogsIn(LogDir(env), MaxLogBytes)
}

func rotateLogsIn(dir string, limit int64) error {
	if dir == "" {
		return nil
	}
	var errs []error
	for _, name := range []string{logFileName, errLogFileName} {
		if err := rotateOne(filepath.Join(dir, name), limit); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func rotateOne(path string, limit int64) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // 아직 아무것도 안 썼다. 정상이다.
	}
	if err != nil {
		return fmt.Errorf("로그 확인 실패 %s: %w", path, err)
	}
	if info.Size() <= limit {
		return nil
	}

	src, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("로그 열기 실패 %s: %w", path, err)
	}
	defer src.Close() //nolint:errcheck // 읽기 전용 핸들이다

	backup := path + ".1"
	dst, err := os.OpenFile(backup, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("로그 사본 생성 실패 %s: %w", backup, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("로그 복사 실패 %s: %w", backup, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("로그 사본 닫기 실패 %s: %w", backup, err)
	}

	// 여기서만 원본을 건드린다. 사본이 온전해진 뒤여야 한다.
	if err := os.Truncate(path, 0); err != nil {
		return fmt.Errorf("로그 비우기 실패 %s: %w", path, err)
	}
	return nil
}
