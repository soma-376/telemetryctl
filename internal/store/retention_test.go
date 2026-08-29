package store

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/session"
)

// 보존 정책의 세부(경계·계수·purge 옵션)는 PROJ-86 의 몫이다. 여기서는 v3 계층에 대해
// 삭제 순서가 성립하고 최근 데이터가 살아남는다는 것만 고정한다.

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
