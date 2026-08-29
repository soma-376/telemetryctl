package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/session"
)

// v3 의 외래 키는 전부 NO ACTION 이다 (ADR 0009). CASCADE 가 없으므로 부모를 먼저 넣지
// 않으면 그 자리에서 실패하고, 부모를 먼저 지우면 고아가 남는다. 두 방향을 다 확인한다.

// foreign_keys PRAGMA 를 켜지 않으면 위반이 조용히 통과한다. 그런데 SQL 은 전부 성공하므로
// 스키마만 보고는 알 수 없다 — 위반이 실제로 거부되는지로 확인한다.
func TestForeignKeysEnforced(t *testing.T) {
	db := openTestDB(t)
	_, err := db.SQL().ExecContext(context.Background(),
		`INSERT INTO sessions (vendor_id, session_key) VALUES ('없는벤더', 'sess-x')`)
	if err == nil {
		t.Fatal("존재하지 않는 벤더의 세션이 삽입됐다 — foreign_keys 가 꺼져 있다")
	}
}

// Write 한 번으로 일곱 테이블이 다 채워지는 픽스처를 넣고 고아가 없는지 본다.
func TestForeignKeyCheckIsEmptyAfterWrite(t *testing.T) {
	db := openTestDB(t)
	const path = "/Users/jy/dev/projects/soma-376/telemetryctl/internal/store/write.go"

	mustWrite(t, db, Batch{
		Sessions: []session.Session{newSession("sess-1", baseTime)},
		Events: []EventRecord{
			// 가상 턴으로 가는 세션 수준 이벤트.
			evrec("claude_code.session.count", baseTime, 0),
			// 실제 턴.
			evrec("claude_code.user_prompt", baseTime, 1, inTurn("p1"), promptBody("고쳐 줘")),
			evrec("claude_code.api_request", baseTime, 2, inTurn("p1"), cost(0.5), tokens(100, 20)),
			evrec("claude_code.tool_decision", baseTime, 3,
				inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"), decision("accept", "config")),
			evrec("claude_code.tool_result", baseTime.Add(time.Second), 0,
				inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"),
				succeeded(true), targetPath(path), fileChange(session.OperationModify, path)),
		},
	})

	// 전제 확인: 일곱 테이블이 다 찼다. 비어 있으면 아래 단언이 공허하게 통과한다.
	for _, table := range []string{
		"vendors", "sessions", "turns", "events", "llm_calls", "tool_calls", "file_changes",
	} {
		if n := countRows(t, db, table); n == 0 {
			t.Fatalf("%s 가 비었다 — 픽스처가 계층을 못 채웠다", table)
		}
	}
	assertNoOrphans(t, db)
}

// 여러 배치·여러 세션·중복 재전송이 섞여도 마찬가지다.
func TestForeignKeyCheckIsEmptyAfterRepeatedWrites(t *testing.T) {
	db := openTestDB(t)

	for i := range 5 {
		at := baseTime.Add(time.Duration(i) * time.Second)
		mustWrite(t, db, Batch{
			Sessions: []session.Session{newSession("sess-1", at), newSession("sess-2", at)},
			Events: []EventRecord{
				evrec("claude_code.user_prompt", at, i, inTurn("p1")),
				evrec("claude_code.session.count", at, i, sess("s-other"), vendor("codex")),
				evrec("claude_code.tool_result", at, i,
					inTurn("p1"), call("claude_code:toolu_1"), toolName("Edit"), succeeded(true)),
			},
		})
	}
	assertNoOrphans(t, db)
}

// 보존 정책이 돈 뒤에도 고아가 없어야 한다 (ADR 0009 인수조건).
func TestForeignKeyCheckIsEmptyAfterPrune(t *testing.T) {
	db := openTestDB(t)
	const path = "/Users/jy/dev/x.go"

	old := baseTime.Add(-500 * 24 * time.Hour)
	s := newSession("sess-old", old)
	s.EndedAt = someSec(s.StartedAt + 60)

	mustWrite(t, db, Batch{
		Sessions: []session.Session{s, newSession("sess-new", baseTime)},
		Events: []EventRecord{
			evrec("claude_code.user_prompt", old, 0, sess("sess-old"), inTurn("p-old")),
			evrec("claude_code.api_request", old, 1, sess("sess-old"), inTurn("p-old"), cost(1)),
			evrec("claude_code.tool_result", old, 2,
				sess("sess-old"), inTurn("p-old"), call("claude_code:old-1"), toolName("Edit"),
				succeeded(true), targetPath(path), fileChange(session.OperationModify, path)),
			evrec("claude_code.user_prompt", baseTime, 0, sess("sess-new"), inTurn("p-new")),
		},
	})

	res, err := db.Prune(context.Background(), baseTime)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if res.Sessions != 1 || res.Events != 3 {
		t.Fatalf("PruneResult = %+v — 오래된 계층이 안 지워졌다", res)
	}
	if res.FileChanges != 1 || res.ToolCalls != 1 || res.LLMCalls != 1 {
		t.Fatalf("승격 계층이 안 지워졌다: %+v", res)
	}
	assertNoOrphans(t, db)

	// 최근 세션은 살아 있어야 한다.
	if n := countRows(t, db, "sessions"); n != 1 {
		t.Fatalf("sessions = %d행, want 1", n)
	}
}

// 지울 세션이 IN 절 상한(idChunk)을 넘으면 삭제가 여러 문장으로 쪼개진다.
// 조각 경계에서 자식 계층 하나만 빠져도 고아가 남는다.
func TestForeignKeyCheckIsEmptyAfterChunkedPrune(t *testing.T) {
	db := openTestDB(t)
	old := baseTime.Add(-500 * 24 * time.Hour)

	const stale = idChunk + 50
	batch := Batch{}
	for i := range stale {
		id := fmt.Sprintf("sess-old-%d", i)
		s := newSession(id, old)
		s.EndedAt = someSec(s.StartedAt + 60)
		batch.Sessions = append(batch.Sessions, s)
		batch.Events = append(batch.Events,
			evrec("claude_code.user_prompt", old, i, sess(id), inTurn("p")),
			evrec("claude_code.tool_result", old, i, sess(id), inTurn("p"),
				call(fmt.Sprintf("claude_code:c-%d", i)), toolName("Edit"), succeeded(true),
				fileChange(session.OperationModify, "/Users/jy/dev/x.go")))
	}
	batch.Sessions = append(batch.Sessions, newSession("sess-new", baseTime))
	batch.Events = append(batch.Events,
		evrec("claude_code.user_prompt", baseTime, 0, sess("sess-new"), inTurn("p-new")))
	mustWrite(t, db, batch)

	res, err := db.Prune(context.Background(), baseTime)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if res.Sessions != stale {
		t.Fatalf("지운 세션 = %d, want %d", res.Sessions, stale)
	}
	if res.Turns != stale || res.Events != 2*stale || res.ToolCalls != stale || res.FileChanges != stale {
		t.Fatalf("자식 계층이 조각 경계에서 남았다: %+v", res)
	}
	assertNoOrphans(t, db)

	if n := countRows(t, db, "sessions"); n != 1 {
		t.Fatalf("sessions = %d행, want 1", n)
	}
}
