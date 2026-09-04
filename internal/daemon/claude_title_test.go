package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"
)

type claudeTitleStoreStub struct {
	mu    sync.Mutex
	saved chan string
	err   error
}

func (s *claudeTitleStoreStub) SetClaudeTitle(_ context.Context, key, title string) error {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.saved <- key + ":" + title
	return nil
}

func (s *claudeTitleStoreStub) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

// readerStub 은 호출 횟수를 세고 정해진 답을 준다.
type claudeReaderStub struct {
	mu    sync.Mutex
	calls int
	title string
	ok    bool
}

func (r *claudeReaderStub) read(string, string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.title, r.ok
}

func (r *claudeReaderStub) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func newTestClaudeRefresher(t *testing.T, rd *claudeReaderStub, st claudeTitleStore, now func() time.Time) *claudeTitleRefresher {
	t.Helper()
	r := newClaudeTitleRefresher(context.Background(), t.TempDir(), st, log.New(io.Discard, "", 0), now)
	if r == nil {
		t.Fatal("refresher 가 nil 이다")
	}
	r.read = rd.read
	t.Cleanup(r.Close)
	return r
}

func TestClaudeTitleRefresherStoresOnce(t *testing.T) {
	rd := &claudeReaderStub{title: "트레이 한도 조회 동작 확인", ok: true}
	st := &claudeTitleStoreStub{saved: make(chan string, 2)}
	r := newTestClaudeRefresher(t, rd, st, time.Now)

	r.Enqueue("s1", false)
	select {
	case got := <-st.saved:
		if got != "s1:트레이 한도 조회 동작 확인" {
			t.Fatalf("저장 = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("제목 저장 시간 초과")
	}

	// 끝난 세션은 다시 읽지 않는다.
	r.Enqueue("s1", false)
	select {
	case got := <-st.saved:
		t.Fatalf("완료한 세션을 다시 저장함: %q", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// 제목이 아직 없으면 쿨다운 안에는 파일을 다시 열지 않는다.
// 제목은 첫 프롬프트 직후에 생기므로 첫 시도는 대개 빈손이다.
func TestClaudeTitleRefresherRetryCooldown(t *testing.T) {
	rd := &claudeReaderStub{ok: false}
	st := &claudeTitleStoreStub{saved: make(chan string, 2)}

	var mu sync.Mutex
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	r := newTestClaudeRefresher(t, rd, st, clock)

	r.Enqueue("s1", false)
	waitCalls(t, rd, 1)

	r.Enqueue("s1", false)
	r.Enqueue("s1", false)
	time.Sleep(50 * time.Millisecond)
	if got := rd.count(); got != 1 {
		t.Fatalf("쿨다운 안에서 다시 읽었다: 호출 %d회", got)
	}

	mu.Lock()
	now = now.Add(claudeTitleRetry + time.Second)
	mu.Unlock()

	rd.mu.Lock()
	rd.title, rd.ok = "뒤늦게 생긴 제목", true
	rd.mu.Unlock()

	r.Enqueue("s1", false)
	select {
	case got := <-st.saved:
		if got != "s1:뒤늦게 생긴 제목" {
			t.Fatalf("저장 = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("쿨다운이 지나도 재시도하지 않았다")
	}
}

// 저장이 실패하면 완료로 치지 않는다 — 쿨다운이 지나면 다시 시도해야 한다.
func TestClaudeTitleRefresherRetriesOnStoreError(t *testing.T) {
	rd := &claudeReaderStub{title: "제목", ok: true}
	st := &claudeTitleStoreStub{saved: make(chan string, 1), err: errors.New("디스크 오류")}

	var mu sync.Mutex
	now := time.Unix(1_700_000_000, 0)
	r := newTestClaudeRefresher(t, rd, st, func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})

	r.Enqueue("s1", false)
	waitCalls(t, rd, 1)

	st.setErr(nil)
	mu.Lock()
	now = now.Add(claudeTitleRetry + time.Second)
	mu.Unlock()

	r.Enqueue("s1", false)
	select {
	case got := <-st.saved:
		if got != "s1:제목" {
			t.Fatalf("저장 = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("저장 실패 후 재시도하지 않았다")
	}
}

// nil 이어도 호출부가 안전해야 한다 — 홈을 못 찾는 환경에서 nil 이 돌아온다.
func TestClaudeTitleRefresherNilSafe(t *testing.T) {
	var r *claudeTitleRefresher
	r.Enqueue("s1", false)
	r.Close()

	if got := newClaudeTitleRefresher(context.Background(), "", &claudeTitleStoreStub{}, log.New(io.Discard, "", 0), time.Now); got != nil {
		t.Fatal("root 가 비었는데 refresher 를 만들었다")
	}
}

func waitCalls(t *testing.T, rd *claudeReaderStub, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if rd.count() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("읽기 %d회를 기다리다 시간 초과 (실제 %d회)", want, rd.count())
}

// 마감된 세션은 다시 스냅샷에 실리지 않는다 — "다음 기회" 가 없으므로 쿨다운을 건너뛰고
// 마지막으로 한 번 더 본다.
func TestClaudeTitleRefresherFinalAttemptOnEnd(t *testing.T) {
	rd := &claudeReaderStub{ok: false}
	st := &claudeTitleStoreStub{saved: make(chan string, 1)}
	// 시계를 멈춰 두면 쿨다운은 영원히 지나지 않는다. ended 만이 통과시킬 수 있다.
	now := time.Unix(1_700_000_000, 0)
	r := newTestClaudeRefresher(t, rd, st, func() time.Time { return now })

	r.Enqueue("s1", false)
	waitCalls(t, rd, 1)

	rd.mu.Lock()
	rd.title, rd.ok = "마감 직전에 생긴 제목", true
	rd.mu.Unlock()

	r.Enqueue("s1", true)
	select {
	case got := <-st.saved:
		if got != "s1:마감 직전에 생긴 제목" {
			t.Fatalf("저장 = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("마감 시 쿨다운을 건너뛰지 않았다")
	}
}
