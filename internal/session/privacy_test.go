package session

import (
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/event"
)

// 이 패키지의 계약: 전체 경로 문자열이 조립기를 지나가지 않는다 (ADR 0003).
//
// 계약을 지키는 것은 규율이 아니라 타입이다 — Input.Target 이 event.Path 라서 담을 자리가
// 해시·basename·확장자뿐이다. 그래도 테스트로 고정하는 이유는, 조립 결과가 sessions ·
// session_files · tool_events 세 테이블로 그대로 나가기 때문이다. 여기서 한 번 새면
// SQLite 에 영구히 남고 GUI 화면까지 그대로 흐른다.
const (
	fixtureRepoFile = "/Users/jy/dev/projects/soma-376/telemetryctl/internal/session/state.go"
	fixtureRepoRoot = "/Users/jy/dev/projects/soma-376/telemetryctl"
)

var forbiddenInSession = []string{fixtureRepoFile, fixtureRepoRoot, "/Users/", "dev/projects"}

// TestNoFullPathsInAssembledSession 은 디코더가 새로 실어 주는 경로(Input.Target)가
// 세션 조립 결과 어디에도 전체 문자열로 남지 않음을 단언한다.
//
// 프롬프트 원문에는 경로가 들어올 수 있고 제목·요약은 거기서 파생되므로, 이 픽스처의
// 프롬프트에는 일부러 경로를 넣지 않는다. 원문 쪽 규칙은 event_content 의 몫이다 (ADR 0003).
func TestNoFullPathsInAssembledSession(t *testing.T) {
	const start = 1_700_000_000

	a := New()
	a.Add(logEv("s1", "claude_code.user_prompt", start,
		prompt("세션 조립기의 라인 배분을 고쳐줘. 파일별 수치가 합계를 넘지 않아야 한다."),
		project(fixtureRepoRoot)))
	a.Add(logEv("s1", "claude_code.tool_result", start+1,
		tool("Edit"), target(fixtureRepoFile), success(true)))
	a.Add(logEv("s1", "claude_code.tool_result", start+2,
		tool("Write"), target(fixtureRepoRoot+"/internal/session/lines.go"), success(true)))
	a.Add(logEv("s1", "claude_code.tool_decision", start+3,
		tool("Edit"), decide("reject"), target(fixtureRepoFile)))
	a.Add(metricEv("s1", "claude_code.lines_of_code.count", start+60, 30, typ("added")))
	a.Add(metricEv("s1", "claude_code.lines_of_code.count", start+60, 4, typ("removed")))

	s := only(t, a.Advance(start+3600))

	// 전제 확인: 파일·툴 행이 실제로 만들어졌다. 비어 있으면 아래 단언이 공허하게 통과한다.
	if len(s.Files) != 2 {
		t.Fatalf("session_files 가 %d행 — 디코더→세션 경로가 끊겼다: %+v", len(s.Files), s.Files)
	}
	if len(s.Tools) != 3 {
		t.Fatalf("tool_events 가 %d행: %+v", len(s.Tools), s.Tools)
	}
	if s.ProjectHash == "" || s.ProjectName != "telemetryctl" {
		t.Fatalf("project 정규화가 안 됐다: hash=%q name=%q", s.ProjectHash, s.ProjectName)
	}

	for _, str := range allStrings(s) {
		for _, forbidden := range forbiddenInSession {
			if strings.Contains(str, forbidden) {
				t.Errorf("조립 결과에 금지 문자열 %q 가 남았다: %q", forbidden, str)
			}
		}
	}

	// 스캐너가 실제로 훑고 있는지 — 이 단언이 없으면 위 루프가 조용히 무의미해질 수 있다.
	found := map[string]bool{}
	for _, str := range allStrings(s) {
		found[str] = true
	}
	for _, want := range []string{"state.go", "lines.go", "telemetryctl", "Edit", "Write"} {
		if !found[want] {
			t.Fatalf("스캐너가 %q 를 못 봤다 — collectStrings 가 구조체를 못 훑고 있다", want)
		}
	}
}

// tool_events.target_* 도 같은 규칙이다. 타임라인 화면에 그대로 나가는 값이다.
func TestToolEventTargetsAreNormalized(t *testing.T) {
	const start = 1_700_000_000

	a := New()
	a.Add(logEv("s1", "claude_code.tool_result", start,
		tool("Edit"), target(fixtureRepoFile), success(true)))
	// 대상이 없는 툴(명령 실행)은 빈 값이어야 한다 — 추측해서 채우면 화면이 조용히 틀린다.
	a.Add(logEv("s1", "claude_code.tool_result", start+1, tool("Bash"), success(false)))

	s, ok := a.Session("s1")
	if !ok {
		t.Fatal("세션이 없다")
	}
	if len(s.Tools) != 2 {
		t.Fatalf("tool_events = %d행", len(s.Tools))
	}

	edit, bash := s.Tools[0], s.Tools[1]
	if edit.TargetName != "state.go" {
		t.Errorf("target_name = %q, want state.go", edit.TargetName)
	}
	assertBasenameOnly(t, "tool_events.target_name", edit.TargetName)
	assertPathHash(t, "tool_events.target_hash", edit.TargetHash)
	if edit.TargetHash != event.NormalizePath(fixtureRepoFile).Hash {
		t.Errorf("target_hash 가 NormalizePath 결과와 다르다: %q", edit.TargetHash)
	}
	if bash.TargetName != "" || bash.TargetHash != "" {
		t.Errorf("대상 없는 툴에 값이 채워졌다: %+v", bash)
	}
}

// 실패한 편집은 파일을 바꾸지 않았으므로 session_files 에 들어가지 않는다.
// 대상이 도달하기 시작한 뒤에도 이 구분이 유지되는지 본다.
func TestFailedEditDoesNotCreateFileRow(t *testing.T) {
	const start = 1_700_000_000

	a := New()
	a.Add(logEv("s1", "claude_code.tool_result", start,
		tool("Edit"), target(fixtureRepoFile), success(false)))
	a.Add(metricEv("s1", "claude_code.lines_of_code.count", start+60, 12, typ("added")))

	s, _ := a.Session("s1")
	assertLineInvariant(t, s)
	if len(s.Files) != 0 {
		t.Fatalf("실패한 편집이 파일 행을 만들었다: %+v", s.Files)
	}
	if s.Diag.UnattributedLinesAdded != 12 {
		t.Fatalf("미배분 = %d, want 12", s.Diag.UnattributedLinesAdded)
	}
	// 타임라인에는 남아야 한다 — 실패도 화면에 보여야 할 사건이다.
	if len(s.Tools) != 1 || s.Tools[0].TargetName != "state.go" {
		t.Fatalf("실패한 편집이 타임라인에서 사라졌다: %+v", s.Tools)
	}
}
