package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/session"
)

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
	mustWrite(t, db, Batch{Events: []EventRecord{
		evrec("claude_code.user_prompt", baseTime, 0, inTurn("p0"), promptBody("인증 토큰 검증")),
	}})

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
			batch := Batch{
				Events: []EventRecord{
					evrec("claude_code.user_prompt", at, i, inTurn("p0"), promptBody("인증 토큰 검증")),
				},
				Sessions: []session.Session{newSession("sess-1", at)},
			}
			if _, err := db.Write(ctx, batch); err != nil {
				t.Errorf("Write %d: %v", i, err)
				return
			}
		}
	}()

	// 조회 고루틴 둘. 하나는 세션·턴 조인, 하나는 원문 LIKE 검색이다 (ADR 0009 가 FTS 를
	// 대신해 정한 방식).
	for r := range 2 {
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
						LEFT JOIN turns t ON t.session_id = s.id`).Scan(&n)
				} else {
					err = ro.SQL().QueryRowContext(ctx,
						`SELECT COUNT(*) FROM turns WHERE prompt_text LIKE '%토큰%'`).Scan(&n)
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
	assertNoOrphans(t, db)
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
		for i := range 20 {
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
	assertNoOrphans(t, db)
}
