package dashboard

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

// 데몬이 쓰는 동안 GUI 가 읽는 것이 이 패키지의 상시 상태다 (ADR 0002·0004).
// WAL + busy_timeout 이 실제로 그 조합을 견디는지, 그리고 Reader 자체가 여러 고루틴에서
// 동시에 불려도 안전한지를 -race 로 확인한다.
func TestConcurrentReadWhileDaemonWrites(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// 조회가 빈 DB 를 도는 것을 막기 위한 초기 데이터.
	f.write(store.Batch{
		Sessions: []session.Session{newSession("s-seed", testNow.Add(-time.Hour))},
		Events: []store.EventRecord{
			promptRecord("s-seed", "t-seed", testNow.Add(-time.Hour), 0, "인증 토큰 검증"),
		},
	})
	seedID := f.sessionID(vendorClaude, "s-seed")

	const rounds = 40
	var wg sync.WaitGroup
	writeDone := make(chan struct{})

	// 쓰기 고루틴 = 데몬.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(writeDone)
		for i := 1; i <= rounds; i++ {
			at := testNow.Add(-time.Duration(i) * time.Minute)
			key := fmt.Sprintf("s-%02d", i%20)
			batch := store.Batch{
				Sessions: []session.Session{newSession(key, at)},
				Events: []store.EventRecord{
					llmRecord(key, key+"-turn", at, i, llmSpec{Cost: 0.1, Input: 10, Output: 5}),
					toolRecord(key, key+"-turn", fmt.Sprintf("call-%03d", i), at, 1000+i, toolSpec{
						ToolName: "Edit", MCPServer: "github", Success: event.Some(true),
						Target: workspaceA + "/apply.go",
						File:   fileChange(workspaceA+"/apply.go", 1, 0),
					}),
					promptRecord("s-seed", fmt.Sprintf("t-seed-%03d", i), at, 2000+i, "인증 토큰 검증 및 전달"),
				},
			}
			if _, err := f.db.Write(ctx, batch); err != nil {
				t.Errorf("Write %d: %v", i, err)
				return
			}
		}
	}()

	// 조회 고루틴 여러 개 = GUI 의 병렬 화면.
	readers := []func() error{
		func() error { _, err := f.reader.Today(ctx, seoul); return err },
		func() error { _, err := f.reader.Sessions(ctx, SessionQuery{Limit: 20}); return err },
		func() error { _, err := f.reader.Session(ctx, seedID); return err },
		func() error {
			_, err := f.reader.Breakdown(ctx, BreakdownQuery{Dim: DimVendor, TZ: seoul})
			return err
		},
		func() error {
			_, err := f.reader.Breakdown(ctx, BreakdownQuery{TZ: seoul, Bucket: BucketHourOfDay})
			return err
		},
		func() error { _, err := f.reader.Search(ctx, SearchQuery{Text: "인증 토큰"}); return err },
		func() error { _, err := f.reader.Vendors(ctx); return err },
		func() error { _, err := f.reader.MCPUsage(ctx, 14); return err },
		func() error { _, err := f.reader.Status(ctx); return err },
	}
	for _, read := range readers {
		wg.Add(1)
		go func(read func() error) {
			defer wg.Done()
			for {
				select {
				case <-writeDone:
					// 쓰기가 끝난 뒤에도 한 번 더 돌아 최종 상태를 읽는다.
					if err := read(); err != nil {
						t.Errorf("조회 실패: %v", err)
					}
					return
				default:
					if err := read(); err != nil {
						t.Errorf("쓰기 중 조회 실패: %v", err)
						return
					}
				}
			}
		}(read)
	}
	wg.Wait()

	// 마지막으로 실제 데이터가 보이는지 확인한다 — 전부 성공했는데 빈 결과였다면
	// 이 테스트는 아무것도 검증하지 못한 것이다.
	rows, err := f.reader.Sessions(ctx, SessionQuery{Limit: 100})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("세션 = %d건, want 2건 이상", len(rows))
	}
}

// Reopen 이 여러 고루틴에서 동시에 불려도 핸들이 새거나 갈리지 않아야 한다.
func TestConcurrentReopen(t *testing.T) {
	dir := t.TempDir()
	path := store.PathIn(dir)
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close() //nolint:errcheck // 테스트 정리

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close() //nolint:errcheck // 테스트 정리

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Reopen(); err != nil {
				t.Errorf("Reopen: %v", err)
			}
			if _, err := r.Status(context.Background()); err != nil {
				t.Errorf("Status: %v", err)
			}
		}()
	}
	wg.Wait()
}
