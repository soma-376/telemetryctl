package daemon

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/session"
)

func TestTitleRefresherForgetsStateAndCancelsRetry(t *testing.T) {
	for _, stopAfterSuccess := range []bool{false, true} {
		for _, pending := range []bool{false, true} {
			label := "저장 완료"
			if pending {
				label = "재시도 대기"
			}
			if stopAfterSuccess {
				label += "/저장 후 종료"
			}
			t.Run(label, func(t *testing.T) {
				calls := make(chan string, 10)
				r := newTitleRefresher(context.Background(), func(_ context.Context, key string) bool {
					calls <- key
					return !pending || key != "old"
				}, titlePolicy{stopAfterSuccess: stopAfterSuccess, finalRetryDelays: []time.Duration{time.Hour}})
				t.Cleanup(r.Close)
				receive := func(want string) {
					t.Helper()
					select {
					case got := <-calls:
						if got != want {
							t.Fatalf("조회=%s, want %s", got, want)
						}
					case <-time.After(time.Second):
						t.Fatalf("조회 대기: %s", want)
					}
				}
				r.Enqueue("old", true)
				receive("old")
				if !r.TryForget("old") {
					t.Fatal("제거 요청 거부")
				}
				// 제거한 상태를 되살리거나 같은 키의 새 상태에 이전 예약을 적용하면 안 된다.
				stale, cancel := context.WithCancel(context.Background())
				cancel()
				retry := titleRequest{key: "old", ended: true, retry: true, generation: 1, retryCtx: stale}
				r.queue <- retry
				r.Enqueue("old", true)
				receive("old")
				r.queue <- retry
				if !r.TryForget("old") {
					t.Fatal("재생성된 상태 제거 요청 거부")
				}
				r.Enqueue("barrier", false)
				receive("barrier")
				drained := make(chan struct{})
				go func() { r.retries.Wait(); close(drained) }()
				select {
				case <-drained:
				case <-time.After(time.Second):
					t.Fatal("제거 후 예약 고루틴이 남음")
				}
				r.Close()
				if got := len(r.states); got != 1 {
					t.Fatalf("제거된 상태가 남음: %d, want barrier 1개", got)
				}
			})
		}
	}
}

func TestTitleCleanupQueueBackpressure(t *testing.T) {
	r := &titleRefresher{ctx: context.Background(), queue: make(chan titleRequest, 1)}
	r.Enqueue("old", true)
	if r.TryForget("old") {
		t.Fatal("포화 큐가 제거 요청을 수락함")
	}
	<-r.queue
	if !r.TryForget("old") {
		t.Fatal("큐가 비었는데 재시도 실패")
	}
	if !(<-r.queue).forget {
		t.Fatal("제거 명령이 유실됨")
	}
}

func TestTitleCloseCancelsRunningRefresh(t *testing.T) {
	started := make(chan struct{})
	r := newTitleRefresher(context.Background(), func(ctx context.Context, _ string) bool {
		close(started)
		<-ctx.Done()
		return false
	}, titlePolicy{})
	t.Cleanup(r.Close)
	r.Enqueue("old", true)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("조회가 시작되지 않음")
	}
	closed := make(chan struct{})
	go func() { r.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("실행 중 조회가 종료되지 않음")
	}
	r.Enqueue("new", false)
	if len(r.queue) != 0 {
		t.Fatal("종료 후 새 요청이 쌓임")
	}
}

type cleanupRefresherStub struct {
	allow     bool
	forgotten []string
}

func (s *cleanupRefresherStub) Enqueue(string, bool) {}
func (s *cleanupRefresherStub) TryForget(key string) bool {
	if !s.allow {
		return false
	}
	s.forgotten = append(s.forgotten, key)
	return true
}

func TestPipelineTitleCleanupFollowsSessionTTL(t *testing.T) {
	db := openTestStore(t)
	defer db.Close()
	now := time.Unix(fixtureUnix, 0)
	claude, codex := &cleanupRefresherStub{}, &cleanupRefresherStub{}
	p := &pipeline{db: db, asm: session.New(), log: log.New(io.Discard, "", 0), now: func() time.Time { return now },
		writeTimeout: time.Second, sessionTTL: sessionMemoryTTL, claudeTitles: claude, codexTitles: codex}
	for _, e := range walkthroughBatch(t).Result.Events {
		p.asm.Add(session.Input{Event: e})
	}
	p.closeSessions()
	if len(claude.forgotten)+len(codex.forgotten) != 0 {
		t.Fatal("TTL 전에 제목 상태 제거")
	}
	now = now.Add(sessionMemoryTTL + time.Hour)
	p.closeSessions()
	if len(p.asm.Snapshot()) == 0 {
		t.Fatal("제거 요청 실패인데 조립기에서 삭제됨")
	}
	claude.allow, codex.allow = true, true
	p.closeSessions()
	if len(p.asm.Snapshot()) != 0 {
		t.Fatal("다음 틱에 제거를 재시도하지 않음")
	}
	if len(claude.forgotten)+len(codex.forgotten) == 0 {
		t.Fatal("제목 워커 제거 요청 누락")
	}
	if countRows(t, db.SQL(), "SELECT COUNT(*) FROM sessions") == 0 {
		t.Fatal("메모리 정리가 DB 세션까지 삭제함")
	}
	// 벤더별로 같은 세션 키를 올바른 워커에 전달한다.
	if !p.forgetTitle(session.Session{SessionID: "codex-only", Vendor: "codex"}) {
		t.Fatal("Codex 제거 실패")
	}
	if codex.forgotten[len(codex.forgotten)-1] != "codex-only" {
		t.Fatal("다른 벤더 워커에 전달함")
	}
}
