package tray

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// testNow 는 02:00 UTC 다. Asia/Seoul 에서 같은 순간이 그날 11:00 이라 "오늘" 의 시작이
// UTC 로 전날 15:00 이다 — UTC 자정으로 자르는 구현과 결과가 갈리는 시각이다.
var testNow = time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC)

const (
	seoul = "Asia/Seoul"
	utc   = "UTC"

	vendorClaude = "claude_code"
	vendorCodex  = "codex"
	workspaceA   = "/home/jy/dev/telemetryctl"
)

// ── 픽스처 ──────────────────────────────────────────────────────────────────

type fixture struct {
	t   *testing.T
	db  *store.DB
	svc *dashboard.Service
}

// newFixture 는 실제 SQLite 파일을 만들고 쓰기 핸들과 조회 서비스를 함께 연다. store 로
// 쓰고 조회 계층으로 읽는 왕복이라 스키마가 어긋나면 여기서 걸린다.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := store.PathIn(t.TempDir())

	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})

	svc := dashboard.NewService(path, dashboard.WithClock(func() time.Time { return testNow }))
	if err := svc.Start(); err != nil {
		t.Fatalf("Service.Start: %v", err)
	}
	t.Cleanup(func() { svc.Stop() }) //nolint:errcheck // 테스트 정리
	return &fixture{t: t, db: db, svc: svc}
}

func (f *fixture) write(b store.Batch) {
	f.t.Helper()
	if _, err := f.db.Write(context.Background(), b); err != nil {
		f.t.Fatalf("store.Write: %v", err)
	}
}

// breakQueries 는 조회 커넥션을 끊어 로컬 질의를 실제로 실패시킨다. Querier 에는 Close 가
// 없어 원본 타입으로 되돌린다 — 테스트에서만 하는 일이다.
func (f *fixture) breakQueries() {
	f.t.Helper()
	q, ok := f.svc.Querier()
	if !ok {
		f.t.Fatal("DB 가 열려 있지 않다")
	}
	if err := q.(*sql.DB).Close(); err != nil {
		f.t.Fatalf("조회 커넥션 닫기: %v", err)
	}
}

// ── 씨앗 ────────────────────────────────────────────────────────────────────

// newSession 은 최소 필드를 채운 세션이다. 기본은 마감된 세션이다.
func newSession(key string, started time.Time, mods ...func(*session.Session)) session.Session {
	sec := event.SecFromTime(started)
	s := session.Session{
		SessionID:     key,
		Vendor:        vendorClaude,
		StartedAt:     sec,
		LastEventAt:   sec + 600,
		EndedAt:       event.Some(sec + 600),
		Status:        session.StatusCompleted,
		Title:         "인증 토큰 검증 프록시",
		WorkspacePath: workspaceA,
		ActiveSeconds: 120,
	}
	for _, m := range mods {
		m(&s)
	}
	return s
}

// running 은 세션을 진행 중으로 바꾼다. v3 에는 status 컬럼이 없고 ended_at 이 NULL 인
// 것이 곧 running 이다 (ADR 0009).
func running(s *session.Session) {
	s.Status = session.StatusRunning
	s.EndedAt = event.Opt[event.UnixSec]{}
}

func codex(s *session.Session) { s.Vendor = vendorCodex }

// promptRecord 는 사용자 프롬프트다. 본문은 turns.prompt_text 에만 남는다.
func promptRecord(sessionKey, turnKey string, at time.Time, seq int, body string) store.EventRecord {
	return store.EventRecord{
		Event:    baseEvent(sessionKey, "claude_code.user_prompt", at, seq),
		Contents: []event.Content{{Kind: event.ContentPrompt, Body: body}},
		TurnKey:  turnKey,
	}
}

// baseEvent 는 events 의 NOT NULL 계약을 만족하는 최소 이벤트다. seq 가 record_hash 를
// 서로 다르게 만드는 손잡이다.
func baseEvent(sessionKey, name string, at time.Time, seq int) event.Event {
	return event.Event{
		Vendor:         vendorClaude,
		InstallationID: "inst-1",
		Signal:         event.SignalLog,
		Name:           name,
		TS:             event.NanoFromTime(at),
		SessionID:      sessionKey,
		Sequence:       seq,
	}
}

type llmSpec struct {
	Model  string
	Cost   float64
	Input  int64
	Output int64
}

// llmRecord 는 claude_code.api_request 로그다 — store 가 llm_calls 로 승격한다.
func llmRecord(sessionKey, turnKey string, at time.Time, seq int, spec llmSpec) store.EventRecord {
	e := baseEvent(sessionKey, vendorClaude+".api_request", at, seq)
	e.Attr.Model = spec.Model
	e.Measure.CostUSD = event.Some(spec.Cost)
	e.Measure.InputTokens = event.Some(spec.Input)
	e.Measure.OutputTokens = event.Some(spec.Output)
	return store.EventRecord{Event: e, TurnKey: turnKey}
}

// ── 한도 씨앗 ───────────────────────────────────────────────────────────────

func availableResult(v vendorlimit.Vendor, windows ...vendorlimit.Window) vendorlimit.Result {
	if windows == nil {
		windows = []vendorlimit.Window{}
	}
	return vendorlimit.Result{Vendor: v, State: vendorlimit.StateAvailable,
		Windows: windows, ObservedAt: "2026-08-10T02:00:00Z"}
}

// unavailableResult 는 실패한 벤더다. 창이 있어도 후보가 되면 안 된다.
func unavailableResult(v vendorlimit.Vendor, reason vendorlimit.Reason, windows ...vendorlimit.Window) vendorlimit.Result {
	if windows == nil {
		windows = []vendorlimit.Window{}
	}
	return vendorlimit.Result{Vendor: v, State: vendorlimit.StateUnavailable, Reason: reason,
		Windows: windows, ObservedAt: "2026-08-10T02:00:00Z"}
}

func window(period vendorlimit.PeriodKind, label string, ratio float64, resetsIn int64) vendorlimit.Window {
	return vendorlimit.Window{Period: period, Label: label, UsedRatio: ratio, ResetsInSeconds: resetsIn}
}

// stubCollector 는 한도 조회를 대신한다. 호출 횟수로 갱신 주기를 검증한다.
type stubCollector struct {
	mu    sync.Mutex
	calls int
	snap  vendorlimit.Snapshot
}

// builderSource 는 데몬 전용 Builder를 GUI 캐시 테스트에만 연결한다. 운영 Builder에
// 외부 갱신 메서드를 억지로 추가하지 않기 위한 테스트 어댑터다.
type builderSource struct{ *Builder }

func (s builderSource) Refresh(ctx context.Context, q Query) (Snapshot, error) {
	return s.Snapshot(ctx, q)
}

func (c *stubCollector) collect(context.Context) vendorlimit.Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.snap
}

func (c *stubCollector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// newTestCache 는 벽시계와 실제 한도 조회에 닿지 않는 캐시다. 출처는 같은 프로세스의
// Builder 라 로컬 질의는 진짜로 돌고, 한도만 stub 으로 갈아끼운다.
func newTestCache(svc *dashboard.Service, snap vendorlimit.Snapshot) (*Cache, *stubCollector, *time.Time) {
	col := &stubCollector{snap: snap}
	clock := testNow
	b := NewBuilder(svc)
	b.now = func() time.Time { return clock }
	b.limits = col.collect
	m := New(builderSource{b})
	m.now = func() time.Time { return clock }
	return m, col, &clock
}

// newTestCacheWithLimits 는 한도 조회를 통째로 갈아끼운 캐시다. 로컬 질의는 진짜로 돈다.
func newTestCacheWithLimits(svc *dashboard.Service, limits func(context.Context) vendorlimit.Snapshot) *Cache {
	clock := testNow
	b := NewBuilder(svc)
	b.now = func() time.Time { return clock }
	b.limits = limits
	m := New(builderSource{b})
	m.now = func() time.Time { return clock }
	return m
}

// ── 단언 보조 ───────────────────────────────────────────────────────────────

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// assertSnakeCaseTags 는 공개 응답 타입의 json 태그가 전부 snake_case 인지 본다. 태그가
// 곧 TS 필드명이라(ADR 0004) 빠뜨리면 화면이 undefined 만 읽고 아무 데서도 실패하지 않는다.
func assertSnakeCaseTags(t *testing.T, v any) {
	t.Helper()
	rt := reflect.TypeOf(v)
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, ok := f.Tag.Lookup("json")
		if !ok {
			t.Errorf("%s.%s 에 json 태그가 없다", rt.Name(), f.Name)
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || !isSnakeCase(name) {
			t.Errorf("%s.%s 의 json 태그 %q 가 snake_case 가 아니다", rt.Name(), f.Name, name)
		}
	}
}

func isSnakeCase(s string) bool {
	for _, r := range s {
		if r != '_' && !unicode.IsDigit(r) && !(r >= 'a' && r <= 'z') {
			return false
		}
	}
	return s != "" && s[0] != '_'
}
