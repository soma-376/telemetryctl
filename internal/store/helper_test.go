package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
)

// baseTime 은 테스트 전체가 쓰는 고정 기준 시각이다. 벽시계를 읽으면 보존 경계 테스트가
// 실행 시각에 따라 흔들린다.
var baseTime = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func openTestDB(t *testing.T, opts ...Option) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), DefaultFileName), opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return db
}

// newEvent 는 events 의 NOT NULL 계약을 만족하는 최소 이벤트를 만든다.
// seq 는 DedupKey 를 서로 다르게 만드는 유일한 손잡이다.
func newEvent(name string, ts time.Time, seq int) event.Event {
	return event.Event{
		Vendor:         "claude_code",
		InstallationID: "inst-1",
		Signal:         event.SignalLog,
		Name:           name,
		TS:             event.NanoFromTime(ts),
		SessionID:      "sess-1",
		Sequence:       seq,
	}
}

func countRows(t *testing.T, db *DB, table string) int {
	t.Helper()
	return countWhere(t, db, table, "")
}

func countWhere(t *testing.T, db *DB, table, where string, args ...any) int {
	t.Helper()
	q := "SELECT COUNT(*) FROM " + table
	if where != "" {
		q += " WHERE " + where
	}
	var n int
	if err := db.SQL().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s 계수: %v", table, err)
	}
	return n
}

// newSession 은 파일·툴·MCP 를 하나씩 단 세션 스냅샷을 만든다.
func newSession(id string, at time.Time) session.Session {
	sec := event.SecFromTime(at)
	return session.Session{
		SessionID:   id,
		Vendor:      "claude_code",
		StartedAt:   sec,
		LastEventAt: sec,
		Status:      session.StatusRunning,
		Title:       "인증 토큰 검증",
		TitleSource: session.TitleFromPrompt,
		ProjectHash: "phash",
		ProjectName: "telemetryctl",
		ToolCalls:   2,
		CostUSD:     0.5,
		Files: []session.File{
			{PathHash: "fhash", Name: "apply.go", Ext: "go", LinesAdded: 10, Edits: 1, LastTS: sec},
		},
		Tools: []session.ToolEvent{
			{TS: sec, ToolName: "Edit", Action: session.ActionEdit, TargetName: "apply.go", TargetHash: "fhash", Success: event.Some(true)},
			// 같은 초·같은 툴·같은 대상의 두 번째 호출. 자연키 방식이면 조용히 접히는 행이다.
			{TS: sec, ToolName: "Edit", Action: session.ActionEdit, TargetName: "apply.go", TargetHash: "fhash", Success: event.Some(true)},
		},
		MCP: []session.MCPUsage{
			{ServerName: "github", Connected: true, ToolCalls: 3},
		},
	}
}
