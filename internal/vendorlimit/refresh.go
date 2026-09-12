package vendorlimit

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	// DefaultManualCooldown 은 사용자가 새로고침 버튼을 눌렀을 때의 최소 간격이다.
	// 연타만 막으면 되므로 짧다 — 10초 안에 두 번 누르는 것은 의도가 아니라 손이다.
	DefaultManualCooldown = 10 * time.Second
	// DefaultRefreshTimeout 은 지원하는 모든 벤더를 한 번씩 조회하는 전체 상한이다.
	DefaultRefreshTimeout = 30 * time.Second
)

// LimitStore 는 갱신 결과를 최신 스냅샷으로 저장하는 최소 계약이다.
// store.DB 가 구현하며, 인터페이스를 여기에 둬 패키지 의존 방향을 뒤집지 않는다.
type LimitStore interface {
	UpsertVendorLimit(context.Context, Result, time.Time) error
}

// RefreshOptions는 수동 제한과 조회 시간 상한을 설정한다.

type RefreshOptions struct {
	ManualCooldown time.Duration
	Timeout        time.Duration
	Now            func() time.Time
	// Logger 는 벤더별 조회 실패를 남길 곳이다. nil 이면 남기지 않는다.
	Logger *log.Logger
}

// Refresher 는 모든 벤더의 조회와 저장, 호출 빈도 제어를 한 경로로 묶는다.
// 동시에 들어온 호출은 진행 중인 한 번을 기다리고 수동 연타만 제한한다.
type Refresher struct {
	collector VendorCollector
	store     LimitStore
	manual    time.Duration
	timeout   time.Duration
	now       func() time.Time
	logger    *log.Logger

	mu       sync.Mutex
	inFlight *refreshCall
	// 수동 요청을 처리한 마지막 저장 완료 시각이다.
	lastManual time.Time
}

type refreshCall struct {
	done   chan struct{}
	manual bool
	err    error
}

// NewRefresher 는 데몬 수명 동안 재사용할 갱신기를 만든다.
func NewRefresher(collector VendorCollector, store LimitStore, opts RefreshOptions) *Refresher {
	if opts.ManualCooldown <= 0 {
		opts.ManualCooldown = DefaultManualCooldown
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
		manual:    opts.ManualCooldown,
		timeout:   opts.Timeout,
		now:       opts.Now,
		logger:    opts.Logger,
	}
}

// RefreshAuto는 데몬 기동과 주기 틱에서 호출한다. 주기 외 쿨다운은 없다.
func (r *Refresher) RefreshAuto(ctx context.Context) error {
	return r.refreshWithin(ctx, false)
}

// RefreshManual 은 사용자가 새로고침 버튼을 누른 것이다. 짧은 쿨다운만 걸리므로 사실상
// 항상 벤더를 다시 조회한다 — 버튼을 눌렀는데 아무 일도 일어나지 않으면 고장으로 읽힌다.
func (r *Refresher) RefreshManual(ctx context.Context) error {
	return r.refreshWithin(ctx, true)
}

// refreshWithin 은 모든 벤더를 조회해 저장한다. 진행 중 호출은 같은 작업의 완료를 기다린 뒤
// 같은 결과를 반환하므로 외부 요청을 중복 실행하지 않는다.
//
// 싱글플라이트는 등급을 보지 않는다. 동시 요청 병합은 억제가 아니라 중복 제거이고, 마침 도는
// 갱신이 자동이었다고 해서 수동 요청이 한 번 더 나갈 이유가 없다.
func (r *Refresher) refreshWithin(ctx context.Context, manual bool) error {
	if r == nil || r.collector == nil || r.store == nil {
		return nil
	}
	r.mu.Lock()
	now := r.now()
	if active := r.inFlight; active != nil {
		active.manual = active.manual || manual
		r.mu.Unlock()
		select {
		case <-active.done:
			return active.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if manual && !r.lastManual.IsZero() && now.Sub(r.lastManual) < r.manual {
		r.mu.Unlock()
		return nil
	}
	active := &refreshCall{done: make(chan struct{}), manual: manual}
	r.inFlight = active
	r.mu.Unlock()

	active.err = r.refresh(ctx, now)
	r.mu.Lock()
	if active.err == nil && active.manual {
		r.lastManual = r.now()
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
