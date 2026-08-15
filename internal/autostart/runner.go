package autostart

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Runner 는 서비스 관리자 CLI 호출 한 번이다.
//
// 이 인터페이스가 이 패키지의 주된 테스트 seam 이다. launchctl·systemctl 을 진짜로
// 부르는 테스트는 CI 러너에서 구조적으로 flaky 하다 — macOS 러너의 UID 에는 GUI 로그인
// 세션이 없어 `bootstrap gui/$UID` 가 exit 5 로 실패하고, ubuntu 러너의 사용자에게는
// systemd user manager 도 XDG_RUNTIME_DIR 도 없다. 그래서 명령 **순서와 인자**를
// 단언하는 것이 실제로 검증할 수 있는 최대치이고, 그 단언이 회귀를 잡는다.
//
// 종료 코드는 오류가 아니다. code 로 돌려주고 err 는 실행 자체가 불가능했을 때만 채운다
// (실행 파일 없음·컨텍스트 취소). 호출자가 "이 종료 코드는 허용" 을 판단해야 하기 때문이다 —
// 예를 들어 `launchctl bootout` 의 exit 3("No such process")은 멱등 재등록의 정상 경로다.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, code int, err error)
}

// execRunner 는 운영 경로다.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	err := cmd.Run()
	if err == nil {
		return out.String(), errOut.String(), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out.String(), errOut.String(), exitErr.ExitCode(), nil
	}
	// 실행 자체가 안 됐다. -1 은 "종료 코드가 없다" 는 뜻이다.
	return out.String(), errOut.String(), -1, err
}

// execLookPath 는 Options.LookPath 의 기본 구현이다.
func execLookPath(name string) (string, error) { return exec.LookPath(name) }

// oneLine 은 서비스 관리자의 stderr 를 오류 메시지에 실을 수 있는 한 줄로 만든다.
//
// 여러 줄 stderr 를 그대로 오류에 넣으면 CLI 출력이 무너진다. 상한을 두는 것은
// launchctl 이 가끔 도움말 전체를 뱉기 때문이다.
func oneLine(s string) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if s == "" {
		return "출력 없음"
	}
	const max = 300
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// appendDetail 은 Status.Detail 에 사유를 덧붙인다. Detail 은 사람이 읽는 한 줄이라
// 여러 사유가 겹치면 " · " 로 잇는다.
func appendDetail(existing, add string) string {
	add = strings.TrimSpace(add)
	switch {
	case add == "":
		return existing
	case existing == "":
		return add
	default:
		return existing + " · " + add
	}
}
