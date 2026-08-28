package main

import (
	"bytes"
	"context"
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

// seed 는 세션·롤업·원문 이벤트를 넣는다. at 을 인자로 받아 테스트가 "지금" 기준
// --since 창 안에 들어오게 맞출 수 있다.
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
				// 한글 제목이 열 정렬 검증의 재료다.
				Title: "인증 토큰 검증 프록시 구현", TitleSource: session.TitleFromPrompt,
				ProjectHash: "hash-a", ProjectName: "telemetryctl",
				DurationMS: 600_000, CostUSD: 1.25, ToolCalls: 7,
				InputTokens: 1200, OutputTokens: 340, Prompts: 3,
			},
			{
				SessionID: "sess-codex", Vendor: "codex",
				StartedAt: sec + 60, LastEventAt: sec + 120,
				Status: session.StatusRunning,
				Title:  "테스트 보강", TitleSource: session.TitleFromFiles,
				ProjectHash: "hash-b", ProjectName: "pulsemetry-backend",
				DurationMS: 60_000, CostUSD: 0.5, ToolCalls: 2,
				InputTokens: 400, OutputTokens: 90, Prompts: 1,
			},
		},
		Events: []store.EventRecord{{
			Event: event.Event{
				Vendor: "claude_code", InstallationID: "inst-1",
				Signal: event.SignalLog, Name: "claude_code.user_prompt",
				TS: event.NanoFromTime(at), SessionID: "sess-claude", Sequence: 1,
			},
			Contents: []event.Content{{Kind: event.ContentPrompt, Body: "토큰 검증을 붙여 줘"}},
		}},
	}
	if _, err := db.Write(context.Background(), batch); err != nil {
		t.Fatalf("store.Write: %v", err)
	}
}

// seedProjectRollup 은 프로젝트 차원 집계의 씨앗이다.
//
// v3 에는 rollup_hourly 가 없고 조회 시점 GROUP BY 로 만든다 (ADR 0009). 이 헬퍼가 무엇을
// 넣어야 하는지는 조회 계층을 v3 로 옮기는 PROJ-87 이 정한다 — 지금은 세션만 넣는다.
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
