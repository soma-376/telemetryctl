package store

import (
	"context"
	"testing"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
)

// 이벤트 한 건이 들어가고 v3 의 부모 계층이 통째로 생기는지 본다.
// vendors → sessions → turns → events 가 한 트랜잭션에서 순서대로 만들어져야 한다.
func TestWriteCreatesParentChain(t *testing.T) {
	db := openTestDB(t)

	res := mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1"), promptBody("토큰 검증을 붙여 줘")),
	}})
	if res.EventsInserted != 1 || res.EventsDuplicate != 0 {
		t.Fatalf("WriteResult = %+v", res)
	}
	if res.VendorsTouched != 1 || res.SessionsUpserted != 1 || res.TurnsUpserted != 1 {
		t.Fatalf("부모 계층이 안 생겼다: %+v", res)
	}

	for _, table := range []string{"vendors", "sessions", "turns", "events"} {
		if n := countRows(t, db, table); n != 1 {
			t.Errorf("%s = %d행, want 1", table, n)
		}
	}
	assertNoOrphans(t, db)

	var (
		name       string
		occurredAt int64
		seq        int64
		hash       string
		payload    any
	)
	row := db.SQL().QueryRowContext(context.Background(),
		`SELECT event_name, occurred_at, seq, record_hash, payload FROM events`)
	if err := row.Scan(&name, &occurredAt, &seq, &hash, &payload); err != nil {
		t.Fatalf("events 조회: %v", err)
	}
	if name != "claude_code.user_prompt" {
		t.Errorf("event_name = %q", name)
	}
	// 모든 시각 컬럼의 단위는 초다 (ADR 0009). 나노초가 들어가면 보존 정책이 오작동한다.
	if want := int64(event.NanoFromTime(baseTime).Sec()); occurredAt != want {
		t.Errorf("occurred_at = %d, want %d (초)", occurredAt, want)
	}
	if seq != 1 {
		t.Errorf("seq = %d, want 1", seq)
	}
	// record_hash 는 event.DedupKey 를 그대로 쓴다. 컬럼 이름만 바뀌었다.
	if want := evrec("claude_code.user_prompt", baseTime, 0).Event.DedupKey(); hash != want {
		t.Errorf("record_hash = %q, want %q", hash, want)
	}
	// payload 는 NULL 이다. 원본 OTLP 를 붙들고 있는 경로가 없다 (ADR 0002·0003).
	if payload != nil {
		t.Errorf("payload = %v, want NULL", payload)
	}

	// 프롬프트 원문은 turns.prompt_text 로만 살아남는다.
	if got := scanOne(t, db, `SELECT prompt_text FROM turns`); got != "토큰 검증을 붙여 줘" {
		t.Errorf("prompt_text = %v", got)
	}
	if res.PromptsStored != 1 {
		t.Errorf("PromptsStored = %d, want 1", res.PromptsStored)
	}
}

// 같은 record_hash 는 두 번 들어가지 않는다. 재전송은 에러가 아니라 정상 동작이다.
func TestWriteDeduplicatesByRecordHash(t *testing.T) {
	db := openTestDB(t)
	batch := Batch{Events: []EventRecord{
		evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1")),
	}}

	first := mustWrite(t, db, batch)
	second := mustWrite(t, db, batch)

	if first.EventsInserted != 1 || first.EventsDuplicate != 0 {
		t.Fatalf("첫 Write = %+v", first)
	}
	if second.EventsInserted != 0 || second.EventsDuplicate != 1 {
		t.Fatalf("두 번째 Write = %+v", second)
	}
	if n := countRows(t, db, "events"); n != 1 {
		t.Fatalf("events = %d행, want 1", n)
	}
	// 턴도 늘지 않는다 — 같은 turn_key 는 같은 행이다.
	if n := countRows(t, db, "turns"); n != 1 {
		t.Fatalf("turns = %d행, want 1", n)
	}
}

// 한 배치 안에 같은 이벤트가 두 번 들어와도 접힌다. 사전 조회는 트랜잭션 시작 시점의 DB 만
// 보므로 여기서 접지 않으면 두 번째가 ON CONFLICT 로 떨어지며 seq 에 구멍을 낸다.
func TestWriteDeduplicatesWithinBatch(t *testing.T) {
	db := openTestDB(t)
	e := evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1"))

	res := mustWrite(t, db, Batch{Events: []EventRecord{e, e}})
	if res.EventsInserted != 1 || res.EventsDuplicate != 1 {
		t.Fatalf("WriteResult = %+v", res)
	}
	if got := scanOne(t, db, `SELECT MAX(seq) FROM events`); got != int64(1) {
		t.Fatalf("seq = %v, want 1 — 중복이 번호를 태웠다", got)
	}
}

// 세션 스냅샷은 v3 sessions 의 식별 정보 컬럼을 채운다 (ADR 0010).
func TestWriteSessionSnapshotFillsIdentityColumns(t *testing.T) {
	db := openTestDB(t)
	s := newSession("sess-1", baseTime)
	s.EndedAt = event.Some(s.StartedAt + 600)

	mustWrite(t, db, Batch{Sessions: []session.Session{s}})

	var (
		vendorID, key                          string
		title, workspace, email, account, term any
		startedAt, endedAt, activeTime         any
		wantStart                              int64 = int64(s.StartedAt)
	)
	row := db.SQL().QueryRowContext(context.Background(), `
		SELECT vendor_id, session_key, title, workspace_path, user_email, user_account_id,
		       terminal_type, started_at, ended_at, active_time_sec
		FROM sessions`)
	if err := row.Scan(&vendorID, &key, &title, &workspace, &email, &account,
		&term, &startedAt, &endedAt, &activeTime); err != nil {
		t.Fatalf("sessions 조회: %v", err)
	}

	if vendorID != "claude_code" || key != "sess-1" {
		t.Errorf("식별자 = %q/%q", vendorID, key)
	}
	// 조립기는 제목을 만들지 않는다. 벤더 제목이 오기 전까지 NULL 이다 (PROJ-124).
	if title != nil {
		t.Errorf("title = %v, want NULL", title)
	}
	if workspace != s.WorkspacePath || email != s.UserEmail || account != s.UserAccountID {
		t.Errorf("식별 정보가 안 실렸다: %v / %v / %v", workspace, email, account)
	}
	if term != "iTerm.app" {
		t.Errorf("terminal_type = %v", term)
	}
	if startedAt != wantStart || endedAt != int64(s.StartedAt+600) {
		t.Errorf("시각 = %v / %v", startedAt, endedAt)
	}
	if activeTime != int64(120) {
		t.Errorf("active_time_sec = %v, want 120", activeTime)
	}
}

// 스냅샷은 제목을 건드리지 않는다. sessions.title 은 벤더 제목 UPDATE 만 쓰므로,
// 스냅샷이 여러 번 와도 이미 저장된 제목을 지우거나 덮지 않아야 한다 (PROJ-124).
func TestWriteSessionDoesNotTouchTitle(t *testing.T) {
	db := openTestDB(t)
	mustWrite(t, db, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}})
	if err := db.SetClaudeTitle(context.Background(), "sess-1", "벤더가 준 제목"); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, db, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}})

	if got := scanOne(t, db, `SELECT title FROM sessions`); got != "벤더가 준 제목" {
		t.Fatalf("title = %v — 스냅샷이 벤더 제목을 건드렸다", got)
	}
	if n := countRows(t, db, "sessions"); n != 1 {
		t.Fatalf("sessions = %d행, want 1", n)
	}
}

// vendors.status 는 사용자 설정이지 관측이 아니다. 배치마다 덮어쓰면 Settings 토글이
// 동작하지 않는 것처럼 보인다.
func TestWriteDoesNotOverwriteVendorStatus(t *testing.T) {
	db := openTestDB(t)
	mustWrite(t, db, Batch{Events: []EventRecord{evrec("claude_code.user_prompt", baseTime, 0)}})

	if _, err := db.SQL().ExecContext(context.Background(),
		`UPDATE vendors SET status = 'disabled'`); err != nil {
		t.Fatalf("status 변경: %v", err)
	}
	mustWrite(t, db, Batch{Events: []EventRecord{evrec("claude_code.api_request", baseTime, 1)}})

	if got := scanOne(t, db, `SELECT status FROM vendors`); got != "disabled" {
		t.Fatalf("status = %v, want disabled — 쓰기가 사용자 설정을 덮었다", got)
	}
}

// first_seen 은 가장 이른 관측, last_seen 은 가장 늦은 관측이다.
func TestWriteVendorSpanWidens(t *testing.T) {
	db := openTestDB(t)
	later := baseTime.Add(3600 * 1e9)
	earlier := baseTime.Add(-3600 * 1e9)

	mustWrite(t, db, Batch{Events: []EventRecord{evrec("claude_code.user_prompt", baseTime, 0)}})
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.api_request", later, 1),
		evrec("claude_code.api_request", earlier, 2),
	}})

	first := scanOne(t, db, `SELECT first_seen FROM vendors`)
	last := scanOne(t, db, `SELECT last_seen FROM vendors`)
	if first != int64(event.SecFromTime(earlier)) {
		t.Errorf("first_seen = %v, want %d", first, event.SecFromTime(earlier))
	}
	if last != int64(event.SecFromTime(later)) {
		t.Errorf("last_seen = %v, want %d", last, event.SecFromTime(later))
	}
}

// --no-store-content 는 프롬프트 원문까지 버린다. 저장소가 프라이버시 모드의 집행 지점이다.
func TestWriteContentStorageDisabled(t *testing.T) {
	db := openTestDB(t, WithContentStorage(false))

	res := mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1"), promptBody("비밀 프롬프트")),
	}})
	if res.PromptsStored != 0 || res.ContentsDropped != 1 {
		t.Fatalf("WriteResult = %+v", res)
	}
	if got := scanOne(t, db, `SELECT COUNT(*) FROM turns WHERE prompt_text IS NOT NULL`); got != int64(0) {
		t.Fatalf("prompt_text 가 남았다: %v", got)
	}
	// 이벤트 자체는 남는다 — 수치와 타임라인은 원문이 아니다.
	if n := countRows(t, db, "events"); n != 1 {
		t.Fatalf("events = %d행, want 1", n)
	}
}

// 프롬프트가 아닌 원문은 v3 에 저장될 컬럼이 없다. 조용히 사라지지 않고 계수에 잡혀야 한다.
func TestWriteDropsNonPromptContent(t *testing.T) {
	db := openTestDB(t)
	rec := evrec("claude_code.tool_result", baseTime, 0, inTurn("p1"))
	rec.Contents = []event.Content{
		{Kind: event.ContentToolInput, Body: `{"file_path":"/tmp/a.go"}`},
		{Kind: event.ContentToolResult, Body: "ok"},
	}

	res := mustWrite(t, db, Batch{Events: []EventRecord{rec}})
	if res.ContentsDropped != 2 || res.PromptsStored != 0 {
		t.Fatalf("WriteResult = %+v", res)
	}
}

// 빈 배치는 트랜잭션도 열지 않는다.
func TestWriteEmptyBatch(t *testing.T) {
	db := openTestDB(t)
	res := mustWrite(t, db, Batch{})
	if res != (WriteResult{}) {
		t.Fatalf("WriteResult = %+v, want 제로값", res)
	}
}

// 이벤트 하나가 계약을 어기면 배치 전체가 롤백된다. 부분 적용은 화면의 모순이다.
func TestWriteIsAtomic(t *testing.T) {
	db := openTestDB(t)
	broken := evrec("claude_code.user_prompt", baseTime, 1, inTurn("p1"))
	broken.Event.Vendor = "" // Validate 가 거부한다

	_, err := db.Write(context.Background(), Batch{
		Sessions: []session.Session{newSession("sess-1", baseTime)},
		Events: []EventRecord{
			evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1")),
			broken,
		},
	})
	if err == nil {
		t.Fatal("검증에 실패한 이벤트인데 Write 가 성공했다")
	}
	for _, table := range []string{"vendors", "sessions", "turns", "events"} {
		if n := countRows(t, db, table); n != 0 {
			t.Errorf("%s = %d행, want 0 — 롤백되지 않았다", table, n)
		}
	}
}

// session.id 가 없는 이벤트는 붙을 턴이 없다. events.turn_id 가 NOT NULL 이라 저장할 수 없다.
func TestWriteSkipsEventsWithoutSession(t *testing.T) {
	db := openTestDB(t)
	rec := evrec("claude_code.user_prompt", baseTime, 0)
	rec.Event.SessionID = ""

	res := mustWrite(t, db, Batch{Events: []EventRecord{rec}})
	if res.EventsInserted != 0 {
		t.Fatalf("WriteResult = %+v", res)
	}
	if n := countRows(t, db, "events"); n != 0 {
		t.Fatalf("events = %d행, want 0", n)
	}
	assertNoOrphans(t, db)
}
