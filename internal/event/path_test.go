package event

import (
	"strings"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantName string
		wantExt  string
		wantHash bool // 해시가 채워져야 하는가
	}{
		{"POSIX 절대경로", "/Users/jy/dev/telemetryctl/internal/event/path.go", "path.go", "go", true},
		{"Windows 절대경로", `C:\Users\jy\dev\telemetryctl\main.go`, "main.go", "go", true},
		{"Windows UNC", `\\server\share\team\notes.md`, "notes.md", "md", true},
		{"WSL 경로", "/mnt/c/Users/jy/dev/app.ts", "app.ts", "ts", true},
		{"홈 축약", "~/dev/secret-project/config.yaml", "config.yaml", "yaml", true},
		{"상대경로", "internal/event/dedup.go", "dedup.go", "go", true},
		{"현재 디렉터리 접두", "./internal/event/dedup.go", "dedup.go", "go", true},
		{"부모 참조", "/a/b/../c/d.rs", "d.rs", "rs", true},
		{"후행 슬래시", "/Users/jy/dev/telemetryctl/", "telemetryctl", "", true},
		{"Windows 후행 역슬래시", `C:\Users\jy\dev\`, "dev", "", true},
		{"중복 슬래시", "/Users//jy///dev/a.go", "a.go", "go", true},
		{"공백 패딩", "  /Users/jy/a.go\n", "a.go", "go", true},
		{"확장자 없음", "/usr/local/bin/telemetryctl", "telemetryctl", "", true},
		{"닷파일", "/Users/jy/dev/.gitignore", ".gitignore", "", true},
		{"이중 확장자", "/tmp/backup/archive.tar.GZ", "archive.tar.GZ", "gz", true},
		{"대문자 확장자", "/tmp/README.MD", "README.MD", "md", true},
		{"점으로 끝남", "/tmp/weird.", "weird.", "", true},
		{"루트", "/", "", "", true},
		{"드라이브 루트", `C:\`, "C:", "", true},
		{"빈 문자열", "", "", "", false},
		{"공백만", "   ", "", "", false},
		{"점 하나", ".", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizePath(tt.in)
			if got.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tt.wantName)
			}
			if got.Ext != tt.wantExt {
				t.Errorf("Ext = %q, want %q", got.Ext, tt.wantExt)
			}
			if hasHash := got.Hash != ""; hasHash != tt.wantHash {
				t.Errorf("Hash 채움 = %v, want %v (hash=%q)", hasHash, tt.wantHash, got.Hash)
			}
			if tt.wantHash && len(got.Hash) != 64 {
				t.Errorf("Hash 길이 = %d, want 64 (sha256 hex)", len(got.Hash))
			}
			if strings.ContainsAny(got.Name, `/\`) {
				t.Errorf("Name 에 구분자가 남음: %q", got.Name)
			}
		})
	}
}

// 이 패키지가 지켜야 할 가장 비싼 성질 — 전체 경로가 출력 어디에도 남지 않는다 (ADR 0003).
func TestNormalizePathNeverLeaksFullPath(t *testing.T) {
	tests := []struct {
		in       string
		segments []string // 출력 어디에도 나타나면 안 되는 상위 구간
	}{
		{
			in:       "/Users/jy/dev/acquisition-2026/src/deal.go",
			segments: []string{"Users", "jy", "dev", "acquisition-2026", "src", "/Users/jy"},
		},
		{
			in:       `C:\Users\jiyong\Documents\개인프로젝트\secret\main.rs`,
			segments: []string{"Users", "jiyong", "Documents", "개인프로젝트", "secret", `C:\Users`},
		},
		{
			in:       "~/work/client-merger/notes.md",
			segments: []string{"work", "client-merger", "~"},
		},
		{
			in:       "/mnt/c/Users/jy/side/tax-2025/report.xlsx",
			segments: []string{"mnt", "Users", "jy", "side", "tax-2025"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			p := NormalizePath(tt.in)
			all := p.Hash + "\x00" + p.Name + "\x00" + p.Ext
			for _, seg := range tt.segments {
				if strings.Contains(all, seg) {
					t.Errorf("상위 구간 %q 가 출력에 남음: %+v", seg, p)
				}
			}
			if strings.Contains(all, tt.in) {
				t.Errorf("전체 경로가 출력에 남음: %+v", p)
			}
			if p.Name == "" {
				t.Fatalf("basename 은 채워져야 한다: %+v", p)
			}
		})
	}
}

// 같은 파일이 표기 차이로 두 행이 되면 session_files 가 갈라진다.
func TestNormalizePathCollapsesEquivalentSpellings(t *testing.T) {
	groups := [][]string{
		{`C:\Users\jy\a.go`, "C:/Users/jy/a.go", `C:\Users\jy\a.go\`, `c:\Users\jy\a.go`},
		{"/a/b/c.go", "/a//b/c.go", "/a/b/../b/c.go", "/a/./b/c.go", "  /a/b/c.go  "},
		{"internal/event", "./internal/event", "internal/event/"},
	}
	for _, group := range groups {
		want := NormalizePath(group[0])
		for _, spelling := range group[1:] {
			if got := NormalizePath(spelling); got != want {
				t.Errorf("NormalizePath(%q) = %+v, want %+v (%q 와 같아야 함)", spelling, got, want, group[0])
			}
		}
	}
}

func TestNormalizePathSeparatesDifferentPaths(t *testing.T) {
	paths := []string{
		"/a/b/c.go", "/a/b/d.go", "/a/x/c.go", "c.go",
		"~/a/b/c.go", "/Users/jy/a/b/c.go", "/",
	}
	seen := map[string]string{}
	for _, p := range paths {
		hash := NormalizePath(p).Hash
		if prev, dup := seen[hash]; dup {
			t.Errorf("서로 다른 경로 %q 와 %q 의 해시가 같음", p, prev)
			continue
		}
		seen[hash] = p
	}
}

func TestNormalizePathIsDeterministic(t *testing.T) {
	const in = "/Users/jy/dev/telemetryctl/internal/event/path.go"
	want := NormalizePath(in)
	for range 50 {
		if got := NormalizePath(in); got != want {
			t.Fatalf("결과가 흔들림 got=%+v want=%+v", got, want)
		}
	}
}
