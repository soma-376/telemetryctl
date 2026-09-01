package vendorlimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type refreshCollector struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *refreshCollector) CollectVendor(_ context.Context, vendor Vendor) Result {
	c.calls.Add(1)
	if c.started != nil {
		c.once.Do(func() {
			close(c.started)
			<-c.release
		})
	}
	return Result{Vendor: vendor, State: StateAvailable}
}

type refreshStore struct{ calls atomic.Int32 }

func (s *refreshStore) UpsertVendorLimit(context.Context, Result, time.Time) error {
	s.calls.Add(1)
	return nil
}

func TestRefresherConcurrentCallsShareOneRefresh(t *testing.T) {
	collector := &refreshCollector{started: make(chan struct{}), release: make(chan struct{})}
	store := &refreshStore{}
	// 쿨다운을 명시한다. 진행 중인 갱신이 끝난 직후에 도착한 호출은 싱글플라이트가 아니라
	// 쿨다운이 막으므로, 0 이면 그 호출들이 각자 조회를 시작해 이 테스트가 성립하지 않는다.
	r := NewRefresher(collector, store, RefreshOptions{AutoCooldown: time.Minute})

	const callers = 8
	errs := make(chan error, callers)
	go func() { errs <- r.RefreshAuto(context.Background()) }()
	<-collector.started
	for range callers - 1 {
		go func() { errs <- r.RefreshAuto(context.Background()) }()
	}
	close(collector.release)

	for range callers {
		if err := <-errs; err != nil {
			t.Fatalf("Refresh() error = %v", err)
		}
	}
	if got, want := collector.calls.Load(), int32(len(SupportedVendors())); got != want {
		t.Fatalf("collector calls = %d, want %d", got, want)
	}
	if got, want := store.calls.Load(), int32(len(SupportedVendors())); got != want {
		t.Fatalf("store calls = %d, want %d", got, want)
	}
}

func TestRefresherThrottlesUntilCooldownExpires(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	collector := &refreshCollector{}
	r := NewRefresher(collector, &refreshStore{}, RefreshOptions{
		AutoCooldown: 10 * time.Second,
		Now:          func() time.Time { return now },
	})

	if err := r.RefreshAuto(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(9 * time.Second)
	if err := r.RefreshAuto(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := collector.calls.Load(), int32(len(SupportedVendors())); got != want {
		t.Fatalf("cooldown collector calls = %d, want %d", got, want)
	}

	now = now.Add(time.Second)
	if err := r.RefreshAuto(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := collector.calls.Load(), int32(2*len(SupportedVendors())); got != want {
		t.Fatalf("expired collector calls = %d, want %d", got, want)
	}
}

// 새로고침 버튼은 자동 쿨다운에 걸리지 않는다. 걸리면 눌러도 아무 일이 없어 고장으로 읽힌다
// (ADR 0014). 반대로 수동 쿨다운 안의 연타는 여전히 막혀야 한다.
func TestRefresherManualIgnoresAutoCooldown(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	collector := &refreshCollector{}
	r := NewRefresher(collector, &refreshStore{}, RefreshOptions{
		AutoCooldown:   10 * time.Minute,
		ManualCooldown: 10 * time.Second,
		Now:            func() time.Time { return now },
	})
	vendors := int32(len(SupportedVendors()))

	if err := r.RefreshAuto(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 자동 쿨다운 한참 안이지만 사용자가 눌렀다 — 조회가 나가야 한다.
	now = now.Add(30 * time.Second)
	if err := r.RefreshManual(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := collector.calls.Load(), 2*vendors; got != want {
		t.Fatalf("수동 갱신이 자동 쿨다운에 막혔다: calls = %d, want %d", got, want)
	}

	// 연타는 수동 쿨다운이 막는다.
	now = now.Add(3 * time.Second)
	if err := r.RefreshManual(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := collector.calls.Load(), 2*vendors; got != want {
		t.Fatalf("연타가 그대로 나갔다: calls = %d, want %d", got, want)
	}

	// 창 열기는 여전히 자동 쿨다운 안이라 나가지 않는다.
	if err := r.RefreshAuto(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := collector.calls.Load(), 2*vendors; got != want {
		t.Fatalf("자동 갱신이 쿨다운을 무시했다: calls = %d, want %d", got, want)
	}
}

// 창을 열면 자동 갱신이 나가고, 사용자는 그 직후에 새로고침을 누른다 — 가장 흔한 순서다.
// 쿨다운 시각이 등급 공용이면 이 누름이 조용히 막혀 버튼이 고장 난 것처럼 보인다.
func TestRefresherManualNotBlockedByRecentAuto(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	collector := &refreshCollector{}
	r := NewRefresher(collector, &refreshStore{}, RefreshOptions{
		AutoCooldown:   10 * time.Minute,
		ManualCooldown: 10 * time.Second,
		Now:            func() time.Time { return now },
	})
	vendors := int32(len(SupportedVendors()))

	// 창이 열렸다.
	if err := r.RefreshAuto(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 2초 뒤 버튼을 눌렀다. 수동 쿨다운(10초)은 자기 성공이 없으니 막지 않아야 한다.
	now = now.Add(2 * time.Second)
	if err := r.RefreshManual(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := collector.calls.Load(), 2*vendors; got != want {
		t.Fatalf("창을 연 직후의 새로고침이 막혔다: calls = %d, want %d", got, want)
	}

	// 방금 수동으로 조회했으니 뒤따르는 자동 갱신은 쉰다.
	now = now.Add(time.Second)
	if err := r.RefreshAuto(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := collector.calls.Load(), 2*vendors; got != want {
		t.Fatalf("수동 조회 직후 자동 갱신이 또 나갔다: calls = %d, want %d", got, want)
	}
}
