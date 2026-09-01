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

// RefreshOptions 는 Refresher의 정책과 테스트 seam이다. 영값은 운영 기본값이며,
// 예외가 하나다 — AutoCooldown 에는 기본값이 없다(아래).
type RefreshOptions struct {
	// AutoCooldown 은 기동·틱·창 열기의 최소 간격, ManualCooldown 은 새로고침 버튼의 것이다.
	// 둘로 나눈 이유는 ADR 0014 에 있다 — 사용자가 누른 것과 화면이 뜬 것은 의도의 세기가 다르다.
	//
	// **AutoCooldown 에는 기본값이 없다. 호출자가 자동 갱신 주기에서 파생시켜 넘겨야 한다.**
	// 쿨다운은 lastSuccess 부터 재고 그 시각은 조회가 끝난 뒤에 찍히므로, 주기와 같거나 더 길면
	// 다음 주기 갱신이 자기 쿨다운에 막혀 자동 갱신이 통째로 멈춘다. 여기 상수를 두면 주기를
	// 옮길 때 한쪽만 바뀌어 그 상태가 되므로, 두 값을 한 곳에서 계산하게 남겨 둔다 (daemon).
	// 0 이면 자동 갱신을 억제하지 않는다.
	AutoCooldown   time.Duration
	ManualCooldown time.Duration
	Timeout        time.Duration
	Now            func() time.Time
	// Logger 는 벤더별 조회 실패를 남길 곳이다. nil 이면 남기지 않는다.
	Logger *log.Logger
}

// Refresher 는 모든 벤더의 조회와 저장, 호출 빈도 제어를 한 경로로 묶는다.
// 동시에 들어온 호출은 진행 중인 한 번을 기다리고, 성공 직후 호출은 외부 조회 없이 끝난다.
type Refresher struct {
	collector VendorCollector
	store     LimitStore
	auto      time.Duration
	manual    time.Duration
	timeout   time.Duration
	now       func() time.Time
	logger    *log.Logger

	mu       sync.Mutex
	inFlight *refreshCall
	// 마지막 성공 시각을 등급별로 따로 잰다. 하나로 두면 창을 열어 나간 자동 갱신이 그 직후
	// 10초 동안 새로고침 버튼까지 막는다 — 창을 열고 바로 누르는 흔한 순서가 그 구간이라,
	// 버튼이 아무 일도 하지 않는 것처럼 보인다 (ADR 0014 의 "버튼은 사실상 항상 새 값").
	//
	// 수동 쿨다운은 연타만 막으면 되므로 자기 성공만 보면 된다. 반대 방향은 그대로 둔다 —
	// 버튼을 눌러 방금 조회했으면 자동 갱신도 쉬는 것이 맞다.
	lastAuto   time.Time
	lastManual time.Time
}

type refreshCall struct {
	done chan struct{}
	err  error
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
		auto:      opts.AutoCooldown,
		manual:    opts.ManualCooldown,
		timeout:   opts.Timeout,
		now:       opts.Now,
		logger:    opts.Logger,
	}
}

// RefreshAuto 는 사람이 누르지 않은 갱신이다 — 기동, 주기 틱, 트레이 창 열기가 여기로 온다.
// 자동 쿨다운이 걸린다.
func (r *Refresher) RefreshAuto(ctx context.Context) error {
	return r.refreshWithin(ctx, r.auto, false)
}

// RefreshManual 은 사용자가 새로고침 버튼을 누른 것이다. 짧은 쿨다운만 걸리므로 사실상
// 항상 벤더를 다시 조회한다 — 버튼을 눌렀는데 아무 일도 일어나지 않으면 고장으로 읽힌다.
func (r *Refresher) RefreshManual(ctx context.Context) error {
	return r.refreshWithin(ctx, r.manual, true)
}

// refreshWithin 은 모든 벤더를 조회해 저장한다. 진행 중 호출은 같은 작업의 완료를 기다린 뒤
// 쿨다운 판정으로 빠져나가므로 외부 요청을 중복 실행하지 않는다.
//
// 싱글플라이트는 등급을 보지 않는다. 동시 요청 병합은 억제가 아니라 중복 제거이고, 마침 도는
// 갱신이 자동이었다고 해서 수동 요청이 한 번 더 나갈 이유가 없다.
func (r *Refresher) refreshWithin(ctx context.Context, cooldown time.Duration, manual bool) error {
	if r == nil || r.collector == nil || r.store == nil {
		return nil
	}
	r.mu.Lock()
	now := r.now()
	// 수동은 자기 성공만 본다. 자동은 lastAuto 만 보면 되는데, 그 값이 등급과 무관하게
	// 모든 성공에서 갱신되기 때문이다 (아래).
	since := r.lastAuto
	if manual {
		since = r.lastManual
	}
	if !since.IsZero() && now.Sub(since) < cooldown {
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
		done := r.now()
		// 조회는 한 번뿐이므로 자동 쪽 시각은 등급과 무관하게 갱신한다. 방금 벤더를 두드렸다면
		// 곧 뒤따르는 틱·창 열기는 쉬어야 한다.
		r.lastAuto = done
		if manual {
			r.lastManual = done
		}
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
