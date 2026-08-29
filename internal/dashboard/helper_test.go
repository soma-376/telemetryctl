package dashboard

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

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

	vendorClaude = "claude_code"
	vendorCodex  = "codex"

	// workspaceA 의 basename 이 프로젝트 이름으로 화면에 나온다 (DimProject).
	workspaceA = "/home/jy/dev/telemetryctl"
	workspaceB = "/home/jy/dev/pulsemetry-backend"
)

type fixture struct {
	t      *testing.T
	dir    string
	path   string
	db     *store.DB
	reader *Reader
}

// newFixture 는 실제 SQLite 파일을 만들고 쓰기 핸들과 조회 핸들을 함께 연다.
// store 로 쓰고 dashboard 로 읽는 왕복이 이 패키지 테스트의 기본형이다 — 조회 계층을
// 흉내 내면 "dashboard 가 v3 스키마를 잘못 읽는다" 는 부류의 버그가 통째로 빠져나간다.
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

func (f *fixture) write(b store.Batch) {
	f.t.Helper()
	if _, err := f.db.Write(context.Background(), b); err != nil {
		f.t.Fatalf("store.Write: %v", err)
	}
}

// sessionID 는 (vendor, session_key) 의 대리 키다.
//
// v3 에서 세션을 가리키는 것은 sessions.id 하나뿐인데 그 값은 저장 시점에 SQLite 가
// 매기므로 테스트가 미리 알 수 없다. Session(id) 를 부르려면 여기서 되찾아야 한다.
func (f *fixture) sessionID(vendor, key string) int64 {
	f.t.Helper()
	var id int64
	err := f.db.SQL().QueryRowContext(context.Background(),
		`SELECT id FROM sessions WHERE vendor_id = ? AND session_key = ?`, vendor, key).Scan(&id)
	if err != nil {
		f.t.Fatalf("세션 id 조회 (%s/%s): %v", vendor, key, err)
	}
	return id
}

// ── 세션 스냅샷 ─────────────────────────────────────────────────────────────

// newSession 은 최소 필드를 채운 세션 스냅샷이다. 기본은 마감된(completed) 세션이다.
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
// 것이 곧 running 이라 EndedAt 을 비우는 것이 핵심이다 (ADR 0009).
func running(s *session.Session) {
	s.Status = session.StatusRunning
	s.EndedAt = event.Opt[event.UnixSec]{}
}

func codex(s *session.Session) { s.Vendor = vendorCodex }

// ── 이벤트 ──────────────────────────────────────────────────────────────────

// baseEvent 는 events 의 NOT NULL 계약을 만족하는 최소 이벤트다.
// seq 가 record_hash 를 서로 다르게 만드는 손잡이다.
func baseEvent(vendor, sessionKey, name string, at time.Time, seq int) event.Event {
	return event.Event{
		Vendor:         vendor,
		InstallationID: "inst-1",
		Signal:         event.SignalLog,
		Name:           name,
		TS:             event.NanoFromTime(at),
		SessionID:      sessionKey,
		Sequence:       seq,
	}
}

// promptRecord 는 사용자 프롬프트다. v3 에는 원문 테이블이 없어 본문은
// turns.prompt_text 에만 남는다 — 원문 검색이 그 컬럼을 읽는다.
func promptRecord(sessionKey, turnKey string, at time.Time, seq int, body string) store.EventRecord {
	return store.EventRecord{
		Event:    baseEvent(vendorClaude, sessionKey, "claude_code.user_prompt", at, seq),
		Contents: []event.Content{{Kind: event.ContentPrompt, Body: body}},
		TurnKey:  turnKey,
	}
}

// llmSpec 은 llmRecord 가 실어 보낼 수치다.
type llmSpec struct {
	Vendor    string
	Model     string
	Cost      float64
	Input     int64
	Output    int64
	CacheRead int64
	CacheWrit int64
}

// llmRecord 는 claude_code.api_request 로그다 — store 가 llm_calls 로 승격한다.
// 비용·토큰의 유일한 출처이므로 조회 쪽 집계 테스트는 전부 이것을 씨앗으로 쓴다.
func llmRecord(sessionKey, turnKey string, at time.Time, seq int, spec llmSpec) store.EventRecord {
	vendor := spec.Vendor
	if vendor == "" {
		vendor = vendorClaude
	}
	e := baseEvent(vendor, sessionKey, vendor+".api_request", at, seq)
	e.Attr.Model = spec.Model
	e.Measure.CostUSD = event.Some(spec.Cost)
	e.Measure.InputTokens = event.Some(spec.Input)
	e.Measure.OutputTokens = event.Some(spec.Output)
	e.Measure.CacheReadTokens = event.Some(spec.CacheRead)
	e.Measure.CacheCreationTokens = event.Some(spec.CacheWrit)
	return store.EventRecord{Event: e, TurnKey: turnKey}
}

// toolSpec 은 toolRecord 가 실어 보낼 값이다.
type toolSpec struct {
	Vendor    string
	ToolName  string
	MCPServer string
	Decision  string
	Success   event.Opt[bool]
	ErrorType string
	// Target 은 도구가 건드린 파일의 원경로다 (ADR 0010, 로컬 저장 전용).
	Target string
	// File 이 비어 있지 않으면 file_changes 한 행이 함께 만들어진다.
	File session.FileChange
}

// toolRecord 는 claude_code.tool_result 로그다 — store 가 tool_calls 로 승격한다.
// callKey 는 전역 UNIQUE 라 테스트마다 서로 달라야 한다.
func toolRecord(sessionKey, turnKey, callKey string, at time.Time, seq int, spec toolSpec) store.EventRecord {
	vendor := spec.Vendor
	if vendor == "" {
		vendor = vendorClaude
	}
	e := baseEvent(vendor, sessionKey, vendor+".tool_result", at, seq)
	e.Attr.ToolName = spec.ToolName
	e.Attr.MCPServer = spec.MCPServer
	e.Attr.Decision = spec.Decision
	e.Measure.Success = spec.Success
	e.Measure.ErrorType = spec.ErrorType
	return store.EventRecord{
		Event:      e,
		TurnKey:    turnKey,
		CallKey:    callKey,
		TargetPath: spec.Target,
		File:       spec.File,
	}
}

// fileChange 는 file_changes 한 행을 만드는 최소 명세다.
func fileChange(path string, added, removed int64) session.FileChange {
	return session.FileChange{
		Path:      path,
		Operation: session.OperationModify,
		Additions: event.Some(added),
		Deletions: event.Some(removed),
	}
}

// ── 단언 보조 ───────────────────────────────────────────────────────────────

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

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// assertSnakeCaseTags 는 공개 응답 타입의 json 태그가 전부 snake_case 인지 본다.
//
// 태그가 곧 TS 필드명이다 (ADR 0004). 태그를 빠뜨리면 Go 필드 이름이 그대로 나가 화면이
// PascalCase 를 읽게 되고, 그 어긋남은 어디서도 실패하지 않은 채 undefined 로만 나타난다.
func assertSnakeCaseTags(t *testing.T, v any) {
	t.Helper()
	rt := reflect.TypeOf(v)
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Anonymous {
			// 임베드는 JSON 에서 평평하게 펼쳐지므로 그 타입을 따로 검사한다.
			assertSnakeCaseTags(t, reflect.Zero(f.Type).Interface())
			continue
		}
		tag, ok := f.Tag.Lookup("json")
		if !ok {
			t.Errorf("%s.%s 에 json 태그가 없다 — Go 필드 이름이 그대로 TS 로 나간다", rt.Name(), f.Name)
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
