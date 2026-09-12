package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// 이 패키지의 테스트는 실제 SQLite 파일에 쓰고 CLI 명령으로 읽는 왕복이 기본형이다.
// 조회 계층(dashboard)을 흉내 내면 "CLI 가 dashboard 를 잘못 호출한다" 는 부류의
// 버그가 통째로 빠져나간다.

// tempTarget 은 --data-dir·--state 인자를 만든다. 상태 파일 경로를 임시 디렉터리 안으로
// 넣는 것이 중요하다 — 비워 두면 테스트가 개발자 홈의 진짜 state.json 을 읽는다.
func tempTarget(t *testing.T) (dataDir string, args []string) {
	t.Helper()
	dir := t.TempDir()
	return dir, []string{"--data-dir", dir, "--state", statePathOf(dir)}
}

// statePathOf 는 임시 디렉터리 안의 상태 파일 경로다.
func statePathOf(dataDir string) string { return filepath.Join(dataDir, "state.json") }

// seed 는 세션과 승격 대상 이벤트를 넣는다. at 을 인자로 받아 테스트가 "지금" 기준
// --since 창 안에 들어오게 맞출 수 있다.
//
// v3 에는 rollup_hourly 도 sessions 의 비정규화 지표 컬럼도 없다. 비용·토큰은 llm_calls,
// 도구 호출은 tool_calls, 프롬프트는 turns 에서만 나오므로 씨앗도 그 승격 대상 이벤트다
// (ADR 0009). 세션 스냅샷만 넣으면 표의 모든 숫자가 0 이 된다.
func seed(t *testing.T, dataDir string, at time.Time) {
	t.Helper()
	db, err := store.Open(context.Background(), store.PathIn(dataDir))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	}()

	sec := event.SecFromTime(at)

	batch := store.Batch{
		Sessions: []session.Session{
			{
				SessionID: "sess-claude", Vendor: "claude_code",
				StartedAt: sec, LastEventAt: sec + 600, EndedAt: event.Some(sec + 600),
				Status: session.StatusCompleted,
				// 프로젝트 이름은 워크스페이스 경로의 basename 이다 (ADR 0010).
				WorkspacePath: workspaceClaude,
				ActiveSeconds: 300,
			},
			{
				SessionID: "sess-codex", Vendor: "codex",
				StartedAt: sec + 60, LastEventAt: sec + 120,
				Status:        session.StatusRunning,
				WorkspacePath: workspaceCodex,
				ActiveSeconds: 60,
			},
		},
		Events: seedEvents(at),
	}
	if _, err := db.Write(context.Background(), batch); err != nil {
		t.Fatalf("store.Write: %v", err)
	}

	// 제목은 벤더가 준 것만 저장하므로 스냅샷으로는 넣을 수 없다 (PROJ-124).
	// 한글 제목이 열 정렬 검증의 재료라 실제 경로와 같게 UPDATE 로 넣는다.
	for key, title := range map[string]string{
		"sess-claude": "인증 토큰 검증 프록시 구현",
		"sess-codex":  "테스트 보강",
	} {
		if _, err := db.SQL().ExecContext(context.Background(),
			`UPDATE sessions SET title = ? WHERE session_key = ?`, title, key); err != nil {
			t.Fatalf("제목 주입: %v", err)
		}
	}
}

// 워크스페이스 경로. basename 이 표의 「프로젝트」 열에 나온다.
const (
	workspaceClaude = "/home/jy/dev/telemetryctl"
	workspaceCodex  = "/home/jy/dev/pulsemetry-backend"
)

// seedPromptRows 는 seed 가 남기는 원문(turns.prompt_text) 행 수다.
//
// v3 에서 원문은 실제 턴에만 붙는다 (store/resolve.go 의 promptText). purge --content 가
// 이 수를 보고하므로 씨앗을 늘리면 여기도 함께 늘려야 한다.
const seedPromptRows int64 = 4

// seedEvents 는 두 세션의 승격 대상 이벤트다.
//
// 프롬프트 수는 실제 턴(turn_index NOT NULL) 개수로 세므로 프롬프트마다 turn_key 가 달라야
// 한다. 같은 키를 쓰면 한 턴으로 합쳐져 프롬프트가 하나로 보인다.
func seedEvents(at time.Time) []store.EventRecord {
	var recs []store.EventRecord
	seq := 0
	next := func() int { seq++; return seq }

	add := func(vendor, sessionKey, turnKey, name string, offset time.Duration, mod func(*store.EventRecord)) {
		e := event.Event{
			Vendor: vendor, InstallationID: "inst-1",
			Signal: event.SignalLog, Name: vendor + "." + name,
			TS: event.NanoFromTime(at.Add(offset)), SessionID: sessionKey, Sequence: next(),
		}
		rec := store.EventRecord{Event: e, TurnKey: turnKey}
		if mod != nil {
			mod(&rec)
		}
		recs = append(recs, rec)
	}

	// ── claude_code: 프롬프트 3건 · api_request 1건 · tool_result 7건 ──────────
	for i := range 3 {
		turn := fmt.Sprintf("claude-turn-%d", i)
		body := "토큰 검증을 붙여 줘"
		add("claude_code", "sess-claude", turn, "user_prompt", time.Duration(i)*time.Second,
			func(r *store.EventRecord) {
				r.Contents = []event.Content{{Kind: event.ContentPrompt, Body: body}}
			})
	}
	add("claude_code", "sess-claude", "claude-turn-0", "api_request", 10*time.Second,
		func(r *store.EventRecord) {
			r.Event.Attr.Model = "claude-sonnet-4"
			r.Event.Measure.CostUSD = event.Some(1.25)
			r.Event.Measure.InputTokens = event.Some(int64(1200))
			r.Event.Measure.OutputTokens = event.Some(int64(340))
			r.Event.Measure.CacheReadTokens = event.Some(int64(80))
		})
	for i := range 7 {
		add("claude_code", "sess-claude", "claude-turn-0", "tool_result",
			time.Duration(20+i)*time.Second, func(r *store.EventRecord) {
				r.Event.Attr.ToolName = "Edit"
				r.Event.Measure.Success = event.Some(true)
				r.CallKey = fmt.Sprintf("claude-call-%d", i)
				r.TargetPath = workspaceClaude + "/apply.go"
				r.File = session.FileChange{
					Path: workspaceClaude + "/apply.go", Operation: session.OperationModify,
					Additions: event.Some(int64(3)), Deletions: event.Some(int64(1)),
				}
			})
	}

	// ── codex: 프롬프트 1건 · api_request 1건 · tool_result 2건 ────────────────
	add("codex", "sess-codex", "codex-turn-0", "user_prompt", 60*time.Second,
		func(r *store.EventRecord) {
			r.Contents = []event.Content{{Kind: event.ContentPrompt, Body: "테스트를 보강해 줘"}}
		})
	add("codex", "sess-codex", "codex-turn-0", "api_request", 70*time.Second,
		func(r *store.EventRecord) {
			r.Event.Attr.Model = "gpt-5-codex"
			r.Event.Measure.CostUSD = event.Some(0.5)
			r.Event.Measure.InputTokens = event.Some(int64(400))
			r.Event.Measure.OutputTokens = event.Some(int64(90))
		})
	for i := range 2 {
		add("codex", "sess-codex", "codex-turn-0", "tool_result",
			time.Duration(80+i)*time.Second, func(r *store.EventRecord) {
				r.Event.Attr.ToolName = "Bash"
				r.Event.Measure.Success = event.Some(true)
				r.CallKey = fmt.Sprintf("codex-call-%d", i)
			})
	}
	return recs
}

// seedProjectRollup 은 프로젝트 축 집계의 씨앗이다.
//
// v3 에는 rollup_hourly 가 없고 조회 시점 GROUP BY 로 만든다 (ADR 0009). 프로젝트 축의
// 키는 sessions.workspace_path 이므로 seed 가 넣는 세션이 그대로 씨앗이다.
func seedProjectRollup(t *testing.T, dataDir string, at time.Time) {
	t.Helper()
	seed(t, dataDir, at)
}

// runResult 는 명령 하나의 stdout·stderr·종료 코드다.
type runResult struct {
	code   int
	stdout string
	stderr string
}

// runCmd 는 runStats·runSessions·runStatus 처럼 (stdout, stderr, args) 를 받는 명령을 돌린다.
func runCmd(t *testing.T, fn func(stdout, stderr io.Writer, args []string) int, args ...string) runResult {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := fn(&out, &errBuf, args)
	return runResult{code: code, stdout: out.String(), stderr: errBuf.String()}
}

func (r runResult) mustContain(t *testing.T, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(r.stdout, s) {
			t.Errorf("stdout 에 %q 가 없다:\n%s", s, r.stdout)
		}
	}
}
