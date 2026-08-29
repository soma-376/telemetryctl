package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// testNow 는 테스트 전체가 쓰는 고정 "지금" 이다. 벽시계를 읽으면 시간대 경계 테스트가
// 하루 중 언제 도느냐에 따라 통과했다 실패했다 한다.
//
// 값이 02:00 UTC 인 것이 핵심이다. Asia/Seoul(UTC+9)에서는 같은 순간이 이미 그날 11:00 이라
// "오늘" 의 시작이 UTC 로 전날 15:00 이다 — UTC 자정으로 자르는 구현과 결과가 갈리는 시각이다.
var testNow = time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)

const (
	seoul = "Asia/Seoul"
	utc   = "UTC"
)

type fixture struct {
	t      *testing.T
	dir    string
	path   string
	db     *store.DB
	reader *Reader
}

// newFixture 는 실제 SQLite 파일을 만들고 쓰기 핸들과 조회 핸들을 함께 연다.
// store 로 쓰고 dashboard 로 읽는 왕복이 이 패키지 테스트의 기본형이다.
func newFixture(t *testing.T, opts ...store.Option) *fixture {
	t.Helper()
	dir := t.TempDir()
	path := store.PathIn(dir)

	db, err := store.Open(context.Background(), path, opts...)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})

	r, err := Open(path)
	if err != nil {
		t.Fatalf("dashboard.Open: %v", err)
	}
	r.now = func() time.Time { return testNow }
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("Reader.Close: %v", err)
		}
	})
	return &fixture{t: t, dir: dir, path: path, db: db, reader: r}
}

// ── v3 전환 중의 자리표시자 ─────────────────────────────────────────────────
//
// v3 에는 rollup_hourly 가 없다. 시간 버킷 집계는 조회 시점 GROUP BY 로 만든다 (ADR 0009).
// 아래 타입들은 이 패키지의 테스트가 **컴파일만** 되게 유지하기 위한 것이다 — 조회 계층을
// v3 로 옮기고 이 테스트들을 다시 쓰는 것은 PROJ-87 의 몫이라 여기서 손대지 않는다.
// write 는 Rollups 를 조용히 버리므로 롤업을 읽는 테스트는 실패한 채로 남는다.

type testRollupDim string

const (
	testDimTotal   testRollupDim = "total"
	testDimVendor  testRollupDim = "vendor"
	testDimProject testRollupDim = "project"
)

type testRollupBucket struct {
	CostUSD             float64
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64

	APIRequests  int64
	APIErrors    int64
	Retries      int64
	LinesAdded   int64
	LinesRemoved int64
	Commits      int64
	PullRequests int64
	Prompts      int64
	ToolCalls    int64
	ToolAccepts  int64
	ToolRejects  int64

	ActiveSeconds   float64
	SessionsStarted int64
}

// IsZero 는 rollup.Bucket 이 제공하던 것과 같은 판정이다. 기여분이 0 인 행은 넣어도
// 값이 바뀌지 않는다.
func (b testRollupBucket) IsZero() bool { return b == testRollupBucket{} }

type testRollupRow struct {
	Hour   event.Hour
	Dim    testRollupDim
	Key    string
	Bucket testRollupBucket
}

type testBatch struct {
	Events   []store.EventRecord
	Sessions []session.Session
	Rollups  []testRollupRow
}

func (f *fixture) write(b testBatch) {
	f.t.Helper()
	batch := store.Batch{Events: b.Events, Sessions: b.Sessions}
	if _, err := f.db.Write(context.Background(), batch); err != nil {
		f.t.Fatalf("store.Write: %v", err)
	}
}

// rollupRow 는 시간 버킷 한 행을 만든다. hour 는 그 시각이 속한 UTC 시간 버킷으로 내려간다.
func rollupRow(at time.Time, dim testRollupDim, key string, b testRollupBucket) testRollupRow {
	return testRollupRow{
		Hour:   event.HourOf(event.NanoFromTime(at)),
		Dim:    dim,
		Key:    key,
		Bucket: b,
	}
}

// newSession 은 최소 필드를 채운 세션 스냅샷이다.
func newSession(id string, started time.Time, mods ...func(*session.Session)) session.Session {
	sec := event.SecFromTime(started)
	s := session.Session{
		SessionID:   id,
		Vendor:      "claude_code",
		StartedAt:   sec,
		LastEventAt: sec,
		EndedAt:     event.Some(sec + 600),
		Status:      session.StatusCompleted,
		Title:       "인증 토큰 검증 프록시",
		TitleSource: session.TitleFromPrompt,
		Summary:     "요약",
		ProjectHash: "hash-a",
		ProjectName: "telemetryctl",
		CostUSD:     1.5,
		ToolCalls:   3,
	}
	for _, m := range mods {
		m(&s)
	}
	return s
}

// newEvent 는 events 의 NOT NULL 계약을 만족하는 최소 이벤트다.
// seq 가 DedupKey 를 서로 다르게 만드는 유일한 손잡이다.
func newEvent(sessionID string, at time.Time, seq int) event.Event {
	return event.Event{
		Vendor:         "claude_code",
		InstallationID: "inst-1",
		Signal:         event.SignalLog,
		Name:           "claude_code.user_prompt",
		TS:             event.NanoFromTime(at),
		SessionID:      sessionID,
		Sequence:       seq,
	}
}

// prompt 는 원문이 달린 이벤트 레코드다. 검색(content_fts) 테스트의 시드다.
func prompt(sessionID string, at time.Time, seq int, body string) store.EventRecord {
	return store.EventRecord{
		Event:    newEvent(sessionID, at, seq),
		Contents: []event.Content{{Kind: event.ContentPrompt, Body: body}},
	}
}

func cardFor(t *testing.T, cards []Card, metric string) Card {
	t.Helper()
	for _, c := range cards {
		if c.Metric == metric {
			return c
		}
	}
	t.Fatalf("카드 %q 가 없다 (있는 것: %v)", metric, cards)
	return Card{}
}

func mustTime(t *testing.T, layout, value, tz string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", tz, err)
	}
	parsed, err := time.ParseInLocation(layout, value, loc)
	if err != nil {
		t.Fatalf("ParseInLocation(%q): %v", value, err)
	}
	return parsed
}
