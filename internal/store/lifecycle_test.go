package store

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
)

// v3 에는 sessions.status 컬럼이 없다. 화면의 running/completed 는 ended_at IS NULL 로
// 계산되므로 (ADR 0009) 생명주기 시각이 곧 상태다. 이 파일은 그 시각과 active_time_sec 가
// 스냅샷을 정확히 따라가는지 고정한다.

// TestSessionEndedAtFollowsSnapshot 은 마감·재개가 컬럼에 그대로 반영되는지 본다.
func TestSessionEndedAtFollowsSnapshot(t *testing.T) {
	running := func() session.Session { return newSession("sess-1", baseTime) }
	closed := func() session.Session {
		s := newSession("sess-1", baseTime)
		s.EndedAt = someSec(s.StartedAt + 600)
		return s
	}

	tests := []struct {
		name     string
		snapshot []session.Session
		wantEnd  any
	}{
		{
			name:     "진행 중 → 마감",
			snapshot: []session.Session{running(), closed()},
			wantEnd:  int64(event.SecFromTime(baseTime)) + 600,
		},
		{
			// 마감된 세션에 같은 session.id 로 이벤트가 다시 오면 조립기가 마감을 되돌린다.
			// 저장 쪽이 옛 ended_at 을 붙들면 실제로 도는 세션이 영원히 completed 로 보인다.
			name:     "마감 → 재개하면 ended_at 이 NULL 로 돌아온다",
			snapshot: []session.Session{closed(), running()},
			wantEnd:  nil,
		},
		{
			name:     "마감 → 마감",
			snapshot: []session.Session{closed(), closed()},
			wantEnd:  int64(event.SecFromTime(baseTime)) + 600,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openTestDB(t)
			for _, s := range tt.snapshot {
				mustWrite(t, db, Batch{Sessions: []session.Session{s}})
			}
			if got := scanOne(t, db, `SELECT ended_at FROM sessions`); got != tt.wantEnd {
				t.Fatalf("ended_at = %v, want %v", got, tt.wantEnd)
			}
		})
	}
}

// 이벤트 씨앗은 생명주기를 모른다. 스냅샷 없이 이벤트만 저장되는 틱이 마감을 지우면 안 된다.
func TestSessionSeedDoesNotClearEndedAt(t *testing.T) {
	db := openTestDB(t)
	s := newSession("sess-1", baseTime)
	s.EndedAt = someSec(s.StartedAt + 600)
	mustWrite(t, db, Batch{Sessions: []session.Session{s}})

	// 스냅샷 없이 이벤트만 — 데몬의 이벤트 플러시 틱이 이 모양이다.
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.user_prompt", baseTime.Add(time.Minute), 0, sess("sess-1"), inTurn("p1")),
	}})

	want := int64(event.SecFromTime(baseTime)) + 600
	if got := scanOne(t, db, `SELECT ended_at FROM sessions`); got != want {
		t.Fatalf("ended_at = %v, want %d — 이벤트 씨앗이 마감을 지웠다", got, want)
	}
}

// 활동 시간은 세션 안에서 줄어들 수 없다. 데몬이 재시작하면 조립기가 0 부터 다시 세므로
// 새 값을 그대로 쓰면 이미 기록된 시간이 사라진다.
func TestSessionActiveTimeIsMonotonic(t *testing.T) {
	tests := []struct {
		name    string
		seconds []float64
		want    any
	}{
		{name: "증가하면 따라간다", seconds: []float64{120, 300}, want: int64(300)},
		{name: "줄어들면 지키지 않는다", seconds: []float64{300, 30}, want: int64(300)},
		{name: "0 은 미관측이라 덮지 않는다", seconds: []float64{300, 0}, want: int64(300)},
		{name: "한 번도 관측되지 않으면 NULL 이다", seconds: []float64{0, 0}, want: nil},
		{name: "소수 초는 반올림한다", seconds: []float64{0.6}, want: int64(1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openTestDB(t)
			for _, sec := range tt.seconds {
				s := newSession("sess-1", baseTime)
				s.ActiveSeconds = sec
				mustWrite(t, db, Batch{Sessions: []session.Session{s}})
			}
			if got := scanOne(t, db, `SELECT active_time_sec FROM sessions`); got != tt.want {
				t.Fatalf("active_time_sec = %v, want %v", got, tt.want)
			}
		})
	}
}

// started_at 은 가장 이른 관측이다. 늦게 도착한 배치가 시작 시각을 밀면 안 된다.
func TestSessionStartedAtKeepsEarliest(t *testing.T) {
	db := openTestDB(t)
	late := newSession("sess-1", baseTime)
	early := newSession("sess-1", baseTime.Add(-time.Hour))

	mustWrite(t, db, Batch{Sessions: []session.Session{late}})
	mustWrite(t, db, Batch{Sessions: []session.Session{early}})

	want := int64(event.SecFromTime(baseTime.Add(-time.Hour)))
	if got := scanOne(t, db, `SELECT started_at FROM sessions`); got != want {
		t.Fatalf("started_at = %v, want %d", got, want)
	}
}

// last_activity_at 은 started_at 의 거울상이다 — 가장 늦은 관측이고, 늦게 도착한 오래된
// 배치가 마지막 활동을 과거로 되돌리면 유휴 스윕이 살아 있는 세션을 마감한다.
func TestSessionLastActivityKeepsLatest(t *testing.T) {
	db := openTestDB(t)
	late := newSession("sess-1", baseTime)
	early := newSession("sess-1", baseTime.Add(-time.Hour))

	mustWrite(t, db, Batch{Sessions: []session.Session{late}})
	mustWrite(t, db, Batch{Sessions: []session.Session{early}})

	want := int64(event.SecFromTime(baseTime))
	if got := scanOne(t, db, `SELECT last_activity_at FROM sessions`); got != want {
		t.Fatalf("last_activity_at = %v, want %d", got, want)
	}
}

// 조립기 스냅샷 없이 이벤트만 저장되는 틱에도 활동 시각은 올라가야 한다. ended_at 과 달리
// "이 시각에 활동이 있었다" 는 이벤트 하나만으로 알 수 있기 때문이다 (sessionUpsertHead).
// 이것이 빠지면 조립기가 놓친 세션의 활동 시각이 멈춰 유휴 스윕이 오판한다.
func TestSessionLastActivityFollowsEventSeed(t *testing.T) {
	db := openTestDB(t)
	mustWrite(t, db, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}})

	later := baseTime.Add(30 * time.Minute)
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.api_request", later, 1, sess("sess-1")),
	}})

	want := int64(event.SecFromTime(later))
	if got := scanOne(t, db, `SELECT last_activity_at FROM sessions`); got != want {
		t.Fatalf("last_activity_at = %v, want %d — 이벤트 씨앗이 활동 시각을 안 올렸다", got, want)
	}
}

// 마감된 세션에 낙오 이벤트가 도착하는 것은 정상 경로다 (exporter 배치 지연). 그때 활동
// 시각만 오르고 마감은 그대로여야 한다 — 이벤트 씨앗이 ended_at 을 건드리면 마감된 세션이
// 화면에서 되살아난다 (seedSessionSQL).
func TestSessionLateEventMovesActivityNotEnd(t *testing.T) {
	db := openTestDB(t)
	closed := newSession("sess-1", baseTime)
	closed.EndedAt = someSec(closed.StartedAt + 600)
	mustWrite(t, db, Batch{Sessions: []session.Session{closed}})

	straggler := baseTime.Add(11 * time.Minute)
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.api_request", straggler, 1, sess("sess-1")),
	}})

	wantEnd := int64(event.SecFromTime(baseTime)) + 600
	if got := scanOne(t, db, `SELECT ended_at FROM sessions`); got != wantEnd {
		t.Fatalf("ended_at = %v, want %d — 낙오 이벤트가 마감을 흔들었다", got, wantEnd)
	}
	wantActivity := int64(event.SecFromTime(straggler))
	if got := scanOne(t, db, `SELECT last_activity_at FROM sessions`); got != wantActivity {
		t.Fatalf("last_activity_at = %v, want %d", got, wantActivity)
	}
}

// 원문 저장을 꺼도 세션·사용량 화면은 동작해야 한다 (PROJ-86 구현 경계).
// 원문은 turns.prompt_text 하나뿐이고 집계는 승격 테이블에서 나오므로 서로 독립이다.
func TestContentDisabledKeepsSessionAndUsageQueries(t *testing.T) {
	db := openTestDB(t, WithContentStorage(false))
	seedUsage(t, db, baseTime)

	if n := countWhere(t, db, "turns", "prompt_text IS NOT NULL"); n != 0 {
		t.Fatalf("원문 저장을 껐는데 prompt_text 가 %d행 남았다", n)
	}

	// 세션 목록 — 상태는 ended_at 으로 계산한다 (ADR 0009).
	var key, status string
	err := db.SQL().QueryRowContext(context.Background(), `
		SELECT session_key,
		       CASE WHEN ended_at IS NULL THEN 'running' ELSE 'completed' END
		FROM sessions`).Scan(&key, &status)
	if err != nil {
		t.Fatalf("세션 목록 조회: %v", err)
	}
	if key != "sess-1" || status != "running" {
		t.Fatalf("세션 = %q/%q", key, status)
	}

	// 사용량 — 조회 시점 GROUP BY 로 승격 테이블에서 만든다.
	var cost float64
	var inTok, calls int64
	err = db.SQL().QueryRowContext(context.Background(), `
		SELECT COALESCE(SUM(l.cost_usd), 0), COALESCE(SUM(l.input_tokens), 0), COUNT(*)
		FROM llm_calls l
		JOIN turns t ON t.id = l.turn_id
		JOIN sessions s ON s.id = t.session_id
		GROUP BY s.id`).Scan(&cost, &inTok, &calls)
	if err != nil {
		t.Fatalf("사용량 집계 조회: %v", err)
	}
	if cost != 0.5 || inTok != 100 || calls != 1 {
		t.Fatalf("사용량 = %v USD / %d 토큰 / %d 호출", cost, inTok, calls)
	}

	// 도구 집계도 마찬가지다.
	if n := countRows(t, db, "tool_calls"); n != 1 {
		t.Fatalf("tool_calls = %d행, want 1", n)
	}
}

// ── 유휴 스윕 (PROJ-67) ─────────────────────────────────────────────────────

// 스윕의 존재 이유다. 조립기 메모리에 없는 세션 — 데몬 재시작 전에 돌던 세션 — 도
// 마감되어야 한다. 여기서는 조립기를 아예 거치지 않고 DB 에 직접 만든 행으로 그 상황을
// 재현한다.
func TestCloseIdleSessionsClosesSessionsAssemblerNeverSaw(t *testing.T) {
	db := openTestDB(t)
	sec := event.SecFromTime(baseTime)
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.api_request", baseTime, 1, sess("sess-1")),
	}})

	n, err := db.CloseIdleSessions(context.Background(), sec+600)
	if err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}
	if n != 1 {
		t.Fatalf("마감한 세션 = %d개, want 1", n)
	}
	// 마감 시각은 감지 시각(컷오프)이 아니라 마지막 활동이다. 컷오프를 쓰면 모든 세션의
	// 소요 시간에 유휴 임계값이 유령처럼 붙는다.
	if got := scanOne(t, db, `SELECT ended_at FROM sessions`); got != int64(sec) {
		t.Fatalf("ended_at = %v, want %d", got, int64(sec))
	}
}

// 컷오프 경계와 이미 마감된 세션·근거 없는 세션의 처리를 한자리에서 고정한다.
func TestCloseIdleSessionsScope(t *testing.T) {
	sec := int64(event.SecFromTime(baseTime))

	tests := []struct {
		name    string
		setup   func(t *testing.T, db *DB)
		cutoff  event.UnixSec
		wantN   int
		wantEnd any
	}{
		{
			// 컷오프와 같은 시각은 아직 유휴가 아니다. 부등호가 < 라 경계가 열려 있다.
			name: "활동이 컷오프와 같으면 마감하지 않는다",
			setup: func(t *testing.T, db *DB) {
				mustWrite(t, db, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}})
			},
			cutoff:  event.UnixSec(sec),
			wantN:   0,
			wantEnd: nil,
		},
		{
			// 마감 뒤 낙오 이벤트가 활동 시각을 미는 것은 정상 경로다. 그때마다 마감 시각이
			// 뒤로 끌려가면 안 되므로 이미 마감된 세션은 아예 후보가 아니어야 한다.
			name: "이미 마감된 세션은 다시 건드리지 않는다",
			setup: func(t *testing.T, db *DB) {
				closed := newSession("sess-1", baseTime)
				closed.EndedAt = someSec(closed.StartedAt + 60)
				mustWrite(t, db, Batch{Sessions: []session.Session{closed}})
				mustWrite(t, db, Batch{Events: []EventRecord{
					evrec("claude_code.api_request", baseTime.Add(11*time.Minute), 1, sess("sess-1")),
				}})
			},
			cutoff:  event.UnixSec(sec + 86400),
			wantN:   0,
			wantEnd: sec + 60,
		},
		{
			// 판정할 근거가 없는 행은 손대지 않는다. 근거 없이 마감하면 되살릴 방법이 없다.
			name: "last_activity_at 이 NULL 이면 마감하지 않는다",
			setup: func(t *testing.T, db *DB) {
				mustWrite(t, db, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}})
				mustExecTest(t, db, `UPDATE sessions SET last_activity_at = NULL`)
			},
			cutoff:  event.UnixSec(sec + 86400),
			wantN:   0,
			wantEnd: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openTestDB(t)
			tt.setup(t, db)

			n, err := db.CloseIdleSessions(context.Background(), tt.cutoff)
			if err != nil {
				t.Fatalf("CloseIdleSessions: %v", err)
			}
			if n != tt.wantN {
				t.Fatalf("마감한 세션 = %d개, want %d", n, tt.wantN)
			}
			if got := scanOne(t, db, `SELECT ended_at FROM sessions`); got != tt.wantEnd {
				t.Fatalf("ended_at = %v, want %v", got, tt.wantEnd)
			}
		})
	}
}

// 스윕이 조립기보다 공격적으로 닫으면 매 틱 왕복이 난다 — 스윕이 닫고, 다음 스냅샷이
// (조립기는 여전히 running 이라 믿으므로) 도로 열고, 다시 스윕이 닫는다. 컷오프가 조립기
// 임계값보다 느슨하기만 하면 스윕이 닫는 것은 조립기가 이미 닫았을 것들뿐이라 충돌이 없다.
func TestCloseIdleSessionsDoesNotFightAssembler(t *testing.T) {
	const idle = 10 * time.Minute
	db := openTestDB(t)
	asm := session.New(session.WithIdleThreshold(idle))
	asm.Add(session.Input{Event: newEvent("claude_code.api_request", baseTime, 1)})

	// 조립기가 아직 살아 있다고 보는 시점. 스윕 컷오프는 그보다 느슨해야 한다.
	now := event.SecFromTime(baseTime.Add(idle - time.Minute))
	asm.Advance(now)
	mustWrite(t, db, Batch{Sessions: asm.Snapshot()})

	cutoff := now - event.UnixSec(idle/time.Second)
	n, err := db.CloseIdleSessions(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}
	if n != 0 {
		t.Fatalf("조립기가 진행 중이라 보는 세션을 스윕이 %d개 마감했다 — 다음 스냅샷이 도로 연다", n)
	}
}

func TestApplyLifecycleStartsEndsAndReopensWithoutActivity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	start := event.SecFromTime(baseTime)
	if err := db.ApplyLifecycle(ctx, "codex", "thr-1", start, false); err != nil {
		t.Fatal(err)
	}
	var started int64
	var ended, activity any
	if err := db.SQL().QueryRow(`SELECT started_at, ended_at, last_activity_at FROM sessions WHERE session_key='thr-1'`).Scan(&started, &ended, &activity); err != nil {
		t.Fatal(err)
	}
	if started != int64(start) || ended != nil || activity != nil {
		t.Fatalf("start = %d/%v/%v", started, ended, activity)
	}
	end := start + 60
	if err := db.ApplyLifecycle(ctx, "codex", "thr-1", end, true); err != nil {
		t.Fatal(err)
	}
	if got := scanOne(t, db, `SELECT ended_at FROM sessions WHERE session_key='thr-1'`); got != int64(end) {
		t.Fatalf("ended_at=%v", got)
	}
	if err := db.ApplyLifecycle(ctx, "codex", "thr-1", start+120, false); err != nil {
		t.Fatal(err)
	}
	if got := scanOne(t, db, `SELECT ended_at FROM sessions WHERE session_key='thr-1'`); got != nil {
		t.Fatalf("resume ended_at=%v", got)
	}
	if got := scanOne(t, db, `SELECT started_at FROM sessions WHERE session_key='thr-1'`); got != int64(start) {
		t.Fatalf("started_at=%v", got)
	}
}
