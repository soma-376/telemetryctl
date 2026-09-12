package vendorlimit

import (
	"context"
	"errors"
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

// Done이 평가되면 합류 요청이 진행 중 작업을 발견한 상태다.
type waitingContext struct {
	context.Context
	joined chan struct{}
	once   sync.Once
}

func (c *waitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return c.Context.Done()
}

func TestRefresherAutoAndManualShareOneRefresh(t *testing.T) {
	for _, firstManual := range []bool{false, true} {
		collector := &refreshCollector{started: make(chan struct{}), release: make(chan struct{})}
		store := &refreshStore{}
		r := NewRefresher(collector, store, RefreshOptions{})
		errs := make(chan error, 2)
		first, second := r.RefreshAuto, r.RefreshManual
		if firstManual {
			first, second = second, first
		}
		go func() { errs <- first(context.Background()) }()
		<-collector.started
		ctx := &waitingContext{Context: context.Background(), joined: make(chan struct{})}
		go func() { errs <- second(ctx) }()
		<-ctx.joined
		close(collector.release)
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		// 자동 조회에 합류한 수동 요청도 연타 제한의 기준이 된다.
		if err := r.RefreshManual(context.Background()); err != nil {
			t.Fatal(err)
		}
		want := int32(len(SupportedVendors()))
		if collector.calls.Load() != want || store.calls.Load() != want {
			t.Fatal("동시 요청 또는 연타가 중복 조회됐다")
		}
	}
}

func TestRefresherManualCooldownBoundary(t *testing.T) {
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	collector := &refreshCollector{}
	r := NewRefresher(collector, &refreshStore{}, RefreshOptions{Now: func() time.Time { return now }})
	if err := r.RefreshAuto(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 자동 완료 직후에도 버튼은 실행된다.
	if err := r.RefreshManual(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := int32(2 * len(SupportedVendors()))
	if collector.calls.Load() != want {
		t.Fatal("자동 완료가 수동 요청을 막았다")
	}
	now = now.Add(9 * time.Second)
	if err := r.RefreshManual(context.Background()); err != nil {
		t.Fatal(err)
	}
	if collector.calls.Load() != want {
		t.Fatal("10초 안의 수동 요청이 실행됐다")
	}
	now = now.Add(time.Second)
	if err := r.RefreshManual(context.Background()); err != nil {
		t.Fatal(err)
	}
	want += int32(len(SupportedVendors()))
	if collector.calls.Load() != want {
		t.Fatal("10초 경계의 요청이 막혔다")
	}
	// 자동 갱신은 수동 제한과 관계없이 매 틱 실행한다.
	for range 2 {
		if err := r.RefreshAuto(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	want += int32(2 * len(SupportedVendors()))
	if collector.calls.Load() != want {
		t.Fatal("자동 갱신에 쿨다운이 적용됐다")
	}
}

type failingRefreshStore struct{ fail bool }

func (s *failingRefreshStore) UpsertVendorLimit(context.Context, Result, time.Time) error {
	if s.fail {
		return errors.New("저장 실패")
	}
	return nil
}
func TestRefresherStorageFailureAllowsRetry(t *testing.T) {
	c := &refreshCollector{}
	s := &failingRefreshStore{fail: true}
	r := NewRefresher(c, s, RefreshOptions{})
	if err := r.RefreshManual(context.Background()); err == nil {
		t.Fatal("저장 오류가 사라졌다")
	}
	s.fail = false
	if err := r.RefreshManual(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.calls.Load() != int32(2*len(SupportedVendors())) {
		t.Fatal("저장 실패 후 재시도가 막혔다")
	}
}
