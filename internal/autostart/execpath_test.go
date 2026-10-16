package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIsVolatilePath(t *testing.T) {
	tmp := os.TempDir()
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"설치된 바이너리는 휘발성이 아니다", "/usr/local/bin/telemetryctl", false},
		{"Homebrew 경로도 아니다", "/opt/homebrew/bin/telemetryctl", false},
		{"go run 산출물은 휘발성이다", filepath.Join(tmp, "go-build123", "b001", "exe", "telemetryctl"), true},
		{"임시 디렉터리 아래는 휘발성이다", filepath.Join(tmp, "telemetryctl"), true},
		{"darwin 의 /private/var/folders 도 휘발성이다", "/private/var/folders/xy/T/telemetryctl", true},
		{"go-build 를 포함하면 휘발성이다", "/somewhere/go-build900/exe/telemetryctl", true},
		{"빈 경로는 판정하지 않는다", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isVolatilePath(tc.path); got != tc.want {
				t.Fatalf("isVolatilePath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestUnderDir는이름접두사에속지않는다(t *testing.T) {
	// /tmpfoo 를 /tmp 아래로 잘못 판정하면 멀쩡한 설치 경로를 거부하게 된다.
	if underDir("/tmpfoo/telemetryctl", "/tmp") {
		t.Fatal("underDir 가 이름 접두사에 속았다")
	}
	if !underDir("/tmp/telemetryctl", "/tmp") {
		t.Fatal("underDir 가 실제 하위 경로를 놓쳤다")
	}
	if underDir("/tmp", "/tmp") {
		t.Fatal("자기 자신은 하위가 아니다")
	}
}

func TestResolveExecPath는테스트바이너리를휘발성으로본다(t *testing.T) {
	// `go test` 의 테스트 바이너리는 임시 디렉터리에 있다. 그래서 이 패키지의 다른
	// 테스트들이 Options.ExecPath 를 반드시 채운다 — 그 사실을 여기서 못박는다.
	// (러너 설정에 따라 아닐 수도 있으므로 그때는 조용히 건너뛴다.)
	p, err := resolveExecPath()
	if p == "" {
		t.Fatalf("resolveExecPath 가 경로를 비웠다: %v", err)
	}
	if !isVolatilePath(p) {
		t.Skipf("이 러너의 테스트 바이너리(%s)는 임시 디렉터리 밖이다", p)
	}
	if !errors.Is(err, ErrExecPathVolatile) {
		t.Fatalf("resolveExecPath err = %v, want ErrExecPathVolatile", err)
	}
}

// 왕복 테스트 — 렌더와 파스가 이스케이프 규칙을 각자 구현하면 `&` 나 공백이 든 경로에서
// 왕복이 깨지고, 그 결과는 "드리프트가 없는데 드리프트로 보고" 다. 싸고 유력한 버그를 잡는다.
func TestExecPath왕복(t *testing.T) {
	paths := []string{
		"/usr/local/bin/telemetryctl",
		"/opt/homebrew/bin/telemetryctl",
		`/opt/a&b/tele<ctl>`,
		"/opt/my apps/telemetryctl",
		`/opt/a"b/telemetryctl`,
	}

	t.Run("plist", func(t *testing.T) {
		for _, want := range paths {
			dir := t.TempDir()
			path := filepath.Join(dir, Label+".plist")
			content := renderPlist(plistParams{
				Label:            Label,
				ProgramArguments: []string{want, "daemon"},
				WorkingDirectory: dir,
				ThrottleInterval: ThrottleInterval,
			})
			mustWrite(t, path, content, 0o644)

			got, err := readPlistExecPath(path)
			if err != nil {
				t.Fatalf("readPlistExecPath(%q): %v", want, err)
			}
			if got != want {
				t.Errorf("plist 왕복 실패: got %q, want %q", got, want)
			}
		}
	})

	t.Run("unit", func(t *testing.T) {
		for _, want := range paths {
			dir := t.TempDir()
			path := filepath.Join(dir, UnitName)
			mustWrite(t, path, renderUnit(unitParams{ExecPath: want, Args: []string{"daemon"}}), 0o644)

			got, err := readUnitExecPath(path)
			if err != nil {
				t.Fatalf("readUnitExecPath(%q): %v", want, err)
			}
			if got != want {
				t.Errorf("unit 왕복 실패: got %q, want %q", got, want)
			}
		}
	})
}

func TestReadExecPath는알수없는파일에오류를내지않는다(t *testing.T) {
	// 남이 만든 동명 파일일 수 있다. 그때 호출자가 볼 것은 "등록된 경로를 알 수 없다"
	// (빈 문자열)이지 조회 실패가 아니다.
	dir := t.TempDir()

	plistPath := filepath.Join(dir, "other.plist")
	mustWrite(t, plistPath, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>Label</key><string>com.other.job</string></dict></plist>
`), 0o644)
	if got, err := readPlistExecPath(plistPath); err != nil || got != "" {
		t.Fatalf("readPlistExecPath = (%q, %v), want (빈 문자열, nil)", got, err)
	}

	unitPath := filepath.Join(dir, "other.service")
	mustWrite(t, unitPath, []byte("[Unit]\nDescription=남의 유닛\n"), 0o644)
	if got, err := readUnitExecPath(unitPath); err != nil || got != "" {
		t.Fatalf("readUnitExecPath = (%q, %v), want (빈 문자열, nil)", got, err)
	}
}

func TestReadUnitExecPath는주석과접두사를건너뛴다(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, UnitName)
	mustWrite(t, path, []byte(
		"# ExecStart=/decoy/telemetryctl\n"+
			"[Service]\n"+
			"ExecStart=-/usr/local/bin/telemetryctl daemon\n"), 0o644)

	got, err := readUnitExecPath(path)
	if err != nil {
		t.Fatalf("readUnitExecPath: %v", err)
	}
	if want := "/usr/local/bin/telemetryctl"; got != want {
		t.Fatalf("readUnitExecPath = %q, want %q", got, want)
	}
}
