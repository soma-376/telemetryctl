package store

import (
	"context"
	"fmt"
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
// seq 는 DedupKey 를 서로 다르게 만드는 손잡이 중 하나다.
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

// evrec 는 EventRecord 하나를 만든다. 필드가 많아 케이스마다 구조체를 다 쓰면 무엇을
// 검증하는지가 묻힌다.
func evrec(name string, ts time.Time, seq int, mods ...func(*EventRecord)) EventRecord {
	r := EventRecord{Event: newEvent(name, ts, seq)}
	for _, m := range mods {
		m(&r)
	}
	return r
}

// ── evrec 옵션 ──────────────────────────────────────────────────────────────

func sess(id string) func(*EventRecord) {
	return func(r *EventRecord) { r.Event.SessionID = id }
}

func vendor(v string) func(*EventRecord) {
	return func(r *EventRecord) { r.Event.Vendor = v }
}

// inTurn 은 실제 턴에 붙인다. 빈 값이면 가상 턴이므로 옵션을 아예 주지 않는 것과 같다.
func inTurn(key string) func(*EventRecord) {
	return func(r *EventRecord) { r.TurnKey = key }
}

func call(key string) func(*EventRecord) {
	return func(r *EventRecord) { r.CallKey = key }
}

func toolName(v string) func(*EventRecord) {
	return func(r *EventRecord) { r.Event.Attr.ToolName = v }
}

func targetPath(p string) func(*EventRecord) {
	return func(r *EventRecord) { r.TargetPath = p }
}

func fileChange(op, path string) func(*EventRecord) {
	return func(r *EventRecord) { r.File = session.FileChange{Path: path, Operation: op} }
}

func promptBody(body string) func(*EventRecord) {
	return func(r *EventRecord) {
		r.Contents = append(r.Contents, event.Content{Kind: event.ContentPrompt, Body: body})
	}
}

func succeeded(v bool) func(*EventRecord) {
	return func(r *EventRecord) { r.Event.Measure.Success = event.Some(v) }
}

func decision(d, source string) func(*EventRecord) {
	return func(r *EventRecord) {
		r.Event.Attr.Decision = d
		r.Event.Attr.DecisionSource = source
	}
}

func cost(usd float64) func(*EventRecord) {
	return func(r *EventRecord) { r.Event.Measure.CostUSD = event.Some(usd) }
}

func tokens(in, out int64) func(*EventRecord) {
	return func(r *EventRecord) {
		r.Event.Measure.InputTokens = event.Some(in)
		r.Event.Measure.OutputTokens = event.Some(out)
	}
}

func metric(name string) func(*EventRecord) {
	return func(r *EventRecord) {
		r.Event.Signal = event.SignalMetric
		r.Event.Name = name
		r.Event.Temporality = event.TemporalityDelta
	}
}

func errMessage(t, msg string) func(*EventRecord) {
	return func(r *EventRecord) {
		r.Event.Measure.ErrorType = t
		r.Event.Measure.ErrorMessage = msg
	}
}

func someInt(v int64) event.Opt[int64] { return event.Some(v) }

func someSec(v event.UnixSec) event.Opt[event.UnixSec] { return event.Some(v) }

// ── 조회 헬퍼 ───────────────────────────────────────────────────────────────

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

// scanOne 은 한 행 한 값을 읽는다. 없으면 테스트를 실패시킨다.
func scanOne(t *testing.T, db *DB, query string, args ...any) any {
	t.Helper()
	var v any
	if err := db.SQL().QueryRowContext(context.Background(), query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

func mustWrite(t *testing.T, db *DB, b Batch) WriteResult {
	t.Helper()
	res, err := db.Write(context.Background(), b)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	return res
}

// assertNoOrphans 는 v3 의 외래 키가 전부 지켜졌는지 본다.
// NO ACTION 이라 CASCADE 가 정리해 주지 않으므로 순서를 한 번만 틀려도 고아가 남는다.
func assertNoOrphans(t *testing.T, db *DB) {
	t.Helper()
	rows, err := db.SQL().QueryContext(context.Background(), `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("PRAGMA foreign_key_check: %v", err)
	}
	defer rows.Close()

	var found []string
	for rows.Next() {
		var table, parent any
		var rowid, fkid any
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			t.Fatalf("foreign_key_check 읽기: %v", err)
		}
		found = append(found, fmt.Sprintf("%v(rowid=%v) → %v", table, rowid, parent))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	if len(found) > 0 {
		t.Fatalf("외래 키 위반 %d건: %v", len(found), found)
	}
}

// newSession 은 v3 sessions 한 행에 대응하는 세션 스냅샷이다.
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

		// ADR 0010 이 로컬 저장을 허용한 값들이다. v3 의 sessions 컬럼으로 그대로 간다.
		WorkspacePath: "/Users/jy/dev/projects/soma-376/telemetryctl",
		UserEmail:     "kjy02927@gmail.com",
		UserAccountID: "acct-1",
		TerminalType:  "iTerm.app",

		ActiveSeconds: 120,
		ToolCalls:     2,
		CostUSD:       0.5,
	}
}
