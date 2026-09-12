package daemon

import (
	"context"
	"sync"
	"time"
)

const titleQueueSize = 128

type titleRequest struct {
	key        string
	ended      bool
	forget     bool
	retry      bool
	generation int
	retryCtx   context.Context
}

type titleState struct {
	saved       bool
	ended       bool
	finalized   bool
	attempts    int
	generation  int
	retryAfter  time.Time
	cancelRetry context.CancelFunc
}

func (s *titleState) cancelPending() {
	if s.cancelRetry != nil {
		s.cancelRetry()
		s.cancelRetry = nil
	}
}

type titlePolicy struct {
	now              func() time.Time
	refreshInterval  time.Duration
	finalRetryDelays []time.Duration
	stopAfterSuccess bool
}

// titleRefresher는 벤더별로 실행하며 큐·재시도·세션 상태의 수명을 관리한다.
// states는 워커만 접근한다. refresh는 조회와 저장이 모두 성공한 경우에만 true를 반환한다.
type titleRefresher struct {
	states  map[string]*titleState
	refresh func(context.Context, string) bool
	policy  titlePolicy
	ctx     context.Context
	cancel  context.CancelFunc
	queue   chan titleRequest
	done    chan struct{}
	once    sync.Once
	retries sync.WaitGroup
}

func newTitleRefresher(parent context.Context, refresh func(context.Context, string) bool, policy titlePolicy) *titleRefresher {
	if policy.now == nil {
		policy.now = time.Now
	}
	if policy.refreshInterval <= 0 {
		policy.refreshInterval = time.Minute
	}
	if policy.finalRetryDelays == nil {
		policy.finalRetryDelays = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute}
	}
	ctx, cancel := context.WithCancel(parent)
	r := &titleRefresher{
		states: make(map[string]*titleState), refresh: refresh, policy: policy,
		ctx: ctx, cancel: cancel, queue: make(chan titleRequest, titleQueueSize), done: make(chan struct{}),
	}
	go r.run()
	return r
}

// Enqueue는 포화 시 수집을 막지 않는다. 다음 세션 스냅샷이 다시 조회 기회를 준다.
func (r *titleRefresher) Enqueue(key string, ended bool) {
	if r == nil || key == "" || r.ctx.Err() != nil {
		return
	}
	select {
	case r.queue <- titleRequest{key: key, ended: ended}:
	default:
	}
}

// TryForget이 실패하면 조립기도 세션 제거를 다음 틱으로 미룬다.
func (r *titleRefresher) TryForget(key string) bool {
	if r == nil || key == "" {
		return true
	}
	select {
	case <-r.ctx.Done():
		return true
	case r.queue <- titleRequest{key: key, forget: true}:
		return true
	default:
		return false
	}
}

func (r *titleRefresher) run() {
	defer close(r.done)
	for {
		select {
		case <-r.ctx.Done():
			return
		case req := <-r.queue:
			if r.ctx.Err() != nil {
				return
			}
			state := r.states[req.key]
			if req.forget {
				if state != nil {
					state.cancelPending()
				}
				delete(r.states, req.key)
				continue
			}
			// 제거·재개 이전의 예약은 같은 키가 다시 등장해도 적용하지 않는다.
			if req.retry && (state == nil || req.generation != state.generation ||
				(req.retryCtx != nil && req.retryCtx.Err() != nil)) {
				continue
			}
			if state == nil {
				state = &titleState{}
				r.states[req.key] = state
			}
			if state.saved && r.policy.stopAfterSuccess {
				continue
			}
			if req.retry {
				state.cancelPending()
			}
			if !req.ended && state.ended {
				state.cancelPending()
				*state = titleState{generation: state.generation + 1}
			}
			if state.finalized || state.cancelRetry != nil {
				continue
			}
			if req.ended && !state.ended {
				state.ended = true
				state.generation++
			}
			if !state.ended && r.policy.now().Before(state.retryAfter) {
				continue
			}
			if r.refresh(r.ctx, req.key) {
				state.saved = true
				state.finalized = state.ended
				if state.ended || r.policy.stopAfterSuccess {
					continue
				}
			}
			state.retryAfter = r.policy.now().Add(r.policy.refreshInterval)
			if !state.ended {
				continue
			}
			if state.attempts == len(r.policy.finalRetryDelays) {
				state.finalized = true
				continue
			}
			delay := r.policy.finalRetryDelays[state.attempts]
			state.attempts++
			state.cancelRetry = r.schedule(req.key, state.generation, delay)
		}
	}
}

func (r *titleRefresher) schedule(key string, generation int, delay time.Duration) context.CancelFunc {
	ctx, cancel := context.WithCancel(r.ctx)
	r.retries.Add(1)
	go func() {
		defer r.retries.Done()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		select {
		case <-ctx.Done():
		case r.queue <- titleRequest{key: key, ended: true, retry: true, generation: generation, retryCtx: ctx}:
		}
	}()
	return cancel
}

// Close는 DB를 닫기 전에 워커와 예약된 재시도를 모두 멈춘다.
func (r *titleRefresher) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.cancel()
		<-r.done
		r.retries.Wait()
	})
}
