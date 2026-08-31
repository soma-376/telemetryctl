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
	r := NewRefresher(collector, store, RefreshOptions{})

	const callers = 8
	errs := make(chan error, callers)
	go func() { errs <- r.Refresh(context.Background()) }()
	<-collector.started
	for range callers - 1 {
		go func() { errs <- r.Refresh(context.Background()) }()
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
		Cooldown: 10 * time.Second,
		Now:      func() time.Time { return now },
	})

	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(9 * time.Second)
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := collector.calls.Load(), int32(len(SupportedVendors())); got != want {
		t.Fatalf("cooldown collector calls = %d, want %d", got, want)
	}

	now = now.Add(time.Second)
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := collector.calls.Load(), int32(2*len(SupportedVendors())); got != want {
		t.Fatalf("expired collector calls = %d, want %d", got, want)
	}
}
