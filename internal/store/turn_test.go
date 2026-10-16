package store

import (
	"context"
	"testing"
	"time"
)

// 가상 턴은 세션당 하나뿐이다 — ux_turns_virtual 이 turn_index IS NULL 행을 하나로 제한한다.
// 두 번째 세션 수준 이벤트가 두 번째 가상 턴을 만들려 하면 그 인덱스에 걸려 배치가 죽는다.
func TestVirtualTurnIsSingletonPerSession(t *testing.T) {
	db := openTestDB(t)

	// TurnKey 가 빈 이벤트 셋. 배치 하나 안에서, 그리고 배치를 넘겨서도 한 행이어야 한다.
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.session.count", baseTime, 0),
		evrec("claude_code.mcp.connection", baseTime, 1),
	}})
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.active_time.total", baseTime.Add(time.Second), 0),
	}})

	if n := countWhere(t, db, "turns", "turn_index IS NULL"); n != 1 {
		t.Fatalf("가상 턴 = %d행, want 1", n)
	}
	if n := countRows(t, db, "events"); n != 3 {
		t.Fatalf("events = %d행, want 3", n)
	}
	if got := scanOne(t, db, `SELECT turn_key FROM turns`); got != virtualTurnKey {
		t.Fatalf("turn_key = %q, want 센티널", got)
	}
	assertNoOrphans(t, db)
}

// 세션이 다르면 가상 턴도 각자 하나씩이다.
func TestVirtualTurnPerSession(t *testing.T) {
	db := openTestDB(t)
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.session.count", baseTime, 0, sess("s1")),
		evrec("claude_code.session.count", baseTime, 0, sess("s2")),
	}})

	if n := countWhere(t, db, "turns", "turn_index IS NULL"); n != 2 {
		t.Fatalf("가상 턴 = %d행, want 2", n)
	}
}

// 실제 턴은 세션별 단조 증가 번호를 받는다. 번호를 카운터에서 뽑지 않으면
// (session_id, turn_index) UNIQUE 에 걸려 ON CONFLICT(session_id, turn_key) 가 잡지 못한다.
func TestTurnIndexIsMonotonicPerSession(t *testing.T) {
	db := openTestDB(t)

	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1")),
		evrec("claude_code.user_prompt", baseTime, 1, inTurn("p2")),
	}})
	// 배치를 넘겨서도 이어져야 한다. 워터마크를 DB 에서 다시 읽는지 보는 것이 핵심이다.
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.user_prompt", baseTime.Add(time.Second), 0, inTurn("p3")),
	}})

	rows, err := db.SQL().QueryContext(context.Background(),
		`SELECT turn_key, turn_index FROM turns ORDER BY turn_index`)
	if err != nil {
		t.Fatalf("turns 조회: %v", err)
	}
	defer rows.Close()

	var keys []string
	var indexes []int64
	for rows.Next() {
		var k string
		var i int64
		if err := rows.Scan(&k, &i); err != nil {
			t.Fatalf("scan: %v", err)
		}
		keys = append(keys, k)
		indexes = append(indexes, i)
	}
	if len(keys) != 3 {
		t.Fatalf("turns = %d행, want 3", len(keys))
	}
	for i, want := range []int64{0, 1, 2} {
		if indexes[i] != want {
			t.Fatalf("turn_index = %v, want [0 1 2]", indexes)
		}
	}
	if keys[0] != "p1" || keys[2] != "p3" {
		t.Fatalf("turn_key 순서 = %v", keys)
	}
}

// 같은 턴을 여러 배치에 걸쳐 다시 만나도 번호를 새로 태우지 않는다.
// 태우면 열린 턴이 배치마다 밀려 turn_index 에 커다란 구멍이 생긴다.
func TestExistingTurnDoesNotBurnIndex(t *testing.T) {
	db := openTestDB(t)

	for i := range 5 {
		mustWrite(t, db, Batch{Events: []EventRecord{
			evrec("claude_code.api_request", baseTime.Add(time.Duration(i)*time.Second), i, inTurn("p1")),
		}})
	}
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.user_prompt", baseTime.Add(time.Minute), 0, inTurn("p2")),
	}})

	if got := scanOne(t, db, `SELECT turn_index FROM turns WHERE turn_key = 'p2'`); got != int64(1) {
		t.Fatalf("두 번째 턴의 turn_index = %v, want 1", got)
	}
}

// 가상 턴은 UPSERT 로 갱신할 것이 하나도 없다. DO NOTHING 이면 RETURNING 이 아무 행도
// 주지 않아 id 를 못 받는다 — 자기 대입 DO UPDATE 가 그 함정을 막는다.
func TestVirtualTurnUpsertStillReturnsID(t *testing.T) {
	db := openTestDB(t)

	// 캐시를 태우지 않고 같은 가상 턴을 서로 다른 트랜잭션에서 두 번 만난다.
	mustWrite(t, db, Batch{Events: []EventRecord{evrec("claude_code.session.count", baseTime, 0)}})
	res := mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.session.count", baseTime.Add(time.Second), 0),
	}})
	if res.EventsInserted != 1 {
		t.Fatalf("두 번째 Write = %+v — 가상 턴 id 를 못 받았다", res)
	}
	if n := countRows(t, db, "events"); n != 2 {
		t.Fatalf("events = %d행, want 2", n)
	}
	assertNoOrphans(t, db)
}

// seq 는 턴 안에서 도착 순서대로 1 부터 증가한다. 배치를 넘겨도 이어진다.
func TestSeqIsMonotonicPerTurn(t *testing.T) {
	db := openTestDB(t)

	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1")),
		evrec("claude_code.api_request", baseTime, 1, inTurn("p1")),
	}})
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.tool_result", baseTime.Add(time.Second), 0, inTurn("p1")),
	}})

	rows, err := db.SQL().QueryContext(context.Background(),
		`SELECT seq FROM events ORDER BY seq`)
	if err != nil {
		t.Fatalf("events 조회: %v", err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("seq = %v, want [1 2 3]", got)
	}
}

// 턴이 다르면 seq 도 각자 1 부터다. (turn_id, seq) 가 UNIQUE 라 섞이면 삽입이 실패한다.
func TestSeqIsScopedToTurn(t *testing.T) {
	db := openTestDB(t)
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1")),
		evrec("claude_code.user_prompt", baseTime, 1, inTurn("p2")),
	}})

	if got := scanOne(t, db, `SELECT COUNT(*) FROM events WHERE seq = 1`); got != int64(2) {
		t.Fatalf("seq=1 인 행 = %v, want 2", got)
	}
}

// seq 는 **도착 순서**이지 벤더 시각이 아니다 (ADR 0009).
// 뒤집혀 도착해도 정상 입력이고, 이미 저장된 행을 재번호하지 않는다.
func TestSeqFollowsArrivalNotOccurredAt(t *testing.T) {
	db := openTestDB(t)
	late := baseTime.Add(time.Minute)

	mustWrite(t, db, Batch{Events: []EventRecord{
		// 나중에 일어난 이벤트가 먼저 도착했다.
		evrec("claude_code.api_request", late, 0, inTurn("p1")),
		evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1")),
	}})

	rows, err := db.SQL().QueryContext(context.Background(),
		`SELECT event_name, seq, occurred_at FROM events ORDER BY seq`)
	if err != nil {
		t.Fatalf("events 조회: %v", err)
	}
	defer rows.Close()

	type row struct {
		name string
		seq  int64
		at   int64
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.seq, &r.at); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("events = %d행", len(got))
	}
	if got[0].name != "claude_code.api_request" || got[1].name != "claude_code.user_prompt" {
		t.Fatalf("seq 순서가 도착 순서가 아니다: %+v", got)
	}
	// 독자는 ORDER BY occurred_at, seq 로 읽는다 — 그 순서에서는 프롬프트가 먼저다.
	if got[0].at <= got[1].at {
		t.Fatalf("occurred_at 이 뒤집히지 않았다: %+v", got)
	}
}

// 중복이라 건너뛴 이벤트는 seq 를 태우지 않는다. 태우면 턴 안의 번호에 이유 없는 구멍이 생긴다.
func TestDuplicateDoesNotBurnSeq(t *testing.T) {
	db := openTestDB(t)
	first := evrec("claude_code.user_prompt", baseTime, 0, inTurn("p1"))
	mustWrite(t, db, Batch{Events: []EventRecord{first}})

	mustWrite(t, db, Batch{Events: []EventRecord{
		first, // 중복
		evrec("claude_code.api_request", baseTime, 1, inTurn("p1")),
	}})

	if got := scanOne(t, db, `SELECT MAX(seq) FROM events`); got != int64(2) {
		t.Fatalf("MAX(seq) = %v, want 2", got)
	}
	if n := countRows(t, db, "events"); n != 2 {
		t.Fatalf("events = %d행, want 2", n)
	}
}

// 벤더가 만들 수 없는 센티널이어야 한다. 벤더가 준 turn_key 와 부딪히면 세션 수준 이벤트가
// 특정 턴의 것으로 둔갑한다.
func TestVirtualTurnKeyIsUnreachableByVendors(t *testing.T) {
	if virtualTurnKey == "" {
		t.Fatal("센티널이 빈 문자열이면 실제 턴과 구분되지 않는다")
	}
	for _, c := range virtualTurnKey {
		if c == 0 {
			return
		}
	}
	t.Fatalf("센티널 %q 에 NUL 바이트가 없다 — 벤더 식별자와 부딪힐 수 있다", virtualTurnKey)
}
