package codexsource

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/your-org/pulsemetry/internal/store"
)

var ErrWorkerUnresponsive = errors.New("Codex 보강 워커가 취소 후에도 종료하지 않았다")

type Supervisor struct {
	DB                                                                   *store.DB
	Root                                                                 string
	Notify                                                               <-chan struct{}
	RunWorker                                                            func(context.Context, int64, func(bool)) error // 테스트용 실제 워커 경계
	CheckInterval, HeartbeatLimit, JobDeadline, CancelWait, ScanInterval time.Duration
	Backoff                                                              []time.Duration // 테스트에서만 기본 재시작 간격을 줄인다.
}

func (s Supervisor) worker(ctx context.Context, epoch int64, progress func(bool)) error {
	if s.RunWorker != nil {
		return s.RunWorker(ctx, epoch, progress)
	}
	return (Worker{DB: s.DB, Root: s.Root, Epoch: epoch, Notify: s.Notify, ScanInterval: s.ScanInterval, Progress: progress}).Run(ctx)
}

func (s Supervisor) Run(ctx context.Context) error {
	if s.DB == nil {
		return errors.New("Codex supervisor DB missing")
	}
	check := s.CheckInterval
	if check <= 0 {
		check = 5 * time.Second
	}
	heartbeatLimit := s.HeartbeatLimit
	if heartbeatLimit <= 0 {
		heartbeatLimit = 15 * time.Second
	}
	jobDeadline := s.JobDeadline
	if jobDeadline <= 0 {
		jobDeadline = 30 * time.Second
	}
	cancelWait := s.CancelWait
	if cancelWait <= 0 {
		cancelWait = 5 * time.Second
	}
	backoff := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second}
	if len(s.Backoff) > 0 {
		backoff = s.Backoff
	}
	restarts := []time.Time{}
	setStatus := func(epoch int64, status, reason string, count int) {
		bounded, stop := context.WithTimeout(context.Background(), min(cancelWait, 2*time.Second))
		defer stop()
		_ = s.DB.SetCodexWorkerStatus(bounded, epoch, status, reason, count)
	}
	for ctx.Err() == nil {
		epoch, err := s.DB.BeginCodexWorkerEpoch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		var lastBeat, jobStart atomic.Int64
		lastBeat.Store(time.Now().UnixNano())
		progress := func(active bool) {
			now := time.Now().UnixNano()
			lastBeat.Store(now)
			if active {
				jobStart.CompareAndSwap(0, now)
			} else {
				jobStart.Store(0)
			}
		}
		go func() {
			defer func() {
				if recover() != nil {
					// panic 값에는 원문이 포함될 수 있어 진단에 싣지 않는다.
					done <- errors.New("Codex 보강 워커 panic")
				}
			}()
			done <- s.worker(workerCtx, epoch, progress)
		}()
		ticker := time.NewTicker(check)
		var failure error
		workerExited := false
		for failure == nil {
			select {
			case <-ctx.Done():
				cancel()
				ticker.Stop()
				select {
				case <-done:
					setStatus(epoch, "stopped", "", len(restarts))
					return nil
				case <-time.After(cancelWait):
					return ErrWorkerUnresponsive
				}
			case err := <-done:
				if ctx.Err() != nil {
					cancel()
					ticker.Stop()
					return nil
				}
				workerExited = true
				if err == nil {
					err = errors.New("Codex 보강 워커가 예상 없이 종료했다")
				}
				failure = err
			case <-ticker.C:
				if time.Since(time.Unix(0, lastBeat.Load())) > heartbeatLimit {
					failure = errors.New("Codex 워커 heartbeat 초과")
				}
				// 작업 시간도 별도 검사한다. 취소는 단일 워커 종료 확인 뒤에만 재시작한다.
				if started := jobStart.Load(); failure == nil && started > 0 && time.Since(time.Unix(0, started)) > jobDeadline {
					failure = errors.New("Codex 워커 작업 기한 초과")
				}
			}
		}
		ticker.Stop()
		cancel()
		if !workerExited {
			select {
			case <-done:
			case <-time.After(cancelWait):
				return fmt.Errorf("%w: %v", ErrWorkerUnresponsive, failure)
			}
		}
		now := time.Now()
		kept := restarts[:0]
		for _, at := range restarts {
			if now.Sub(at) < 5*time.Minute {
				kept = append(kept, at)
			}
		}
		restarts = kept
		if len(restarts) >= 5 {
			setStatus(epoch, "degraded", failure.Error(), len(restarts))
			<-ctx.Done() // OTel 수집은 계속하고 데몬 재시작에서만 복구한다.
			return nil
		}
		restarts = append(restarts, now)
		setStatus(epoch, "backoff", failure.Error(), len(restarts))
		delay := backoff[min(len(restarts)-1, len(backoff)-1)]
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
	return nil
}
