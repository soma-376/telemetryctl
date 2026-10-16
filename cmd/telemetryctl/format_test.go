package main

import (
	"bytes"
	"strings"
	"testing"
)

// 한글은 룬 하나가 터미널 두 칸을 먹는다. 룬 개수로 재면(=tabwriter 의 방식) 한글이 섞인
// 열의 오른쪽 경계가 행마다 밀린다.
func TestDisplayWidth(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{in: "", want: 0},
		{in: "tool", want: 4},
		{in: "벤더", want: 4},
		{in: "프로젝트", want: 8},
		{in: "비용(USD)", want: 9}, // 한글 2자(4) + ASCII 괄호·USD(5)
		{in: "claude_code", want: 11},
		{in: "telemetryctl 저장소", want: 19}, // ASCII 12 + 공백 1 + 한글 3자(6)
	}
	for _, tt := range tests {
		if got := displayWidth(tt.in); got != tt.want {
			t.Errorf("displayWidth(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// 표의 모든 줄은 터미널에서 같은 칸에서 열이 시작해야 한다. 폭 계산이 틀리면 한글 이름이
// 섞인 행에서만 어긋나므로, 검증은 한글과 ASCII 를 같은 열에 섞어서 해야 의미가 있다.
func TestWriteTableAlignsByDisplayWidth(t *testing.T) {
	var buf bytes.Buffer
	writeTable(&buf, []column{{Header: "그룹"}, {Header: "비용", Right: true}},
		[][]string{
			{"claude_code", "1.2500"},
			{"코덱스", "0.5000"},
			{"매우 긴 한글 이름", "12.0000"},
		})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("줄 수 = %d, want 5 (헤더+구분선+3행):\n%s", len(lines), buf.String())
	}

	// 핵심 단언: 모든 줄의 **표시 폭** 이 같다. 룬 개수로 패딩하면(=tabwriter 의 방식)
	// 한글이 든 행만 폭이 달라져 여기서 걸린다.
	want := displayWidth(lines[0])
	for i, line := range lines {
		if w := displayWidth(line); w != want {
			t.Errorf("%d번째 줄 폭 = %d, want %d (%q)", i, w, want, line)
		}
	}
	// 왼쪽 정렬 열은 줄 맨 앞에서, 오른쪽 정렬 열은 줄 맨 끝에서 맞는다.
	for i, row := range [][2]string{{"claude_code", "1.2500"}, {"코덱스", "0.5000"}, {"매우 긴 한글 이름", "12.0000"}} {
		line := lines[i+2]
		if !strings.HasPrefix(line, row[0]) {
			t.Errorf("%q 행이 첫 열에서 시작하지 않는다: %q", row[0], line)
		}
		if !strings.HasSuffix(line, row[1]) {
			t.Errorf("%q 행의 수치가 줄 끝에서 맞지 않는다: %q", row[0], line)
		}
	}
}

func TestFormatInt(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{in: 0, want: "0"},
		{in: 7, want: "7"},
		{in: 999, want: "999"},
		{in: 1000, want: "1,000"},
		{in: 1234567, want: "1,234,567"},
		{in: -1234, want: "-1,234"},
	}
	for _, tt := range tests {
		if got := formatInt(tt.in); got != tt.want {
			t.Errorf("formatInt(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatDurationMS(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{in: 0, want: "-"},
		{in: -5, want: "-"},
		{in: 120, want: "120ms"},
		{in: 45_000, want: "45s"},
		{in: 90_000, want: "1m30s"},
		{in: 3_600_000, want: "1h00m"},
		{in: 3_930_000, want: "1h05m"},
	}
	for _, tt := range tests {
		if got := formatDurationMS(tt.in); got != tt.want {
			t.Errorf("formatDurationMS(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{in: 0, want: "0 B"},
		{in: 512, want: "512 B"},
		{in: 2048, want: "2.0 KiB"},
		{in: 3 * 1024 * 1024, want: "3.0 MiB"},
	}
	for _, tt := range tests {
		if got := formatBytes(tt.in); got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
