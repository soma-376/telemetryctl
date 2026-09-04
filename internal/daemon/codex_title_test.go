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

type titleReadResult struct {
	name string
	err  error
}

type titleReaderStub struct {
	mu      sync.Mutex
	calls   int
	results []titleReadResult
}

func (s *titleReaderStub) ThreadName(context.Context, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.calls
	s.calls++
	if len(s.results) == 0 {
		return "", nil
	}
	if i >= len(s.results) {
		i = len(s.results) - 1
	}
	return s.results[i].name, s.results[i].err
}

func (s *titleReaderStub) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type titleStoreStub struct {
	saved chan string
}

func (s *titleStoreStub) SetCodexTitle(_ context.Context, key, title string) error {
	s.saved <- key + ":" + title
	return nil
}

func testCodexTitlePolicy(interval time.Duration, delays ...time.Duration) codexTitlePolicy {
	return codexTitlePolicy{now: time.Now, refreshInterval: interval, finalRetryDelays: delays}
}

func TestCodexTitleRefresherPollsActiveSessionAfterCooldown(t *testing.T) {
	reader := &titleReaderStub{results: []titleReadResult{{name: "첫 제목"}, {name: "바뀐 제목"}}}
	store := &titleStoreStub{saved: make(chan string, 2)}
	r := newCodexTitleRefresherWithPolicy(context.Background(), reader, store, log.New(io.Discard, "", 0), testCodexTitlePolicy(20*time.Millisecond))
	t.Cleanup(r.Close)

	r.Enqueue("thread-1", false)
	expectSavedTitle(t, store, "thread-1:첫 제목")
	r.Enqueue("thread-1", false)
	time.Sleep(5 * time.Millisecond)
	if got := reader.CallCount(); got != 1 {
		t.Fatalf("쿨다운 중 호출 = %d, want 1", got)
	}

	time.Sleep(20 * time.Millisecond)
	r.Enqueue("thread-1", false)
	expectSavedTitle(t, store, "thread-1:바뀐 제목")
}

func TestCodexTitleRefresherAlwaysChecksAtSessionEnd(t *testing.T) {
	reader := &titleReaderStub{results: []titleReadResult{{name: "첫 제목"}, {name: "종료 제목"}}}
	store := &titleStoreStub{saved: make(chan string, 2)}
	r := newCodexTitleRefresherWithPolicy(context.Background(), reader, store, log.New(io.Discard, "", 0), testCodexTitlePolicy(time.Hour))
	t.Cleanup(r.Close)

	r.Enqueue("thread-1", false)
	expectSavedTitle(t, store, "thread-1:첫 제목")
	r.Enqueue("thread-1", true)
	expectSavedTitle(t, store, "thread-1:종료 제목")

	r.Enqueue("thread-1", true)
	time.Sleep(5 * time.Millisecond)
	if got := reader.CallCount(); got != 2 {
		t.Fatalf("확정 뒤 호출 = %d, want 2", got)
	}
}

func TestCodexTitleRefresherRetriesEmptyFinalReadWithoutAnotherFlush(t *testing.T) {
	reader := &titleReaderStub{results: []titleReadResult{{}, {}, {name: "나중에 생긴 제목"}}}
	store := &titleStoreStub{saved: make(chan string, 1)}
	r := newCodexTitleRefresherWithPolicy(context.Background(), reader, store, log.New(io.Discard, "", 0), testCodexTitlePolicy(time.Hour, 5*time.Millisecond, 5*time.Millisecond))
	t.Cleanup(r.Close)

	r.Enqueue("thread-1", true)
	expectSavedTitle(t, store, "thread-1:나중에 생긴 제목")
	if got := reader.CallCount(); got != 3 {
		t.Fatalf("App Server 호출 = %d, want 3", got)
	}
}

func TestCodexTitleRefresherStopsAfterBoundedFinalRetries(t *testing.T) {
	reader := &titleReaderStub{results: []titleReadResult{{err: errors.New("temporary")}}}
	store := &titleStoreStub{saved: make(chan string, 1)}
	r := newCodexTitleRefresherWithPolicy(context.Background(), reader, store, log.New(io.Discard, "", 0), testCodexTitlePolicy(time.Hour, 5*time.Millisecond, 5*time.Millisecond))
	t.Cleanup(r.Close)

	r.Enqueue("thread-1", true)
	waitForTitleCalls(t, reader, 3)
	time.Sleep(10 * time.Millisecond)
	r.Enqueue("thread-1", true)
	time.Sleep(5 * time.Millisecond)
	if got := reader.CallCount(); got != 3 {
		t.Fatalf("재시도 한도 뒤 호출 = %d, want 3", got)
	}
}

func TestCodexTitleRefresherResumesPollingWhenSessionReopens(t *testing.T) {
	reader := &titleReaderStub{results: []titleReadResult{{name: "종료 제목"}, {name: "재개 제목"}}}
	store := &titleStoreStub{saved: make(chan string, 2)}
	r := newCodexTitleRefresherWithPolicy(context.Background(), reader, store, log.New(io.Discard, "", 0), testCodexTitlePolicy(time.Hour))
	t.Cleanup(r.Close)

	r.Enqueue("thread-1", true)
	expectSavedTitle(t, store, "thread-1:종료 제목")
	r.Enqueue("thread-1", false)
	expectSavedTitle(t, store, "thread-1:재개 제목")
}

func expectSavedTitle(t *testing.T, store *titleStoreStub, want string) {
	t.Helper()
	select {
	case got := <-store.saved:
		if got != want {
			t.Fatalf("저장 = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("제목 저장 시간 초과: %q", want)
	}
}

func waitForTitleCalls(t *testing.T, reader *titleReaderStub, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if reader.CallCount() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("App Server 호출 = %d, want >= %d", reader.CallCount(), want)
}
