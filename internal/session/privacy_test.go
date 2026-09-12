package session

import (
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/event"
)

// 이 패키지의 계약: 식별 정보가 **지정된 필드 밖으로는** 조립기를 지나가지 않는다.
//
// ADR 0010 이 v3 스키마가 요구하는 값(작업 경로 원문·이메일·계정 ID)에 한해 로컬 저장을
// 열었다. 그래서 검사를 통째로 푸는 대신 그 필드들만 예외로 두고 나머지는 그대로 유지한다 —
// 조립 결과는 sessions · turns · file_changes 로 그대로 나가고, 예외 목록 밖에서 한 번 새면
// SQLite 에 영구히 남아 GUI 화면까지 흐른다.
const (
	fixtureRepoFile = "/Users/jy/dev/projects/soma-376/telemetryctl/internal/session/state.go"
	fixtureRepoRoot = "/Users/jy/dev/projects/soma-376/telemetryctl"
	fixtureEmail    = "kjy02927@gmail.com"
	fixtureAccount  = "9f2c1d55-3a7e-4c18-9b02-6d41ee0a7788"
)

// localOnlyFields 는 ADR 0010 이 로컬 저장을 허용한 Session 필드다.
//
// 이 목록은 예외이지 면제가 아니다. 스캐너는 이 필드들만 건너뛰고 나머지를 전부 훑는다.
// 목록을 늘리는 것은 ADR 개정을 요구하는 결정이다.
var localOnlyFields = []string{
	"WorkspacePath", // sessions.workspace_path
	"UserEmail",     // sessions.user_email
	"UserAccountID", // sessions.user_account_id
}

var forbiddenInSession = []string{fixtureRepoFile, fixtureRepoRoot, "/Users/", "dev/projects"}

// TestNoFullPathsInAssembledSession 은 디코더가 새로 실어 주는 경로(Input.Target)가
// 세션 조립 결과 어디에도 전체 문자열로 남지 않음을 단언한다.
//
// 프롬프트 원문에는 경로가 들어올 수 있고 제목은 거기서 파생되므로, 이 픽스처의
// 프롬프트에는 일부러 경로를 넣지 않는다. 원문 쪽 규칙은 event_content 의 몫이다 (ADR 0003).
func TestNoFullPathsInAssembledSession(t *testing.T) {
	const start = 1_700_000_000

	a := New()
	a.Add(logEv("s1", "claude_code.user_prompt", start,
		prompt("세션 조립기의 라인 배분을 고쳐줘. 파일별 수치가 합계를 넘지 않아야 한다."),
		project(fixtureRepoRoot), identity(fixtureEmail, fixtureAccount)))
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

	// 예외 필드에는 실제로 값이 있어야 한다. 비어 있으면 아래 루프가 "새지 않는다" 를
	// 증명하는 게 아니라 "채워지지 않았다" 를 통과시키는 것이 된다.
	if s.WorkspacePath != fixtureRepoRoot {
		t.Fatalf("workspace_path = %q, want %q", s.WorkspacePath, fixtureRepoRoot)
	}
	if s.UserEmail != fixtureEmail || s.UserAccountID != fixtureAccount {
		t.Fatalf("식별 정보가 안 실렸다: %q / %q", s.UserEmail, s.UserAccountID)
	}

	for _, str := range allStringsExcept(s, localOnlyFields) {
		for _, forbidden := range append(forbiddenInSession, fixtureEmail, fixtureAccount) {
			if strings.Contains(str, forbidden) {
				t.Errorf("조립 결과에 금지 문자열 %q 가 남았다: %q", forbidden, str)
			}
		}
	}

	// 스캐너가 실제로 훑고 있는지 — 이 단언이 없으면 위 루프가 조용히 무의미해질 수 있다.
	found := map[string]bool{}
	for _, str := range allStringsExcept(s, localOnlyFields) {
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
