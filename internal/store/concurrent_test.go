package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
)

// foreign_keys PRAGMA 를 켜지 않으면 CASCADE 가 조용히 동작하지 않는다. 그런데 SQL 은
// 전부 성공하므로 스키마만 보고는 알 수 없다 — FK 위반이 실제로 거부되는지로 확인한다.
func TestForeignKeysEnforced(t *testing.T) {
	db := openTestDB(t)
	_, err := db.SQL().ExecContext(context.Background(),
		`INSERT INTO tool_events (session_id, ts, tool_name) VALUES ('없는세션', 0, 'Edit')`)
	if err == nil {
		t.Fatal("존재하지 않는 세션의 tool_events 가 삽입됐다 — foreign_keys 가 꺼져 있다")
	}
}

// 세션을 지우면 종속 테이블이 따라 지워져야 한다 (계획서 테스트 전략 §5).
func TestSessionDeleteCascades(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Write(ctx, Batch{Sessions: []session.Session{newSession("sess-1", baseTime)}}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := db.SQL().ExecContext(ctx, `
		INSERT INTO turns (session_id, turn_index, started_at, last_event_at)
		VALUES ('sess-1', 1, ?, ?)`, baseTime.Unix(), baseTime.Unix()); err != nil {
		t.Fatalf("turn INSERT: %v", err)
	}
	if _, err := db.SQL().ExecContext(ctx, `
		INSERT INTO session_phases
		  (session_id, phase_index, phase_type, start_turn_index, end_turn_index, started_at, last_event_at, turn_count)
		VALUES ('sess-1', 1, 'implementation', 1, 1, ?, ?, 1)`, baseTime.Unix(), baseTime.Unix()); err != nil {
		t.Fatalf("session_phase INSERT: %v", err)
	}
	for _, table := range []string{"turns", "session_phases", "session_files", "tool_events", "mcp_session_usage"} {
		if n := countRows(t, db, table); n == 0 {
			t.Fatalf("사전 조건 실패: %s 가 비어 있다", table)
		}
	}

	if _, err := db.SQL().ExecContext(ctx, `DELETE FROM sessions WHERE session_id = 'sess-1'`); err != nil {
		t.Fatalf("sessions 삭제: %v", err)
	}
	for _, table := range []string{"turns", "session_phases", "session_files", "tool_events", "mcp_session_usage"} {
		if n := countRows(t, db, table); n != 0 {
			t.Errorf("%s = %d행, want 0 — CASCADE 가 동작하지 않았다", table, n)
		}
	}
}

// 데몬이 쓰는 동안 GUI 가 read-only 로 읽는 것이 전제다 (ADR 0002).
// -race 로 돌려 두 핸들이 서로를 막거나 깨뜨리지 않는지 본다.
func TestConcurrentReadWhileWriting(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DefaultFileName)

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// read-only 연결은 WAL·shm 파일이 이미 있어야 열린다. 첫 쓰기로 만든다.
	if _, err := db.Write(ctx, Batch{Events: []EventRecord{{
		Event:    newEvent("claude_code.user_prompt", baseTime, 0),
		Contents: []event.Content{{Kind: event.ContentPrompt, Body: "인증 토큰 검증"}},
	}}}); err != nil {
		t.Fatalf("첫 Write: %v", err)
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()
	ro.SetReadLimits(4, time.Minute)

	const writes = 60
	done := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 1; i <= writes; i++ {
			at := baseTime.Add(time.Duration(i) * time.Second)
			s := newSession("sess-1", at)
			batch := Batch{
				Events: []EventRecord{{
					Event:    newEvent("claude_code.user_prompt", at, i),
					Contents: []event.Content{{Kind: event.ContentPrompt, Body: "인증 토큰 검증"}},
				}},
				Sessions: []session.Session{s},
			}
			if _, err := db.Write(ctx, batch); err != nil {
				t.Errorf("Write %d: %v", i, err)
				return
			}
		}
	}()

	// 조회 고루틴 둘. 하나는 세션 조인, 하나는 FTS.
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				var n int
				var err error
				if r == 0 {
					err = ro.SQL().QueryRowContext(ctx, `
						SELECT COUNT(*) FROM sessions s
						LEFT JOIN tool_events t ON t.session_id = s.session_id`).Scan(&n)
				} else {
					err = ro.SQL().QueryRowContext(ctx,
						`SELECT COUNT(*) FROM content_fts WHERE content_fts MATCH '토큰'`).Scan(&n)
				}
				if err != nil {
					t.Errorf("read-only 조회 %d: %v", r, err)
					return
				}
			}
		}(r)
	}
	wg.Wait()

	if n := countRows(t, db, "events"); n != writes+1 {
		t.Fatalf("events = %d행, want %d", n, writes+1)
	}
	var got int
	if err := ro.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM content_fts WHERE content_fts MATCH '토큰'`).Scan(&got); err != nil {
		t.Fatalf("최종 FTS 조회: %v", err)
	}
	if got != writes+1 {
		t.Fatalf("FTS 히트 = %d, want %d", got, writes+1)
	}
}

// prune 이 도는 동안 read-only 조회가 죽지 않아야 한다.
func TestConcurrentReadWhilePruning(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DefaultFileName)

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	seedRetention(t, db, baseTime)

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < 20; i++ {
			if _, err := db.Prune(ctx, baseTime); err != nil {
				t.Errorf("Prune %d: %v", i, err)
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			var n int
			if err := ro.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
				t.Errorf("read-only 조회: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}
