package vendorlimit

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	// DefaultRefreshCooldown 동안은 직전 갱신 결과를 재사용한다. 수동 새로고침 연타와
	// 여러 GUI 창의 동시 요청이 벤더 API 호출로 증폭되지 않게 하는 최소 간격이다.
	DefaultRefreshCooldown = 10 * time.Second
	// DefaultRefreshTimeout 은 지원하는 모든 벤더를 한 번씩 조회하는 전체 상한이다.
	DefaultRefreshTimeout = 30 * time.Second
)

// LimitStore 는 갱신 결과를 최신 스냅샷으로 저장하는 최소 계약이다.
// store.DB 가 구현하며, 인터페이스를 여기에 둬 패키지 의존 방향을 뒤집지 않는다.
type LimitStore interface {
	UpsertVendorLimit(context.Context, Result, time.Time) error
}

// RefreshOptions 는 Refresher의 정책과 테스트 seam이다. 영값은 운영 기본값이다.
type RefreshOptions struct {
	Cooldown time.Duration
	Timeout  time.Duration
	Now      func() time.Time
	// Logger 는 벤더별 조회 실패를 남길 곳이다. nil 이면 남기지 않는다.
	Logger *log.Logger
}

// Refresher 는 모든 벤더의 조회와 저장, 호출 빈도 제어를 한 경로로 묶는다.
// 동시에 들어온 호출은 진행 중인 한 번을 기다리고, 성공 직후 호출은 외부 조회 없이 끝난다.
type Refresher struct {
	collector VendorCollector
	store     LimitStore
	cooldown  time.Duration
	timeout   time.Duration
	now       func() time.Time
	logger    *log.Logger

	mu          sync.Mutex
	inFlight    *refreshCall
	lastSuccess time.Time
}

type refreshCall struct {
	done chan struct{}
	err  error
}

// NewRefresher 는 데몬 수명 동안 재사용할 갱신기를 만든다.
func NewRefresher(collector VendorCollector, store LimitStore, opts RefreshOptions) *Refresher {
	if opts.Cooldown <= 0 {
		opts.Cooldown = DefaultRefreshCooldown
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultRefreshTimeout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Refresher{
		collector: collector,
		store:     store,
		cooldown:  opts.Cooldown,
		timeout:   opts.Timeout,
		now:       opts.Now,
		logger:    opts.Logger,
	}
}

// Refresh 는 모든 벤더를 조회해 저장한다. 진행 중 호출은 같은 작업의 완료를 기다린 뒤
// 쿨다운 판정으로 빠져나가므로 외부 요청을 중복 실행하지 않는다.
func (r *Refresher) Refresh(ctx context.Context) error {
	if r == nil || r.collector == nil || r.store == nil {
		return nil
	}
	r.mu.Lock()
	now := r.now()
	if !r.lastSuccess.IsZero() && now.Sub(r.lastSuccess) < r.cooldown {
		r.mu.Unlock()
		return nil
	}
	if active := r.inFlight; active != nil {
		r.mu.Unlock()
		select {
		case <-active.done:
			return active.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	active := &refreshCall{done: make(chan struct{})}
	r.inFlight = active
	r.mu.Unlock()

	active.err = r.refresh(ctx, now)
	r.mu.Lock()
	if active.err == nil {
		r.lastSuccess = r.now()
	}
	r.inFlight = nil
	close(active.done)
	r.mu.Unlock()
	return active.err
}

func (r *Refresher) refresh(ctx context.Context, checkedAt time.Time) error {
	callCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	writeCtx := context.WithoutCancel(ctx)

	var firstErr error
	for _, vendor := range SupportedVendors() {
		result := r.collector.CollectVendor(callCtx, vendor)
		// 조회 실패는 error 가 아니라 Result 의 상태다(패키지 머리 주석). 그래서 호출자가
		// 알아채려면 여기서 남겨야 한다 — 429 가 반복되는지 같은 것을 DB 스냅샷만으로는
		// 뒤늦게 알게 된다. Reason·Detail 에는 토큰이 들어가지 않는다(leak_test).
		if r.logger != nil && result.State != StateAvailable {
			r.logger.Printf("경고: %s 사용 한도를 읽지 못했다 (%s): %s",
				vendor, result.Reason, result.Detail)
		}
		if err := r.store.UpsertVendorLimit(writeCtx, result, checkedAt); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("한도 저장 실패 (%s): %w", vendor, err)
		}
	}
	return firstErr
}
