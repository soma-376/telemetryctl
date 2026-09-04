package daemon

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/your-org/pulsemetry/internal/codexapp"
)

const (
	codexTitleQueueSize       = 128
	codexTitleTimeout         = 10 * time.Second
	codexTitleRefreshInterval = time.Minute
)

var codexTitleFinalRetryDelays = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute}

type codexTitleStore interface {
	SetCodexTitle(context.Context, string, string) error
}

type codexTitleRequest struct {
	key        string
	ended      bool
	retry      bool
	generation int
}

type codexTitleState struct {
	lastChecked    time.Time
	ended          bool
	finalized      bool
	retryScheduled bool
	finalAttempts  int
	generation     int
}

type codexTitlePolicy struct {
	now              func() time.Time
	refreshInterval  time.Duration
	finalRetryDelays []time.Duration
}

// codexTitleRefresher는 별도 App Server 프로세스의 공유 저장소를 thread/read로 조회한다.
// Codex 앱의 App Server 알림은 프로세스 경계를 넘는다는 보장이 없으므로 사용하지 않는다.
type codexTitleRefresher struct {
	reader codexapp.ThreadReader
	store  codexTitleStore
	log    *log.Logger
	policy codexTitlePolicy

	ctx    context.Context
	cancel context.CancelFunc
	queue  chan codexTitleRequest
	done   chan struct{}
	once   sync.Once
}

func newCodexTitleRefresher(parent context.Context, reader codexapp.ThreadReader, store codexTitleStore, logger *log.Logger) *codexTitleRefresher {
	return newCodexTitleRefresherWithPolicy(parent, reader, store, logger, codexTitlePolicy{})
}

func newCodexTitleRefresherWithPolicy(parent context.Context, reader codexapp.ThreadReader, store codexTitleStore, logger *log.Logger, policy codexTitlePolicy) *codexTitleRefresher {
	if policy.now == nil {
		policy.now = time.Now
	}
	if policy.refreshInterval <= 0 {
		policy.refreshInterval = codexTitleRefreshInterval
	}
	if policy.finalRetryDelays == nil {
		policy.finalRetryDelays = append([]time.Duration(nil), codexTitleFinalRetryDelays...)
	}
	ctx, cancel := context.WithCancel(parent)
	r := &codexTitleRefresher{
		reader: reader, store: store, log: logger, policy: policy,
		ctx: ctx, cancel: cancel, queue: make(chan codexTitleRequest, codexTitleQueueSize), done: make(chan struct{}),
	}
	go r.run()
	return r
}

// Enqueue는 수집 저장을 막지 않는다. 진행 중 세션은 워커가 조회 간격을 제한하고,
// 종료 세션은 쿨다운과 무관하게 최종 조회한다.
func (r *codexTitleRefresher) Enqueue(sessionKey string, ended bool) {
	if r == nil || sessionKey == "" {
		return
	}
	select {
	case r.queue <- codexTitleRequest{key: sessionKey, ended: ended}:
	default:
	}
}

func (r *codexTitleRefresher) run() {
	defer close(r.done)
	states := make(map[string]*codexTitleState)

	for {
		select {
		case <-r.ctx.Done():
			return
		case req := <-r.queue:
			state := states[req.key]
			if state == nil {
				state = &codexTitleState{}
				states[req.key] = state
			}
			if req.retry {
				if req.generation != state.generation {
					continue
				}
				state.retryScheduled = false
			}
			// 유휴 마감 뒤 같은 세션이 다시 활동하면 이전 종료 상태와 예약 재시도를
			// 폐기하고 진행 중 폴링으로 돌아간다.
			if !req.ended && !req.retry && state.ended {
				state.ended = false
				state.finalized = false
				state.retryScheduled = false
				state.finalAttempts = 0
				state.lastChecked = time.Time{}
				state.generation++
			}
			if state.finalized {
				continue
			}
			if state.ended && state.retryScheduled && !req.retry {
				continue
			}
			if req.ended {
				if !state.ended {
					state.ended = true
					state.generation++
				}
			}

			now := r.policy.now()
			if !state.ended && !state.lastChecked.IsZero() && now.Sub(state.lastChecked) < r.policy.refreshInterval {
				continue
			}

			found, err := r.refresh(req.key)
			state.lastChecked = now
			if !state.ended {
				continue
			}
			if err == nil && found {
				state.finalized = true
				continue
			}

			state.finalAttempts++
			if state.finalAttempts > len(r.policy.finalRetryDelays) {
				state.finalized = true
				continue
			}
			state.retryScheduled = true
			r.schedule(req.key, state.generation, r.policy.finalRetryDelays[state.finalAttempts-1])
		}
	}
}

func (r *codexTitleRefresher) schedule(sessionKey string, generation int, delay time.Duration) {
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-r.ctx.Done():
			return
		case <-timer.C:
		}
		select {
		case <-r.ctx.Done():
		case r.queue <- codexTitleRequest{key: sessionKey, ended: true, retry: true, generation: generation}:
		}
	}()
}

func (r *codexTitleRefresher) refresh(sessionKey string) (bool, error) {
	ctx, cancel := context.WithTimeout(r.ctx, codexTitleTimeout)
	defer cancel()
	title, err := r.reader.ThreadName(ctx, sessionKey)
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, codexapp.ErrThreadIDRequired) {
			r.log.Printf("경고: Codex 세션 제목 조회 실패 (%s): %v", sessionKey, err)
		}
		return false, err
	}
	if title == "" {
		return false, nil
	}
	if err := r.store.SetCodexTitle(ctx, sessionKey, title); err != nil {
		r.log.Printf("경고: Codex 세션 제목 저장 실패 (%s): %v", sessionKey, err)
		return false, err
	}
	return true, nil
}

// Close는 DB와 App Server를 닫기 전에 워커와 예약된 재시도를 멈춘다.
func (r *codexTitleRefresher) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.cancel()
		<-r.done
	})
}
