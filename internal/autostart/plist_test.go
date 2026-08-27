package autostart

import (
	"encoding/xml"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Tier 1 — 순수 렌더러 테스트다. 빌드 태그를 쓰지 않기로 한 덕에 darwin.go 는 **세 러너
// 모두에서 컴파일되고 go vet 대상에 들어간다** — 플랫폼 파일 분리를 거부한 이유가 그것이고,
// 그 값은 그대로 살아 있다.
//
// 다만 렌더 결과 검증이 실제로 도는 곳은 darwin·linux 다. windows 는 TestRenderPlist 를
// 스킵한다. 사유는 렌더러의 플랫폼 의존이 아니라 아래 defaultPlist 의 픽스처 제약이다.
//
// 이 테스트들이 이 티켓에서 가장 값어치 있는 회귀 방어선이다: plist 가 깨져도 launchd 는
// 조용히 job 을 띄우지 않을 뿐이라 운영에서는 "아무 일도 안 일어남" 으로만 보인다.

// defaultPlist 는 렌더러 입력 픽스처다.
//
// # 이 픽스처는 GOOS 에 의존한다 (렌더러는 아니다)
//
// 로그 경로를 filepath.Join 으로 만드는데, windows 의 Join 은 내부 Clean 을 거치며 구분자를
// 전부 `\` 로 바꾼다 — `\Users\tester\Library\Logs\pulsemetry\daemon.log`. 반면 기대값은
// macOS 경로라 슬래시로 적혀 있어 비교가 어긋난다. renderPlist 자체는 문자열만 조립하는
// 순수 함수라 GOOS 와 무관하게 같은 바이트를 낸다. **깨지는 것은 렌더러가 아니라 이 픽스처다.**
//
// 픽스처를 GOOS 중립으로(path.Join·리터럴) 고치는 대신 windows 를 스킵하기로 했다. plist 는
// windows 자동 실행 경로에 아예 등장하지 않기 때문이다 — autostart.New 의 switch 는
// darwin·linux 만 받고 windows 는 ErrUnsupportedPlatform 이며(작업 스케줄러는 PROJ-56),
// 순수 함수를 세 번째 러너에서 한 번 더 돌려도 새로 잡히는 회귀가 없다.
//
// **이 픽스처를 GOOS 중립으로 고치는 날 TestRenderPlist 의 스킵도 함께 지워라.**
func defaultPlist(execPath, home string) string {
	return string(renderPlist(plistParams{
		Label:             Label,
		ProgramArguments:  []string{execPath, "daemon"},
		WorkingDirectory:  home,
		StandardOutPath:   filepath.Join(home, "Library", "Logs", "pulsemetry", logFileName),
		StandardErrorPath: filepath.Join(home, "Library", "Logs", "pulsemetry", errLogFileName),
		ThrottleInterval:  ThrottleInterval,
	}))
}

func TestRenderPlist(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("픽스처(defaultPlist)의 로그 경로가 GOOS 의존이다 — 렌더러 문제가 아니다")
	}
	const exec = "/usr/local/bin/telemetryctl"
	const home = "/Users/tester"
	out := defaultPlist(exec, home)

	tests := []struct {
		name string
		want []string
	}{
		{
			// 데몬 플래그를 유닛에 굽지 않는다는 결정의 회귀 방어선이다.
			// --state·--data-dir 이 여기 들어오면 state.json 의 위치가 두 곳이 된다.
			name: "기본 plist 는 ProgramArguments 가 [exec, daemon] 이다",
			want: []string{
				"<key>ProgramArguments</key>\n\t<array>\n" +
					"\t\t<string>/usr/local/bin/telemetryctl</string>\n" +
					"\t\t<string>daemon</string>\n\t</array>",
			},
		},
		{
			// ADR 0007. true 로 바뀌면 사용자가 정상 정지시켜도 launchd 가 되살린다.
			name: "KeepAlive 는 SuccessfulExit=false 다",
			want: []string{"<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>"},
		},
		{
			name: "RunAtLoad 가 켜져 있다",
			want: []string{"<key>RunAtLoad</key>\n\t<true/>"},
		},
		{
			// Background 는 I/O 스로틀 + CPU 우선순위 하락이라 §5.4 에 반한다.
			name: "ProcessType 은 Adaptive 다",
			want: []string{"<key>ProcessType</key>\n\t<string>Adaptive</string>"},
		},
		{
			name: "ThrottleInterval 은 30 이다",
			want: []string{"<key>ThrottleInterval</key>\n\t<integer>30</integer>"},
		},
		{
			name: "로그 두 경로를 나눠 적는다",
			want: []string{
				"<key>StandardOutPath</key>\n\t<string>/Users/tester/Library/Logs/pulsemetry/daemon.log</string>",
				"<key>StandardErrorPath</key>\n\t<string>/Users/tester/Library/Logs/pulsemetry/daemon.err.log</string>",
			},
		},
		{
			name: "WorkingDirectory 는 홈이다",
			want: []string{"<key>WorkingDirectory</key>\n\t<string>/Users/tester</string>"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("plist 에 다음이 없다:\n%s\n\n실제:\n%s", w, out)
				}
			}
		})
	}

	t.Run("EnvironmentVariables 를 넣지 않는다", func(t *testing.T) {
		// 개발자 셸 환경 스냅샷을 영구 파일에 굽는 것은 토큰 유출 경로다.
		if strings.Contains(out, "EnvironmentVariables") {
			t.Fatalf("plist 에 EnvironmentVariables 가 있다:\n%s", out)
		}
	})

	t.Run("생성된 plist 는 유효한 XML 이다", func(t *testing.T) {
		dec := xml.NewDecoder(strings.NewReader(out))
		for {
			_, err := dec.Token()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				t.Fatalf("XML 파싱 실패: %v\n%s", err, out)
			}
		}
	})
}

func TestRenderPlist경로의XML특수문자를이스케이프한다(t *testing.T) {
	// `&` 가 든 홈 디렉터리는 실제로 존재한다. 이스케이프를 빠뜨리면 plist 가 깨져
	// job 이 조용히 뜨지 않는다 — 사용자에게는 "아무것도 수집되지 않음" 으로만 보인다.
	const exec = `/Users/a&b/tele<ctl>`
	out := defaultPlist(exec, `/Users/a&b`)

	for _, raw := range []string{"<string>/Users/a&b", "<ctl>"} {
		if strings.Contains(out, raw) {
			t.Errorf("이스케이프되지 않은 %q 가 있다:\n%s", raw, out)
		}
	}
	for _, escaped := range []string{"&amp;", "&lt;ctl&gt;"} {
		if !strings.Contains(out, escaped) {
			t.Errorf("이스케이프 결과 %q 가 없다:\n%s", escaped, out)
		}
	}
}

func TestLabel과plist파일이름이같다(t *testing.T) {
	env := testEnv(t, "darwin")
	got := filepath.Base(LaunchAgentPath(env))
	if want := Label + ".plist"; got != want {
		// launchd 는 파일 이름과 Label 이 달라도 로드하지만, 다르면 사람이 파일을 찾지
		// 못하고 `launchctl print` 의 대상과 디스크의 파일이 어긋난 것처럼 보인다.
		t.Fatalf("plist 파일 이름 = %q, want %q", got, want)
	}
}
