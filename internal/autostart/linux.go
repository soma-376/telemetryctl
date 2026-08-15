package autostart

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/your-org/pulsemetry/internal/config"
)

// linuxBackend 는 systemd user unit 등록이다 (`systemctl --user`).
//
// 시스템 유닛이 아닌 이유는 패키지 주석 「1. 사용자 수준 서비스만 쓴다」에 있다.
// `loginctl enable-linger` 를 켜지 않는 이유는 「4. linger 를 켜지 않는다」에 있다.
type linuxBackend struct {
	opts Options
}

func (b *linuxBackend) kind() Kind       { return KindSystemdUser }
func (b *linuxBackend) unitPath() string { return UnitPath(b.opts.Env) }

// logPath 는 빈 문자열이다. stdout/stderr 는 journald 로 가고 로테이션도 journald 몫이다.
func (b *linuxBackend) logPath() string { return "" }

// JournalCommand 는 사용자에게 안내할 로그 조회 명령이다.
const JournalCommand = "journalctl --user -u " + UnitName + " -n 100"

// detect 는 systemd 사용자 관리자를 싸게→비싸게 감지한다.
//
// 이 함수는 1·2단계만 한다. 3단계(`systemctl --user daemon-reload` 로 실제 버스 연결
// 확인)는 파일을 쓴 **뒤에** enable 이 직접 한다 — 그러지 않으면 daemon-reload 를 두 번
// 돌게 되고, 어차피 등록의 첫 단계가 그것이기 때문이다.
//
// 셋 다 ErrNoServiceManager 를 감싸되 **고치는 방법이 다르므로 메시지가 다르다.**
// "systemd 가 없습니다" 한 줄로 뭉뚱그리면 WSL 사용자는 무엇을 해야 하는지 알 수 없다.
func (b *linuxBackend) detect() error {
	// 1. 표준 sd_booted() 체크. 없으면 systemd 가 PID 1 이 아니다.
	if _, err := b.opts.stat("/run/systemd/system"); err != nil {
		if b.opts.Env.IsWSL {
			return fmt.Errorf("%w: WSL 에서 systemd 가 켜져 있지 않습니다."+
				" /etc/wsl.conf 에 [boot] systemd=true 를 넣고 `wsl --shutdown` 후 다시 시도하세요",
				ErrNoServiceManager)
		}
		return fmt.Errorf("%w: 이 호스트의 init 이 systemd 가 아닙니다"+
			" (컨테이너이거나 비-systemd 배포판입니다)", ErrNoServiceManager)
	}
	// 2. systemctl 이 설치돼 있는가. 1번이 통과했는데 여기서 걸리는 것은 드물지만,
	//    걸리면 원인이 완전히 다르므로 따로 말한다.
	if _, err := b.opts.lookPath("systemctl"); err != nil {
		return fmt.Errorf("%w: PATH 에서 systemctl 을 찾지 못했습니다: %v", ErrNoServiceManager, err)
	}
	return nil
}

// busError 는 3단계 감지 실패다. PID 1 이 systemd 여도 사용자 관리자가 없을 수 있다
// (`sudo`·`su -` 로 들어와 XDG_RUNTIME_DIR 이 없는 세션).
func busError(code int, stderr string) error {
	return fmt.Errorf("%w: systemd 사용자 관리자에 연결하지 못했습니다"+
		" (XDG_RUNTIME_DIR 이 없는 세션일 수 있습니다). 로그인 세션에서 다시 실행하세요"+
		" — systemctl --user daemon-reload: exit %d: %s", ErrNoServiceManager, code, oneLine(stderr))
}

func (b *linuxBackend) enable(ctx context.Context, execPath string, args []string) (Result, error) {
	unitPath := b.unitPath()
	res := Result{Kind: KindSystemdUser, UnitPath: unitPath, ExecPath: execPath}

	if err := b.detect(); err != nil {
		return res, err
	}

	content := renderUnit(unitParams{ExecPath: execPath, Args: args})
	if err := config.AtomicWriteFile(unitPath, content, 0o644); err != nil {
		return res, fmt.Errorf("unit 기록 실패 %s: %w", unitPath, err)
	}

	// 3단계 감지이자 등록의 첫 단계다. 여기서 실패하면 방금 쓴 파일을 지운다 —
	// systemd 가 영원히 읽지 않을 파일을 남기면 다음 status 가 "등록됨" 이라고 거짓말한다.
	if _, stderr, code, err := b.opts.Runner.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil || code != 0 {
		_ = os.Remove(unitPath)
		if err != nil {
			return res, fmt.Errorf("systemctl 실행 실패: %w", err)
		}
		return res, busError(code, stderr)
	}

	_, stderr, code, err := b.opts.Runner.Run(ctx, "systemctl", "--user", "enable", "--now", UnitName)
	if err != nil {
		return res, fmt.Errorf("systemctl 실행 실패: %w", err)
	}
	if code != 0 {
		return res, fmt.Errorf("systemctl --user enable --now %s 실패 (exit %d): %s",
			UnitName, code, oneLine(stderr))
	}

	res.Notes = append(res.Notes,
		"로그: "+JournalCommand,
		"로그아웃하면 데몬도 함께 종료됩니다 (launchd LaunchAgent 와 같은 의미론입니다)."+
			" 로그아웃 후에도 유지하려면 `loginctl enable-linger $USER` 를 직접 실행하세요.")
	return res, nil
}

func (b *linuxBackend) disable(ctx context.Context) (Result, error) {
	unitPath := b.unitPath()
	res := Result{Kind: KindSystemdUser, UnitPath: unitPath}

	// systemd 가 없는 호스트에서도 남은 유닛 파일은 치울 수 있어야 한다.
	// 그때는 systemctl 을 부르지 않고 파일만 지운다.
	managed := b.detect() == nil
	if managed {
		// **순서가 중요하다.** 파일이 있어야 systemd 가 유닛을 찾아 정지하고
		// default.target.wants/ 의 심볼릭 링크를 지운다. 파일을 먼저 지우면 링크가 남는다.
		_, stderr, code, err := b.opts.Runner.Run(ctx, "systemctl", "--user", "disable", "--now", UnitName)
		if err != nil {
			return res, fmt.Errorf("systemctl 실행 실패: %w", err)
		}
		if code != 0 {
			// 등록된 적이 없으면 실패하는 것이 정상이다. 파일 삭제로 진행한다.
			res.Notes = append(res.Notes,
				fmt.Sprintf("참고: systemctl --user disable 이 exit %d 로 끝났습니다 (%s)", code, oneLine(stderr)))
		}
	} else {
		res.Notes = append(res.Notes, "참고: systemd 사용자 관리자가 없어 유닛 파일만 정리했습니다.")
	}

	removeErr := os.Remove(unitPath)
	switch {
	case removeErr == nil:
	case errors.Is(removeErr, fs.ErrNotExist):
		res.AlreadyInState = true
	default:
		return res, fmt.Errorf("unit 삭제 실패 %s: %w", unitPath, removeErr)
	}

	if managed {
		// 지운 유닛을 systemd 가 계속 알고 있으면 status 가 유령을 보고한다.
		_, _, _, _ = b.opts.Runner.Run(ctx, "systemctl", "--user", "daemon-reload")
	}
	return res, nil
}

func (b *linuxBackend) status(ctx context.Context) (Status, error) {
	unitPath := b.unitPath()
	st := Status{Kind: KindSystemdUser, UnitPath: unitPath}

	switch _, err := os.Stat(unitPath); {
	case err == nil:
		st.Registered = true
		p, readErr := readUnitExecPath(unitPath)
		if readErr != nil {
			st.Detail = appendDetail(st.Detail, fmt.Sprintf("unit 파일을 해석하지 못했습니다: %v", readErr))
		} else {
			st.RegisteredExecPath = p
		}
	case errors.Is(err, fs.ErrNotExist):
		// 미등록은 상태이지 오류가 아니다.
	default:
		return st, fmt.Errorf("unit 파일 확인 실패 %s: %w", unitPath, err)
	}

	// systemd 부재는 오류가 아니라 상태다. status 는 어떤 환경에서도 멈추면 안 된다.
	if err := b.detect(); err != nil {
		st.Detail = appendDetail(st.Detail, err.Error())
		return st, nil
	}
	st.Supported = true

	// is-enabled 는 disabled·not-found 에서 **종료 코드가 0 이 아니지만 상태 단어를
	// stdout 으로 출력한다.** 그래서 종료 코드가 아니라 stdout 을 읽는다.
	enabled, _, _, err := b.opts.Runner.Run(ctx, "systemctl", "--user", "is-enabled", UnitName)
	if err != nil {
		st.Detail = appendDetail(st.Detail, fmt.Sprintf("systemctl 을 실행하지 못했습니다: %v", err))
		return st, nil
	}
	switch word := strings.TrimSpace(enabled); word {
	case "enabled", "enabled-runtime":
		st.Loaded = true
	case "", "not-found", "disabled":
		// 유닛을 모르거나 로그인 시 시작하지 않는다. 파일만 있고 daemon-reload 전인
		// 상태가 여기 온다.
	default:
		st.Detail = appendDetail(st.Detail, "systemd 상태: "+word)
	}

	active, _, _, err := b.opts.Runner.Run(ctx, "systemctl", "--user", "is-active", UnitName)
	if err == nil && strings.TrimSpace(active) == "active" {
		st.Running = true
	}

	// --value 는 systemd ≥230 을 요구하므로 평범한 Key=Value 형태로 받아 파싱한다.
	// `systemctl status` 는 절대 파싱하지 않는다 — 사람용 출력이라 형식이 자유롭다.
	show, _, _, err := b.opts.Runner.Run(ctx, "systemctl", "--user", "show",
		"-p", "MainPID", "-p", "ExecMainStatus", "-p", "FragmentPath", "-p", "NRestarts", UnitName)
	if err == nil {
		parseSystemctlShow(show, &st)
	}
	return st, nil
}

func parseSystemctlShow(out string, st *Status) {
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "MainPID":
			if v, err := strconv.Atoi(value); err == nil && v > 0 {
				st.PID = v
			}
		case "ExecMainStatus":
			if v, err := strconv.Atoi(value); err == nil {
				st.LastExit = v
			}
		case "NRestarts":
			if v, err := strconv.Atoi(value); err == nil {
				st.Restarts = v
			}
		}
	}
}

// unitParams 는 renderUnit 이 받는 값이다.
type unitParams struct {
	ExecPath string
	Args     []string
}

// renderUnit 은 systemd user unit 파일을 만든다.
//
// 주석을 파일 안에 남기는 것은 사용자가 이 파일을 직접 열어 볼 것이기 때문이다.
// 특히 **샌드박싱 지시자를 넣지 않은 이유**는 반드시 파일에 남아야 한다 — 나중에 누군가
// "보안 강화" 로 친절하게 ProtectHome 을 추가하면 데몬이 ~/.pulsemetry 를 못 읽어 조용히
// 죽는다.
func renderUnit(p unitParams) []byte {
	var b bytes.Buffer

	b.WriteString("# telemetryctl 이 생성한 파일입니다 (telemetryctl autostart enable).\n")
	b.WriteString("# 직접 고치지 마세요 — autostart enable 이 다시 쓸 때 덮어씁니다.\n")
	b.WriteString("\n[Unit]\n")
	b.WriteString("Description=pulsemetry 로컬 텔레메트리 데몬\n")
	b.WriteString("Documentation=https://github.com/your-org/pulsemetry\n")
	b.WriteString("After=default.target\n")
	b.WriteString("# 미enroll 상태(state.json 없음)에서 daemon.Run 이 즉시 실패하므로 무한 재시작을 막는다.\n")
	b.WriteString("# systemd v229 부터 이 두 키는 [Service] 가 아니라 [Unit] 에 있어야 한다.\n")
	fmt.Fprintf(&b, "StartLimitIntervalSec=%d\n", StartLimitIntervalSec)
	fmt.Fprintf(&b, "StartLimitBurst=%d\n", StartLimitBurst)

	b.WriteString("\n[Service]\n")
	b.WriteString("Type=simple\n")
	fmt.Fprintf(&b, "ExecStart=%s\n", unitCommandLine(p.ExecPath, p.Args))
	b.WriteString("WorkingDirectory=%h\n")
	b.WriteString("# 비정상 종료일 때만 재시작한다 (ADR 0007). SIGTERM 은 main.go 의\n")
	b.WriteString("# signal.NotifyContext 가 잡아 종료 코드 0 이 되므로 systemctl --user stop 은\n")
	b.WriteString("# 정지 상태를 유지한다.\n")
	b.WriteString("Restart=on-failure\n")
	fmt.Fprintf(&b, "RestartSec=%d\n", int(RestartSec.Seconds()))
	b.WriteString("# daemon.DefaultShutdownTimeout(15s)보다 반드시 커야 한다. 작으면 systemd 가 flush\n")
	b.WriteString("# 도중 SIGKILL 을 보내 미저장 집계를 잃고 runtime.json 이 남는다.\n")
	fmt.Fprintf(&b, "TimeoutStopSec=%d\n", int(TimeoutStopSec.Seconds()))
	b.WriteString("KillSignal=SIGTERM\n")
	fmt.Fprintf(&b, "SyslogIdentifier=%s\n", syslogIdentifier)
	b.WriteString("# 샌드박싱 지시자(ProtectHome·PrivateTmp·ProtectSystem)를 넣지 않는다.\n")
	b.WriteString("# ProtectHome 은 ~/.pulsemetry 를 통째로 가리고, PrivateTmp 와 D-Bus 제한은\n")
	b.WriteString("# go-keyring 의 Secret Service 접근을 깬다. 둘 다 데몬이 뜨지 못하는 결과가 된다.\n")

	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return b.Bytes()
}

// unitCommandLine 은 ExecStart 값을 만든다.
func unitCommandLine(execPath string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteUnitArg(execPath))
	for _, a := range args {
		parts = append(parts, quoteUnitArg(a))
	}
	return strings.Join(parts, " ")
}

// quoteUnitArg 는 systemd 명령줄 인자를 안전하게 감싼다.
//
// systemd 는 ExecStart 값을 공백으로 쪼개므로 공백이 든 경로(`/Users/a b/telemetryctl`)를
// 그대로 적으면 인자 두 개가 된다. 큰따옴표로 감싸고 내부의 `"`·`\` 를 이스케이프한다.
func quoteUnitArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\"'\\$;") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// readUnitExecPath 는 등록된 unit 파일에서 ExecStart 의 첫 인자를 되읽는다 (드리프트 판정).
//
// systemd 의 FragmentPath 가 아니라 **우리 파일을 직접 읽는다.** 그래야 두 플랫폼에서
// 같은 로직이 되고 서비스 관리자가 없는 호스트에서도 답할 수 있다.
func readUnitExecPath(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // 읽기 전용 핸들이다

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		value, ok := strings.CutPrefix(line, "ExecStart=")
		if !ok {
			continue
		}
		// 우리는 쓰지 않지만 사람이 손으로 붙였을 수 있는 접두사들이다.
		value = strings.TrimLeft(value, "-@+!:")
		return firstUnitArg(value), nil
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", nil
}

// firstUnitArg 는 ExecStart 값에서 첫 인자를 꺼낸다. quoteUnitArg 의 짝이다.
func firstUnitArg(s string) string {
	var b strings.Builder
	inQuote := false
	escaped := false
	for _, r := range strings.TrimLeft(s, " \t") {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case (r == ' ' || r == '\t') && !inQuote:
			return b.String()
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
