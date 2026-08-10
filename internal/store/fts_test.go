package store

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
)

func seedContent(t *testing.T, db *DB, bodies ...string) {
	t.Helper()
	var recs []EventRecord
	for i, body := range bodies {
		recs = append(recs, EventRecord{
			Event:    newEvent("claude_code.user_prompt", baseTime.Add(time.Duration(i)*time.Second), i),
			Contents: []event.Content{{Kind: event.ContentPrompt, Body: body}},
		})
	}
	if _, err := db.Write(context.Background(), Batch{Events: recs}); err != nil {
		t.Fatalf("시드 Write: %v", err)
	}
}

func ftsHits(t *testing.T, db *DB, query string) int {
	t.Helper()
	var n int
	err := db.SQL().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM content_fts WHERE content_fts MATCH ?`, query).Scan(&n)
	if err != nil {
		t.Fatalf("FTS 질의 (%q): %v", query, err)
	}
	return n
}

// external content FTS5 는 원본 테이블을 자동으로 따라가지 않는다. 트리거를 빠뜨리면
// 색인이 비어 있고 검색이 조용히 "결과 없음" 이 된다.
func TestContentFTSIndexedOnInsert(t *testing.T) {
	db := openTestDB(t)
	seedContent(t, db, "authentication token validation", "unrelated body")

	if got := ftsHits(t, db, "token"); got != 1 {
		t.Fatalf("MATCH 'token' = %d건, want 1 — FTS 색인이 동기화되지 않았다", got)
	}
	if got := ftsHits(t, db, "body"); got != 1 {
		t.Fatalf("MATCH 'body' = %d건, want 1", got)
	}
}

// 계획서 「추가 의존성」이 modernc.org/sqlite 에서 한글 MATCH 동작을 확인했다고 명시했다.
// 화면의 검색이 한국어 프롬프트를 찾지 못하면 검색 기능 자체가 무의미하다.
func TestContentFTSKoreanMatch(t *testing.T) {
	db := openTestDB(t)
	seedContent(t,
		db,
		"인증 토큰 검증 및 Collector 전달 프록시 구현",
		"세션 조립기의 유휴 임계값을 조정",
		"unrelated english body",
	)

	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"단일 한글 토큰", "토큰", 1},
		{"다른 문서의 한글 토큰", "세션", 1},
		{"AND 질의", "인증 검증", 1},
		{"OR 질의", "토큰 OR 세션", 2},
		{"접두 질의", "프록시*", 1},
		{"구절 질의", `"인증 토큰"`, 1},
		{"없는 낱말", "존재하지않는낱말", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ftsHits(t, db, tc.query); got != tc.want {
				t.Fatalf("MATCH %q = %d건, want %d", tc.query, got, tc.want)
			}
		})
	}

	// 색인에서 본문 행으로 되돌아갈 수 있어야 한다 (검색 화면이 세션으로 이동하는 경로).
	var sessionID string
	err := db.SQL().QueryRowContext(context.Background(), `
		SELECT e.session_id
		FROM content_fts f
		JOIN event_content c ON c.id = f.rowid
		JOIN events e ON e.id = c.event_id
		WHERE content_fts MATCH '토큰'`).Scan(&sessionID)
	if err != nil {
		t.Fatalf("FTS 조인: %v", err)
	}
	if sessionID != "sess-1" {
		t.Fatalf("session_id = %q, want sess-1", sessionID)
	}
}

// events 를 지우면 CASCADE 로 event_content 가, 트리거로 content_fts 가 따라 정리돼야 한다.
// 색인에 유령이 남으면 본문 없는 검색 히트가 나오고 원문 삭제 약속도 깨진다.
func TestContentFTSCleanedOnCascade(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedContent(t, db, "인증 토큰 검증", "다른 본문")

	if got := ftsHits(t, db, "토큰"); got != 1 {
		t.Fatalf("사전 조건 실패: MATCH = %d건", got)
	}
	if _, err := db.SQL().ExecContext(ctx, `DELETE FROM events WHERE session_id = 'sess-1'`); err != nil {
		t.Fatalf("events 삭제: %v", err)
	}
	if n := countRows(t, db, "event_content"); n != 0 {
		t.Fatalf("event_content = %d행, want 0 — CASCADE 가 동작하지 않았다", n)
	}
	if got := ftsHits(t, db, "토큰"); got != 0 {
		t.Fatalf("MATCH = %d건, want 0 — FTS 색인에 유령이 남았다", got)
	}
	var total int
	if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM content_fts`).Scan(&total); err != nil {
		t.Fatalf("content_fts 계수: %v", err)
	}
	if total != 0 {
		t.Fatalf("content_fts = %d행, want 0", total)
	}
}

// 보존 정책과 purge 도 색인을 남기지 않아야 한다.
func TestContentFTSCleanedOnPruneAndPurge(t *testing.T) {
	t.Run("prune", func(t *testing.T) {
		db := openTestDB(t)
		old := EventRecord{
			Event:    newEvent("claude_code.user_prompt", baseTime.Add(-31*day), 0),
			Contents: []event.Content{{Kind: event.ContentPrompt, Body: "오래된 인증 토큰"}},
		}
		if _, err := db.Write(context.Background(), Batch{Events: []EventRecord{old}}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if _, err := db.Prune(context.Background(), baseTime); err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if got := ftsHits(t, db, "토큰"); got != 0 {
			t.Fatalf("prune 후 MATCH = %d건, want 0", got)
		}
	})

	t.Run("purge", func(t *testing.T) {
		db := openTestDB(t)
		seedContent(t, db, "인증 토큰 검증")
		if _, err := db.PurgeContent(context.Background(), time.Time{}); err != nil {
			t.Fatalf("PurgeContent: %v", err)
		}
		if got := ftsHits(t, db, "토큰"); got != 0 {
			t.Fatalf("purge 후 MATCH = %d건, want 0", got)
		}
	})
}

// FTS5 자체 무결성 검사. 색인과 원본이 어긋나면 여기서 잡힌다.
func TestContentFTSIntegrity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedContent(t, db, "인증 토큰 검증", "세션 조립", "third body")

	if _, err := db.SQL().ExecContext(ctx, `DELETE FROM events WHERE session_id = 'sess-1'`); err != nil {
		t.Fatalf("events 삭제: %v", err)
	}
	seedContent(t, db, "다시 넣은 본문")

	if _, err := db.SQL().ExecContext(ctx,
		`INSERT INTO content_fts(content_fts) VALUES ('integrity-check')`); err != nil {
		t.Fatalf("FTS 무결성 검사 실패: %v", err)
	}
}
