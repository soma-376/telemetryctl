package store

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
)

// 보존 정책은 되돌릴 수 없는 삭제다. 그래서 "무엇이 지워지는가" 만큼 "무엇이 남는가" 와
// "실패하면 아무것도 바뀌지 않는가" 를 같은 무게로 고정한다.

// cutoffSec 은 baseTime 기준 400일 컷오프다. 이 값과 정확히 같은 시각의 행은 남는다.
var cutoffSec = event.SecFromTime(baseTime.Add(-DefaultRetentionDays * 24 * time.Hour))

// closedSession 은 주어진 시각에 시작하고 마감된 세션이다. 경계 판정에 필요한 시각만
// 다르고 나머지 컬럼은 newSession 에서 빌린다.
func closedSession(id string, at event.UnixSec) session.Session {
	s := newSession(id, baseTime)
	s.StartedAt, s.LastEventAt = at, at
	s.EndedAt = someSec(at)
	return s
}

// seedRetention 은 컷오프 바깥(오래된) 세션과 안쪽(최근) 세션을 하나씩 넣는다.
func seedRetention(t *testing.T, db *DB, now time.Time) {
	t.Helper()
	old := now.Add(-(DefaultRetentionDays + 10) * 24 * time.Hour)
	const path = "/Users/jy/dev/projects/soma-376/telemetryctl/internal/store/write.go"

	oldSession := newSession("sess-old", old)
	oldSession.EndedAt = someSec(oldSession.StartedAt + 60)

	mustWrite(t, db, Batch{
		Sessions: []session.Session{oldSession, newSession("sess-new", now)},
		Events: []EventRecord{
			evrec("claude_code.user_prompt", old, 0,
				sess("sess-old"), inTurn("p-old"), promptBody("오래된 프롬프트")),
			evrec("claude_code.api_request", old, 1, sess("sess-old"), inTurn("p-old"), cost(1)),
			evrec("claude_code.tool_result", old, 2,
				sess("sess-old"), inTurn("p-old"), call("claude_code:old-1"), toolName("Edit"),
				succeeded(true), targetPath(path), fileChange(session.OperationModify, path)),

			evrec("claude_code.user_prompt", now, 0,
				sess("sess-new"), inTurn("p-new"), promptBody("최근 프롬프트")),
		},
	})
}

func TestPruneRemovesOldLayersInOrder(t *testing.T) {
	db := openTestDB(t)
	seedRetention(t, db, baseTime)

	res, err := db.Prune(context.Background(), baseTime)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if res.Total() == 0 {
		t.Fatal("아무것도 지우지 않았다")
	}
	if res.Sessions != 1 || res.Turns != 1 || res.Events != 3 {
		t.Fatalf("PruneResult = %+v", res)
	}
	if res.FileChanges != 1 || res.ToolCalls != 1 || res.LLMCalls != 1 {
		t.Fatalf("승격 계층이 안 지워졌다: %+v", res)
	}
	assertNoOrphans(t, db)

	// 최근 세션은 그대로다.
	if n := countWhere(t, db, "sessions", "session_key = 'sess-new'"); n != 1 {
		t.Fatal("최근 세션이 지워졌다")
	}
	if n := countRows(t, db, "events"); n != 1 {
		t.Fatalf("events = %d행, want 1", n)
	}
}

// 같은 컷오프로 두 번 돌려도 안전하다. 실패 시 다음 틱에 그대로 다시 부르는 것이 정상 경로다.
func TestPruneIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	seedRetention(t, db, baseTime)

	if _, err := db.Prune(context.Background(), baseTime); err != nil {
		t.Fatalf("첫 Prune: %v", err)
	}
	second, err := db.Prune(context.Background(), baseTime)
	if err != nil {
		t.Fatalf("두 번째 Prune: %v", err)
	}
	if second.Total() != 0 {
		t.Fatalf("두 번째 Prune 이 %d행을 더 지웠다: %+v", second.Total(), second)
	}
}

// 세션이 아직 남아 있는 벤더는 지우지 않는다. sessions.vendor_id 가 NO ACTION 이라
// 지우는 순간 외래 키 위반이다.
func TestPruneKeepsVendorWithLiveSessions(t *testing.T) {
	db := openTestDB(t)
	seedRetention(t, db, baseTime)

	if _, err := db.Prune(context.Background(), baseTime); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n := countRows(t, db, "vendors"); n != 1 {
		t.Fatalf("vendors = %d행, want 1 — 살아 있는 세션의 벤더가 지워졌다", n)
	}
	assertNoOrphans(t, db)
}

// 시각이 아예 없는 세션은 판정할 근거가 없으므로 대상이 아니다.
func TestPruneKeepsSessionsWithoutTimestamps(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.SQL().ExecContext(context.Background(),
		`INSERT INTO vendors (vendor, first_seen, last_seen, status) VALUES ('codex', 1, 2, 'enabled')`); err != nil {
		t.Fatalf("vendor 삽입: %v", err)
	}
	if _, err := db.SQL().ExecContext(context.Background(),
		`INSERT INTO sessions (vendor_id, session_key) VALUES ('codex', 'sess-x')`); err != nil {
		t.Fatalf("session 삽입: %v", err)
	}

	if _, err := db.Prune(context.Background(), baseTime); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n := countRows(t, db, "sessions"); n != 1 {
		t.Fatal("시각 없는 세션이 지워졌다 — 근거 없이 지우면 되살릴 방법이 없다")
	}
}

// v3 에서 원문이 남는 자리는 turns.prompt_text 하나다.
func TestPurgeContentClearsPromptText(t *testing.T) {
	db := openTestDB(t)
	seedRetention(t, db, baseTime)

	if n := countWhere(t, db, "turns", "prompt_text IS NOT NULL"); n != 2 {
		t.Fatalf("사전 조건 실패: 프롬프트가 있는 턴 = %d행", n)
	}

	n, err := db.PurgeContent(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("PurgeContent: %v", err)
	}
	if n != 2 {
		t.Fatalf("지운 행 = %d, want 2", n)
	}
	if got := countWhere(t, db, "turns", "prompt_text IS NOT NULL"); got != 0 {
		t.Fatalf("원문이 %d행 남았다", got)
	}
	// 턴·이벤트 행과 수치는 그대로다 — 집계가 변하면 안 된다.
	if countRows(t, db, "turns") != 2 || countRows(t, db, "events") != 4 {
		t.Fatal("purge 가 원문 말고 다른 것을 지웠다")
	}
}

// --before 는 그 시각 이전에 시작한 턴만 지운다.
func TestPurgeContentBefore(t *testing.T) {
	db := openTestDB(t)
	seedRetention(t, db, baseTime)

	n, err := db.PurgeContent(context.Background(), baseTime.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("PurgeContent: %v", err)
	}
	if n != 1 {
		t.Fatalf("지운 행 = %d, want 1", n)
	}
	if got := scanOne(t, db, `SELECT prompt_text FROM turns WHERE turn_key = 'p-new'`); got != "최근 프롬프트" {
		t.Fatalf("최근 프롬프트가 지워졌다: %v", got)
	}
}

// 컷오프 경계는 열려 있다 — 컷오프와 정확히 같은 시각의 행은 남는다.
// 경계가 한쪽으로 밀리면 매일 하루치가 조용히 더(또는 덜) 지워진다.
func TestPruneCutoffBoundary(t *testing.T) {
	tests := []struct {
		name        string
		endedAt     event.UnixSec
		wantDeleted bool
	}{
		{name: "컷오프 1초 전은 지운다", endedAt: cutoffSec - 1, wantDeleted: true},
		{name: "컷오프 정각은 남긴다", endedAt: cutoffSec},
		{name: "컷오프 1초 후는 남긴다", endedAt: cutoffSec + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openTestDB(t)
			mustWrite(t, db, Batch{Sessions: []session.Session{closedSession("sess-1", tt.endedAt)}})

			res, err := db.Prune(context.Background(), baseTime)
			if err != nil {
				t.Fatalf("Prune: %v", err)
			}
			want := int64(0)
			if tt.wantDeleted {
				want = 1
			}
			if res.Sessions != want {
				t.Fatalf("지운 세션 = %d, want %d", res.Sessions, want)
			}
			if got := int64(countRows(t, db, "sessions")); got != 1-want {
				t.Fatalf("남은 세션 = %d행", got)
			}
			assertNoOrphans(t, db)
		})
	}
}

// 400일 전에 시작해 지금도 도는 세션은 살아 있다. 시작 시각만 보면 어제 만들어진
// 이벤트까지 함께 사라진다.
func TestPruneKeepsLongRunningSession(t *testing.T) {
	db := openTestDB(t)
	s := newSession("sess-long", baseTime)
	s.StartedAt = cutoffSec - 100*24*3600 // 500일 전 시작, 아직 마감 안 됨
	s.LastEventAt = event.SecFromTime(baseTime)

	mustWrite(t, db, Batch{
		Sessions: []session.Session{s},
		Events: []EventRecord{
			evrec("claude_code.user_prompt", baseTime, 0, sess("sess-long"), inTurn("p1")),
		},
	})

	res, err := db.Prune(context.Background(), baseTime)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if res.Total() != 0 {
		t.Fatalf("도는 세션을 지웠다: %+v", res)
	}
}

// 세션 행에 시각이 없어도 이벤트가 있으면 판정할 수 있다.
func TestPruneUsesEventTimeWhenSessionHasNoTimestamps(t *testing.T) {
	tests := []struct {
		name        string
		occurredAt  event.UnixSec
		wantDeleted bool
	}{
		{name: "오래된 이벤트만 있으면 지운다", occurredAt: cutoffSec - 1, wantDeleted: true},
		{name: "최근 이벤트가 있으면 남긴다", occurredAt: event.SecFromTime(baseTime)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openTestDB(t)
			seedTimelessSession(t, db, tt.occurredAt)

			res, err := db.Prune(context.Background(), baseTime)
			if err != nil {
				t.Fatalf("Prune: %v", err)
			}
			want := int64(0)
			if tt.wantDeleted {
				want = 1
			}
			if res.Sessions != want || res.Events != want || res.Turns != want {
				t.Fatalf("PruneResult = %+v, 지울 것 = %d", res, want)
			}
			assertNoOrphans(t, db)
		})
	}
}

// 한 트랜잭션이라는 것은 중간에 실패하면 **아무것도** 바뀌지 않는다는 뜻이다.
// 자식 계층을 다 지운 뒤 sessions 에서 실패시켜 그 앞의 삭제가 되돌려지는지 본다.
func TestPruneRollsBackOnFailure(t *testing.T) {
	db := openTestDB(t)
	seedRetention(t, db, baseTime)
	before := snapshotRows(t, db)

	// BEFORE DELETE 트리거로 sessions 삭제만 실패시킨다. 앞의 다섯 문장은 이미 성공한 뒤다.
	if _, err := db.SQL().ExecContext(context.Background(),
		`CREATE TRIGGER prune_boom BEFORE DELETE ON sessions BEGIN
		   SELECT RAISE(ABORT, '테스트가 강제한 실패');
		 END`); err != nil {
		t.Fatalf("트리거 생성: %v", err)
	}

	if _, err := db.Prune(context.Background(), baseTime); err == nil {
		t.Fatal("Prune 이 성공했다 — 트리거가 안 걸렸다")
	}

	if _, err := db.SQL().ExecContext(context.Background(), `DROP TRIGGER prune_boom`); err != nil {
		t.Fatalf("트리거 삭제: %v", err)
	}
	for table, want := range before {
		if got := snapshotRows(t, db)[table]; got != want {
			t.Fatalf("%s 가 롤백되지 않았다:\n실패 후:\n%s\n실패 전:\n%s", table, got, want)
		}
	}

	// 트리거가 사라졌으니 같은 호출이 이제 정상적으로 지운다.
	res, err := db.Prune(context.Background(), baseTime)
	if err != nil {
		t.Fatalf("재시도 Prune: %v", err)
	}
	if res.Sessions != 1 {
		t.Fatalf("재시도 PruneResult = %+v", res)
	}
	assertNoOrphans(t, db)
}

// 두 번째 실행은 아무것도 바꾸지 않는다. 실패 후 다음 틱에 그대로 다시 부르는 것이 정상 경로다.
func TestPruneSecondRunChangesNothing(t *testing.T) {
	db := openTestDB(t)
	seedRetention(t, db, baseTime)

	if _, err := db.Prune(context.Background(), baseTime); err != nil {
		t.Fatalf("첫 Prune: %v", err)
	}
	after := snapshotRows(t, db)

	second, err := db.Prune(context.Background(), baseTime)
	if err != nil {
		t.Fatalf("두 번째 Prune: %v", err)
	}
	if second.Total() != 0 {
		t.Fatalf("두 번째 Prune 이 %d행을 더 지웠다: %+v", second.Total(), second)
	}
	for table, want := range after {
		if got := snapshotRows(t, db)[table]; got != want {
			t.Fatalf("%s 가 두 번째 실행에서 바뀌었다:\n%s\n원래:\n%s", table, got, want)
		}
	}
}
