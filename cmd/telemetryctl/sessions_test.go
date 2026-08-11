package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// DB 가 없어도 sessions 는 돌아야 한다 (stats 와 같은 이유).
func TestSessionsWithoutDatabase(t *testing.T) {
	_, args := tempTarget(t)

	res := runCmd(t, runSessions, args...)
	if res.code != 0 {
		t.Fatalf("code = %d, want 0 (stderr: %s)", res.code, res.stderr)
	}
	res.mustContain(t, "로컬 데이터가 아직 없습니다")

	jsonRes := runCmd(t, runSessions, append(args, "--json")...)
	if jsonRes.code != 0 {
		t.Fatalf("--json code = %d, want 0", jsonRes.code)
	}
	var parsed sessionsResult
	if err := json.Unmarshal([]byte(jsonRes.stdout), &parsed); err != nil {
		t.Fatalf("JSON 파싱: %v\n%s", err, jsonRes.stdout)
	}
	if parsed.Available || parsed.Count != 0 {
		t.Errorf("available=%t count=%d, want false·0", parsed.Available, parsed.Count)
	}
	if parsed.Sessions == nil {
		t.Error("sessions = null, want []")
	}
}

func TestSessionsInvalidFlags(t *testing.T) {
	_, args := tempTarget(t)
	tests := []struct {
		name string
		flag []string
	}{
		{name: "잘못된 --since", flag: []string{"--since", "이틀"}},
		{name: "잘못된 --status", flag: []string{"--status", "done"}},
		{name: "범위 밖 --limit", flag: []string{"--limit", "5000"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := runCmd(t, runSessions, append(append([]string{}, args...), tt.flag...)...)
			if res.code != 2 {
				t.Fatalf("code = %d, want 2 (stderr: %s)", res.code, res.stderr)
			}
		})
	}
}

// 표의 각 값이 JSON 의 같은 필드에서 오는지 확인한다.
func TestSessionsHumanMatchesJSON(t *testing.T) {
	dir, args := tempTarget(t)
	at := time.Now().Add(-30 * time.Minute)
	seed(t, dir, at)

	jsonRes := runCmd(t, runSessions, append(args, "--json")...)
	if jsonRes.code != 0 {
		t.Fatalf("--json code = %d (stderr: %s)", jsonRes.code, jsonRes.stderr)
	}
	var parsed sessionsResult
	if err := json.Unmarshal([]byte(jsonRes.stdout), &parsed); err != nil {
		t.Fatalf("JSON 파싱: %v\n%s", err, jsonRes.stdout)
	}
	if parsed.Count != 2 {
		t.Fatalf("count = %d, want 2: %s", parsed.Count, jsonRes.stdout)
	}

	human := runCmd(t, runSessions, args...)
	if human.code != 0 {
		t.Fatalf("code = %d (stderr: %s)", human.code, human.stderr)
	}
	for _, s := range parsed.Sessions {
		cells := sessionCells(s)
		line := findLine(t, human.stdout, cells[0])
		// 제목은 공백을 포함하므로 맨 뒤에 있고, 나머지 셀은 순서대로 들어 있어야 한다.
		rest := line
		for _, cell := range cells {
			idx := strings.Index(rest, cell)
			if idx < 0 {
				t.Fatalf("세션 %s 행에 %q 가 순서대로 없다:\n%s", s.SessionID, cell, line)
			}
			rest = rest[idx+len(cell):]
		}
	}
}

// 사용자에게 보이는 시각은 로컬 시간대여야 한다. UTC 로 찍으면 한국에서 아홉 시간 전에
// 한 작업으로 보인다.
func TestSessionsShowsLocalTime(t *testing.T) {
	dir, args := tempTarget(t)
	at := time.Now().Add(-90 * time.Minute)
	seed(t, dir, at)

	res := runCmd(t, runSessions, args...)
	if res.code != 0 {
		t.Fatalf("code = %d (stderr: %s)", res.code, res.stderr)
	}
	want := at.In(time.Local).Format(timeLayout)
	res.mustContain(t, want)
	if !strings.Contains(res.stdout, zoneLabel(at)) {
		t.Errorf("출력에 시간대 표시(%s)가 없다:\n%s", zoneLabel(at), res.stdout)
	}
}

func TestSessionsStatusFilter(t *testing.T) {
	dir, args := tempTarget(t)
	seed(t, dir, time.Now().Add(-30*time.Minute))

	res := runCmd(t, runSessions, append(args, "--status", "running", "--json")...)
	if res.code != 0 {
		t.Fatalf("code = %d (stderr: %s)", res.code, res.stderr)
	}
	var parsed sessionsResult
	if err := json.Unmarshal([]byte(res.stdout), &parsed); err != nil {
		t.Fatalf("JSON 파싱: %v", err)
	}
	if parsed.Count != 1 || parsed.Sessions[0].SessionID != "sess-codex" {
		t.Fatalf("running 필터 결과 = %d개 %+v, want sess-codex 1개", parsed.Count, parsed.Sessions)
	}
}

// --since 밖의 세션은 나오지 않아야 한다.
func TestSessionsSinceWindow(t *testing.T) {
	dir, args := tempTarget(t)
	seed(t, dir, time.Now().Add(-48*time.Hour))

	res := runCmd(t, runSessions, append(args, "--since", "1d", "--json")...)
	if res.code != 0 {
		t.Fatalf("code = %d (stderr: %s)", res.code, res.stderr)
	}
	var parsed sessionsResult
	if err := json.Unmarshal([]byte(res.stdout), &parsed); err != nil {
		t.Fatalf("JSON 파싱: %v", err)
	}
	if parsed.Count != 0 {
		t.Errorf("count = %d, want 0 (48시간 전 세션은 --since 1d 밖이다)", parsed.Count)
	}
}

func findLine(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("%q 로 시작하는 줄이 없다:\n%s", prefix, out)
	return ""
}
