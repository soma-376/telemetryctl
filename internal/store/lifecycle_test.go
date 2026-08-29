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
