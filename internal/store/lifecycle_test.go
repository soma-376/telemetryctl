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

// 스냅샷은 ended_at 을 쓰지도 지우지도 않는다 (ADR 0021). 조립기가 무엇을 말하든
// 생명주기는 훅과 유휴 스윕만 바꾼다.
func TestSnapshotDoesNotWriteEndedAt(t *testing.T) {
	db := openTestDB(t)
	closed := newSession("sess-1", baseTime)
	closed.EndedAt = someSec(closed.StartedAt + 600)

	// 픽스처 헬퍼가 훅 경로로 닫아 준다. 그 뒤 진행 중 스냅샷이 와도 마감은 그대로다.
	mustWrite(t, db, Batch{Sessions: []session.Session{closed}})
	want := int64(event.SecFromTime(baseTime)) + 600
	if got := scanOne(t, db, `SELECT ended_at FROM sessions`); got != want {
		t.Fatalf("훅 마감 = %v, want %d", got, want)
	}

	running := newSession("sess-1", baseTime)
	running.EndedAt = event.Opt[event.UnixSec]{}
	mustWrite(t, db, Batch{Sessions: []session.Session{running}})
	if got := scanOne(t, db, `SELECT ended_at FROM sessions`); got != want {
		t.Fatalf("ended_at = %v, want %d — 스냅샷이 마감을 지웠다", got, want)
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

// last_activity_at 은 started_at 의 거울상이다. 늦게 도착한 오래된 배치가 마지막 활동을
// 과거로 되돌리면 유휴 스윕이 살아 있는 세션을 마감한다.
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

// 스냅샷 없이 이벤트만 저장되는 틱에도 활동 시각은 올라가야 한다. 빠지면 조립기가 놓친
// 세션의 활동 시각이 멈춰 유휴 스윕이 오판한다.
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

// 마감된 세션에 낙오 이벤트가 오면 활동 시각만 오르고 마감은 그대로여야 한다.
func TestSessionLateEventMovesActivityNotEnd(t *testing.T) {
	db := openTestDB(t)
	closed := newSession("sess-1", baseTime)
	closed.EndedAt = someSec(closed.StartedAt + 600)
	mustWrite(t, db, Batch{Sessions: []session.Session{closed}})

	straggler := baseTime.Add(9 * time.Minute) // 마감(10분) 이전
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

// 스윕의 존재 이유다. 조립기를 거치지 않은 세션 — 데몬 재시작 전에 돌던 세션 — 도 마감된다.
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
	// 마감 시각은 컷오프가 아니라 마지막 활동이다.
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
			// 부등호가 < 라 경계가 열려 있다.
			name: "활동이 컷오프와 같으면 마감하지 않는다",
			setup: func(t *testing.T, db *DB) {
				mustWrite(t, db, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}})
			},
			cutoff:  event.UnixSec(sec),
			wantN:   0,
			wantEnd: nil,
		},
		{
			// 낙오 이벤트가 활동 시각을 밀어도 마감 시각이 끌려가면 안 된다.
			name: "이미 마감된 세션은 다시 건드리지 않는다",
			setup: func(t *testing.T, db *DB) {
				closed := newSession("sess-1", baseTime)
				closed.EndedAt = someSec(closed.StartedAt + 60)
				mustWrite(t, db, Batch{Sessions: []session.Session{closed}})
				mustWrite(t, db, Batch{Events: []EventRecord{
					evrec("claude_code.api_request", baseTime.Add(30*time.Second), 1, sess("sess-1")),
				}})
			},
			cutoff:  event.UnixSec(sec + 86400),
			wantN:   0,
			wantEnd: sec + 60,
		},
		{
			// 판정할 근거가 없는 행은 손대지 않는다.
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

// 스냅샷은 마감을 되돌리지 못한다. 조립기가 진행 중이라 말해도 ended_at 은 훅과 스윕만
// 쓴다 (ADR 0021) — 이것이 매 틱 왕복을 구조적으로 불가능하게 하는 지점이다.
func TestSnapshotCannotReopenSweptSession(t *testing.T) {
	db := openTestDB(t)
	sec := event.SecFromTime(baseTime)
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.api_request", baseTime, 1, sess("sess-1")),
	}})
	if _, err := db.CloseIdleSessions(context.Background(), sec+600); err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}

	// 조립기가 같은 세션을 진행 중이라 말하는 스냅샷 — 활동 시각은 마감과 같다.
	running := newSession("sess-1", baseTime)
	running.EndedAt = event.Opt[event.UnixSec]{}
	mustWrite(t, db, Batch{Sessions: []session.Session{running}})

	if got := scanOne(t, db, `SELECT ended_at FROM sessions`); got != int64(sec) {
		t.Fatalf("ended_at = %v, want %d — 스냅샷이 마감을 되돌렸다", got, int64(sec))
	}
}

func TestApplyLifecycleStartsEndsAndReopens(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	start := event.SecFromTime(baseTime)
	if err := db.ApplyLifecycle(ctx, "codex", "thr-1", start, false, ""); err != nil {
		t.Fatal(err)
	}
	var started int64
	var ended, activity any
	if err := db.SQL().QueryRow(`SELECT started_at, ended_at, last_activity_at FROM sessions WHERE session_key='thr-1'`).Scan(&started, &ended, &activity); err != nil {
		t.Fatal(err)
	}
	// 시작 훅은 last_activity_at 을 바닥값으로 깐다 (startLifecycleSQL).
	if started != int64(start) || ended != nil || activity != int64(start) {
		t.Fatalf("start = %d/%v/%v", started, ended, activity)
	}
	end := start + 60
	if err := db.ApplyLifecycle(ctx, "codex", "thr-1", end, true, ""); err != nil {
		t.Fatal(err)
	}
	if got := scanOne(t, db, `SELECT ended_at FROM sessions WHERE session_key='thr-1'`); got != int64(end) {
		t.Fatalf("ended_at=%v", got)
	}
	if err := db.ApplyLifecycle(ctx, "codex", "thr-1", start+120, false, ""); err != nil {
		t.Fatal(err)
	}
	if got := scanOne(t, db, `SELECT ended_at FROM sessions WHERE session_key='thr-1'`); got != nil {
		t.Fatalf("resume ended_at=%v", got)
	}
	if got := scanOne(t, db, `SELECT started_at FROM sessions WHERE session_key='thr-1'`); got != int64(start) {
		t.Fatalf("started_at=%v", got)
	}
}

// 훅으로 열리고 OTel 이벤트가 한 건도 없는 세션 — 프롬프트 없이 바로 닫은 경우 — 도
// 스윕이 마감할 수 있어야 한다. last_activity_at 이 NULL 이면 스윕이 건너뛴다.
func TestStartHookSeedsLastActivity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := event.SecFromTime(baseTime)

	if err := db.ApplyLifecycle(ctx, "claude_code", "sess-hook", at, false, ""); err != nil {
		t.Fatalf("ApplyLifecycle: %v", err)
	}
	if got := scanOne(t, db, `SELECT last_activity_at FROM sessions`); got != int64(at) {
		t.Fatalf("last_activity_at = %v, want %d", got, int64(at))
	}

	n, err := db.CloseIdleSessions(ctx, at+600)
	if err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}
	if n != 1 {
		t.Fatalf("마감한 세션 = %d개, want 1 — 훅으로만 열린 세션이 영원히 running 이다", n)
	}
}

// 재개 훅이 활동 시각을 밀지 않으면 ended_at 을 NULL 로 되돌리자마자 다음 스윕이 도로 닫는다.
func TestResumeHookPushesLastActivityForward(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := event.SecFromTime(baseTime)

	mustWrite(t, db, Batch{Sessions: []session.Session{newSession("sess-hook", baseTime)}})
	resumed := at + 3600
	if err := db.ApplyLifecycle(ctx, "claude_code", "sess-hook", resumed, false, ""); err != nil {
		t.Fatalf("ApplyLifecycle: %v", err)
	}

	if got := scanOne(t, db, `SELECT last_activity_at FROM sessions`); got != int64(resumed) {
		t.Fatalf("last_activity_at = %v, want %d", got, int64(resumed))
	}
	n, err := db.CloseIdleSessions(ctx, resumed-600)
	if err != nil {
		t.Fatalf("CloseIdleSessions: %v", err)
	}
	if n != 0 {
		t.Fatalf("방금 재개한 세션을 스윕이 마감했다")
	}
}

// 종료 훅은 활동 시각을 건드리지 않는다. 밀면 마감보다 활동이 뒤인 행이 생긴다.
func TestEndHookLeavesLastActivity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := event.SecFromTime(baseTime)

	mustWrite(t, db, Batch{Sessions: []session.Session{newSession("sess-hook", baseTime)}})
	if err := db.ApplyLifecycle(ctx, "claude_code", "sess-hook", at+3600, true, ""); err != nil {
		t.Fatalf("ApplyLifecycle: %v", err)
	}
	if got := scanOne(t, db, `SELECT last_activity_at FROM sessions`); got != int64(at) {
		t.Fatalf("last_activity_at = %v, want %d", got, int64(at))
	}
}

// 훅 마감은 데몬 재시작을 넘어야 한다. 조립기의 hookEnded 는 메모리에만 있어, 재시작 뒤
// 마감 이전 시각의 낙오 배치가 오면 조립기가 그 세션을 처음 보는 진행 중 세션으로 만들고
// 그 스냅샷이 훅이 기록한 종료 시각을 지웠다.
func TestHookEndSurvivesRestartStraggler(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := event.SecFromTime(baseTime)

	mustWrite(t, db, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}})
	endedAt := at + 600
	if err := db.ApplyLifecycle(ctx, "claude_code", "sess-1", endedAt, true, ""); err != nil {
		t.Fatalf("ApplyLifecycle: %v", err)
	}

	// 재시작한 조립기가 낙오 이벤트로 만든 스냅샷 — 마감을 모르고 진행 중이라 말한다.
	straggler := newSession("sess-1", baseTime)
	straggler.LastEventAt = endedAt - 60
	mustWrite(t, db, Batch{Sessions: []session.Session{straggler}})

	if got := scanOne(t, db, `SELECT ended_at FROM sessions`); got != int64(endedAt) {
		t.Fatalf("ended_at = %v, want %d — 낙오 배치가 훅 마감을 지웠다", got, int64(endedAt))
	}
}

// 마감보다 뒤인 활동은 진짜 재개다. 훅 마감을 고집하면 도는 세션이 영원히 완료로 남는다.
func TestHookEndYieldsToLaterActivity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := event.SecFromTime(baseTime)

	mustWrite(t, db, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}})
	endedAt := at + 600
	if err := db.ApplyLifecycle(ctx, "claude_code", "sess-1", endedAt, true, ""); err != nil {
		t.Fatalf("ApplyLifecycle: %v", err)
	}

	revived := newSession("sess-1", baseTime)
	revived.LastEventAt = endedAt + 60
	revived.EndedAt = event.Opt[event.UnixSec]{}
	mustWrite(t, db, Batch{Sessions: []session.Session{revived}})

	if got := scanOne(t, db, `SELECT ended_at FROM sessions`); got != nil {
		t.Fatalf("ended_at = %v, want nil — 마감 뒤 활동인데 되살아나지 않았다", got)
	}
}

// 시작 훅은 명시적 재개라 언제나 마감을 되돌린다.
func TestStartHookReopensAfterEndHook(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	at := event.SecFromTime(baseTime)

	if err := db.ApplyLifecycle(ctx, "claude_code", "sess-1", at+600, true, ""); err != nil {
		t.Fatalf("ApplyLifecycle(end): %v", err)
	}
	if err := db.ApplyLifecycle(ctx, "claude_code", "sess-1", at+900, false, ""); err != nil {
		t.Fatalf("ApplyLifecycle(start): %v", err)
	}
	if got := scanOne(t, db, `SELECT ended_at FROM sessions`); got != nil {
		t.Fatalf("ended_at = %v, want nil", got)
	}
}
