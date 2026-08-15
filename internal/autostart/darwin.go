package autostart

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/your-org/pulsemetry/internal/config"
)

// darwinBackend 는 launchd LaunchAgent 등록이다.
//
// # 왜 modern bootstrap/bootout 만 쓰는가
//
// 레거시 `launchctl load -w`·`unload` 폴백을 넣지 않는다. `bootstrap` 은 OS X 10.11(2015)
// 부터 있고 Go 1.25 가 지원하는 어떤 macOS 에도 존재한다. 반면 `load -w` 는 **영구
// disabled-override DB** 를 부수효과로 건드려서 나중의 등록을 조용히 방해한다.
// 테스트할 수 없는 레거시 경로는 순수 부채다.
//
// 같은 이유로 `launchctl disable` 은 **절대 부르지 않는다.** 그것은 영구 override 를 써서
// 미래의 enable 을 막는다. 등록 해제는 bootout + 파일 삭제로 충분하다.
type darwinBackend struct {
	opts Options
	uid  int
}

func (b *darwinBackend) kind() Kind       { return KindLaunchd }
func (b *darwinBackend) unitPath() string { return LaunchAgentPath(b.opts.Env) }

// logPath 는 **표준 오류** 로그를 가리킨다. 사용자에게 "크래시를 보려면 이 파일" 이라고
// 말할 수 있는 파일이 하나여야 하고, stderr 에는 "daemon 실패:" 한 줄만 나가기 때문이다.
func (b *darwinBackend) logPath() string { return ErrLogPath(b.opts.Env) }

func (b *darwinBackend) domain() string { return "gui/" + strconv.Itoa(b.uid) }
func (b *darwinBackend) target() string { return b.domain() + "/" + Label }

func (b *darwinBackend) enable(ctx context.Context, execPath string, args []string) (Result, error) {
	plistPath := b.unitPath()
	res := Result{Kind: KindLaunchd, UnitPath: plistPath, LogPath: b.logPath(), ExecPath: execPath}

	// launchd 는 로그 파일의 **디렉터리를 만들어 주지 않는다.** 없으면 job 이 뜨지 못하고,
	// 그 실패는 갈 곳이 없어서(로그 파일이 목적지다) 사용자에게 보이지 않는다.
	if dir := LogDir(b.opts.Env); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return res, fmt.Errorf("로그 디렉터리 생성 실패 %s: %w", dir, err)
		}
	}

	content := renderPlist(plistParams{
		Label:             Label,
		ProgramArguments:  append([]string{execPath}, args...),
		WorkingDirectory:  b.opts.Env.HomeDir,
		StandardOutPath:   LogPath(b.opts.Env),
		StandardErrorPath: ErrLogPath(b.opts.Env),
		ThrottleInterval:  ThrottleInterval,
	})
	// 0644 다. plist 는 launchd 가 읽는 공개 설정이고 비밀이 들어가지 않는다
	// (토큰은 키링에, 데몬 인자는 서브커맨드 하나뿐).
	if err := config.AtomicWriteFile(plistPath, content, 0o644); err != nil {
		return res, fmt.Errorf("plist 기록 실패 %s: %w", plistPath, err)
	}

	// 과거에 `launchctl disable` 로 굳은 override 를 푼다. 실패는 허용한다 —
	// override 가 애초에 없으면 실패하는 것이 정상이다.
	_, _, _, _ = b.opts.Runner.Run(ctx, "launchctl", "enable", b.target())

	// 멱등 재등록 경로다. 로드된 적이 없으면 exit 3("No such process")이고 그것은 정상이다.
	_, stderr, code, err := b.opts.Runner.Run(ctx, "launchctl", "bootout", b.target())
	if err != nil {
		return res, fmt.Errorf("launchctl bootout 실행 실패: %w", err)
	}
	if code != 0 && code != 3 {
		res.Notes = append(res.Notes,
			fmt.Sprintf("참고: 기존 등록 해제가 exit %d 로 끝났습니다 (%s)", code, oneLine(stderr)))
	}

	// 여기는 반드시 성공해야 한다. RunAtLoad=true 라 bootstrap 이 곧 기동이고,
	// 그래서 kickstart 를 따로 부르지 않는다.
	_, stderr, code, err = b.opts.Runner.Run(ctx, "launchctl", "bootstrap", b.domain(), plistPath)
	if err != nil {
		return res, fmt.Errorf("launchctl bootstrap 실행 실패: %w", err)
	}
	if code != 0 {
		return res, bootstrapError(code, stderr)
	}

	res.Notes = append(res.Notes,
		"로그: "+ErrLogPath(b.opts.Env)+" (표준 출력은 "+LogPath(b.opts.Env)+")",
		"macOS 13 이상에서는 시스템 설정 → 일반 → 로그인 항목에 이 항목이 나타나고,"+
			" 거기서 끄면 등록이 남아 있어도 실행되지 않습니다.",
		"로그아웃하면 데몬도 함께 종료되고 다음 로그인에 다시 시작합니다.")
	return res, nil
}

func (b *darwinBackend) disable(ctx context.Context) (Result, error) {
	plistPath := b.unitPath()
	res := Result{Kind: KindLaunchd, UnitPath: plistPath, LogPath: b.logPath()}

	// **순서가 중요하다.** 파일을 먼저 지우면 로드된 job 이 로그아웃까지 남는다 —
	// 파일 없는 job 이라 되돌릴 방법도 마땅치 않다.
	_, stderr, code, err := b.opts.Runner.Run(ctx, "launchctl", "bootout", b.target())
	if err != nil {
		return res, fmt.Errorf("launchctl bootout 실행 실패: %w", err)
	}
	if code != 0 && code != 3 {
		res.Notes = append(res.Notes,
			fmt.Sprintf("참고: launchctl bootout 이 exit %d 로 끝났습니다 (%s)", code, oneLine(stderr)))
	}

	if err := os.Remove(plistPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			res.AlreadyInState = true
			return res, nil
		}
		return res, fmt.Errorf("plist 삭제 실패 %s: %w", plistPath, err)
	}
	return res, nil
}

func (b *darwinBackend) status(ctx context.Context) (Status, error) {
	plistPath := b.unitPath()
	st := Status{Kind: KindLaunchd, UnitPath: plistPath, LogPath: b.logPath(), Supported: true}

	switch _, err := os.Stat(plistPath); {
	case err == nil:
		st.Registered = true
		p, readErr := readPlistExecPath(plistPath)
		if readErr != nil {
			st.Detail = appendDetail(st.Detail, fmt.Sprintf("plist 를 해석하지 못했습니다: %v", readErr))
		} else {
			st.RegisteredExecPath = p
		}
	case errors.Is(err, fs.ErrNotExist):
		// 미등록은 상태이지 오류가 아니다.
	default:
		return st, fmt.Errorf("plist 확인 실패 %s: %w", plistPath, err)
	}

	stdout, _, code, err := b.opts.Runner.Run(ctx, "launchctl", "print", b.target())
	if err != nil {
		st.Detail = appendDetail(st.Detail, fmt.Sprintf("launchctl 을 실행하지 못했습니다: %v", err))
		return st, nil
	}
	if code != 0 {
		// 로드되지 않았다는 뜻이다. 등록 파일은 있는데 로드가 안 된 상태도 정상적으로
		// 존재한다 (등록 직후 로그아웃 전, 또는 로그인 항목에서 끈 경우).
		return st, nil
	}
	st.Loaded = true
	parseLaunchctlPrint(stdout, &st)
	return st, nil
}

func bootstrapError(code int, stderr string) error {
	detail := oneLine(stderr)
	if code == 5 {
		// "Bootstrap failed: 5: Input/output error" — gui/<uid> 도메인이 없다는 뜻이다.
		return fmt.Errorf("launchctl bootstrap 실패 (exit 5: %s) — GUI 로그인 세션이 없습니다."+
			" SSH 전용 세션에서는 LaunchAgent 를 등록할 수 없습니다."+
			" 데스크톱에 로그인한 상태에서 다시 실행하세요", detail)
	}
	return fmt.Errorf("launchctl bootstrap 실패 (exit %d): %s", code, detail)
}

// launchctl print 출력에서 기회주의적으로 뽑는 값들.
//
// **이 포맷은 macOS 릴리스마다 바뀐다.** 그래서 로드 여부 판정은 오직 종료 코드로 하고,
// 아래 값들은 못 읽어도 상태 조회가 실패하지 않는다. 데몬 생존의 권위 있는 신호는
// 여전히 runtime.json + /healthz 다 (cmd/telemetryctl/local.go 의 daemonRunning).
var (
	reLaunchctlPID      = regexp.MustCompile(`(?m)^\s*pid\s*=\s*(\d+)`)
	reLaunchctlState    = regexp.MustCompile(`(?m)^\s*state\s*=\s*(\S+)`)
	reLaunchctlLastExit = regexp.MustCompile(`(?m)^\s*last exit code\s*=\s*(-?\d+)`)
)

func parseLaunchctlPrint(out string, st *Status) {
	matched := false
	if m := reLaunchctlPID.FindStringSubmatch(out); m != nil {
		if pid, err := strconv.Atoi(m[1]); err == nil && pid > 0 {
			st.PID = pid
			st.Running = true
			matched = true
		}
	}
	if m := reLaunchctlState.FindStringSubmatch(out); m != nil {
		matched = true
		if strings.EqualFold(m[1], "running") {
			st.Running = true
		}
	}
	if m := reLaunchctlLastExit.FindStringSubmatch(out); m != nil {
		if v, err := strconv.Atoi(m[1]); err == nil {
			st.LastExit = v
			matched = true
		}
	}
	if !matched {
		st.Detail = appendDetail(st.Detail,
			"launchctl print 출력에서 pid·state 를 찾지 못했습니다 (형식이 바뀐 것으로 보입니다)")
	}
}

// plistParams 는 renderPlist 가 받는 값이다.
type plistParams struct {
	Label             string
	ProgramArguments  []string
	WorkingDirectory  string
	StandardOutPath   string
	StandardErrorPath string
	ThrottleInterval  int
}

// renderPlist 는 LaunchAgent plist 를 만든다.
//
// # 왜 손으로 조립하는가
//
// encoding/xml 의 구조체 마샬링은 plist 의 `<key>k</key><true/>` 교차 배치와 맞지 않는다.
// 서드파티 plist 라이브러리는 직접 의존성 5개 · CGO 금지(ADR 0002) · `go mod tidy -diff`
// 게이트가 있는 저장소에 30줄짜리 XML 을 위해 들일 것이 아니다. text/template 은 XML
// 이스케이프를 하지 않으므로 어차피 수동 이스케이프가 필요하다.
//
// **동적 값은 전부 xml.EscapeText 를 지난다.** 홈 디렉터리에 `&` 나 `<` 가 들어간 사용자는
// 실제로 존재하고, 이스케이프를 빠뜨리면 plist 가 깨져 job 이 조용히 뜨지 않는다.
//
// 키 선택의 근거:
//
//	RunAtLoad         로그인 시 + bootstrap 즉시 기동
//	KeepAlive         {SuccessfulExit:false} — 비정상 종료에만 재시작 (ADR 0007)
//	ThrottleInterval  30 (launchd 하한 10). 미enroll 크래시 루프를 싸게 만든다
//	ProcessType       Adaptive. Background 는 I/O 스로틀 + CPU 우선순위 하락이라
//	                  "개발 도구를 지연시키지 않는다"(§5.4)에 반한다
//	WorkingDirectory  홈. cwd 를 / 로 두지 않는다
//	EnvironmentVariables  **생략한다.** 개발자 셸 환경 스냅샷을 영구 파일에 굽는 것은
//	                  footgun 이자 토큰 유출 경로다. launchd 가 주는 HOME 과
//	                  PATH=/usr/bin:… 로 충분하다 (hostenv.Detect 는 HOME 만,
//	                  go-keyring 은 /usr/bin/security 만 필요하다)
func renderPlist(p plistParams) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")
	b.WriteString("<plist version=\"1.0\">\n")
	b.WriteString("<dict>\n")

	writePlistString(&b, "Label", p.Label)

	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range p.ProgramArguments {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", xmlEscape(a))
	}
	b.WriteString("\t</array>\n")

	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n")
	b.WriteString("\t<key>KeepAlive</key>\n\t<dict>\n")
	b.WriteString("\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n")
	b.WriteString("\t</dict>\n")
	fmt.Fprintf(&b, "\t<key>ThrottleInterval</key>\n\t<integer>%d</integer>\n", p.ThrottleInterval)
	writePlistString(&b, "ProcessType", "Adaptive")
	writePlistString(&b, "WorkingDirectory", p.WorkingDirectory)
	writePlistString(&b, "StandardOutPath", p.StandardOutPath)
	writePlistString(&b, "StandardErrorPath", p.StandardErrorPath)

	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

func writePlistString(b *bytes.Buffer, key, value string) {
	fmt.Fprintf(b, "\t<key>%s</key>\n\t<string>%s</string>\n", xmlEscape(key), xmlEscape(value))
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	// EscapeText 는 bytes.Buffer 에 쓸 때 오류를 내지 않는다.
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// readPlistExecPath 는 등록된 plist 에서 ProgramArguments[0] 를 되읽는다 (드리프트 판정).
//
// renderPlist 의 짝이다. 정규식이 아니라 XML 디코더를 쓰는 이유는 **이스케이프를 되돌리는
// 일을 표준 라이브러리에 맡기기 위해서**다 — 렌더와 파스가 이스케이프 규칙을 각자 구현하면
// `&` 가 들어간 경로에서 왕복이 깨지고, 그 결과는 "드리프트가 없는데 드리프트로 보고" 다.
//
// DOCTYPE 의 외부 DTD 는 encoding/xml 이 Directive 토큰으로 흘려보내므로 네트워크를 타지 않는다.
func readPlistExecPath(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // 읽기 전용 핸들이다

	dec := xml.NewDecoder(f)
	lastKey := ""
	inProgramArguments := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			var k string
			if err := dec.DecodeElement(&k, &start); err != nil {
				return "", err
			}
			lastKey = strings.TrimSpace(k)
		case "array":
			inProgramArguments = lastKey == "ProgramArguments"
		case "string":
			var v string
			if err := dec.DecodeElement(&v, &start); err != nil {
				return "", err
			}
			if inProgramArguments {
				return v, nil
			}
		}
	}
	// ProgramArguments 가 없다. 오류로 만들지 않는다 — 남이 만든 동명 plist 일 수 있고,
	// 그때 호출자가 볼 것은 "등록된 경로를 알 수 없다"(빈 문자열)이다.
	return "", nil
}
