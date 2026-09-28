package codexsource

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupervisorNormalShutdownDoesNotRestart(t *testing.T) {
	db, _, root := workerFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	var starts atomic.Int32
	s := Supervisor{DB: db, Root: root, CheckInterval: 5 * time.Millisecond,
		HeartbeatLimit: 100 * time.Millisecond, JobDeadline: 100 * time.Millisecond, CancelWait: 50 * time.Millisecond,
		RunWorker: func(ctx context.Context, _ int64, progress func(bool)) error {
			starts.Add(1)
			progress(false)
			<-ctx.Done()
			return nil
		}}
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitForStarts(t, &starts, 1)
	cancel()
	select {
	case err := <-done:
		if err != nil || starts.Load() != 1 {
			t.Fatalf("정상 종료 오류/중복 재시작: err=%v starts=%d", err, starts.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("정상 종료 지연")
	}
	_, status, _, _, _, err := db.CodexWorkerStatus(context.Background())
	if err != nil || status != "stopped" {
		t.Fatalf("상태 = %s, %v", status, err)
	}
}

func TestSupervisorCancelledBeforeWorkerEpochIsNormalShutdown(t *testing.T) {
	db, _, root := workerFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var starts atomic.Int32
	s := Supervisor{DB: db, Root: root, RunWorker: func(context.Context, int64, func(bool)) error {
		starts.Add(1)
		return nil
	}}
	if err := s.Run(ctx); err != nil || starts.Load() != 0 {
		t.Fatalf("기동 전 정상 종료 = %v starts=%d", err, starts.Load())
	}
}

func TestSupervisorUnresponsiveWorkerIsFatalWithoutReplacement(t *testing.T) {
	db, _, root := workerFixture(t, true)
	block := make(chan struct{})
	defer close(block)
	var starts atomic.Int32
	s := Supervisor{DB: db, Root: root, CheckInterval: 5 * time.Millisecond,
		HeartbeatLimit: 20 * time.Millisecond, JobDeadline: 100 * time.Millisecond, CancelWait: 15 * time.Millisecond,
		RunWorker: func(context.Context, int64, func(bool)) error {
			starts.Add(1)
			<-block // 취소를 일부러 무시하는 워커
			return nil
		}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := s.Run(ctx)
	if !errors.Is(err, ErrWorkerUnresponsive) || starts.Load() != 1 {
		t.Fatalf("취소 미응답: err=%v starts=%d", err, starts.Load())
	}
}

func TestSupervisorProgressHeartbeatCannotExtendJobDeadline(t *testing.T) {
	db, _, root := workerFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := Supervisor{DB: db, Root: root, CheckInterval: 5 * time.Millisecond,
		HeartbeatLimit: 100 * time.Millisecond, JobDeadline: 35 * time.Millisecond, CancelWait: 50 * time.Millisecond,
		RunWorker: func(ctx context.Context, _ int64, progress func(bool)) error {
			progress(true)
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					progress(true)
				}
			}
		}}
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	deadline := time.After(time.Second)
	for {
		_, status, reason, _, _, err := db.CodexWorkerStatus(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status == "backoff" {
			if !strings.Contains(reason, "작업 기한") {
				t.Fatalf("heartbeat 중단으로 잘못 판정: %s", reason)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("작업 기한을 감지하지 못함")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorRepeatedFailureDegradesAfterFiveRestarts(t *testing.T) {
	db, _, root := workerFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var active, maxActive, starts atomic.Int32
	s := Supervisor{DB: db, Root: root, Backoff: []time.Duration{time.Millisecond},
		CheckInterval: 5 * time.Millisecond, CancelWait: 50 * time.Millisecond,
		RunWorker: func(context.Context, int64, func(bool)) error {
			current := active.Add(1)
			for {
				old := maxActive.Load()
				if current <= old || maxActive.CompareAndSwap(old, current) {
					break
				}
			}
			starts.Add(1)
			active.Add(-1)
			return errors.New("synthetic failure")
		}}
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	deadline := time.After(time.Second)
	for {
		_, status, _, _, _, err := db.CodexWorkerStatus(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status == "degraded" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("반복 실패 degraded 없음, starts=%d", starts.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if starts.Load() != 6 || maxActive.Load() != 1 {
		t.Fatalf("단일 워커/재시작 수: starts=%d max=%d", starts.Load(), maxActive.Load())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorWorkerPanicDegradesWithoutStartingParallelWorker(t *testing.T) {
	db, _, root := workerFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var starts atomic.Int32
	s := Supervisor{DB: db, Root: root, Backoff: []time.Duration{time.Millisecond},
		CheckInterval: 5 * time.Millisecond, CancelWait: 50 * time.Millisecond,
		RunWorker: func(context.Context, int64, func(bool)) error {
			starts.Add(1)
			panic("민감한 원문")
		}}
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	deadline := time.After(time.Second)
	for {
		_, status, reason, _, _, err := db.CodexWorkerStatus(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if status == "degraded" {
			if starts.Load() != 6 || !strings.Contains(reason, "panic") || strings.Contains(reason, "민감한") {
				t.Fatalf("panic 처리 상태: starts=%d reason=%q", starts.Load(), reason)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("panic 반복 실패를 감지하지 못함")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func waitForStarts(t *testing.T, value *atomic.Int32, wanted int32) {
	t.Helper()
	deadline := time.After(time.Second)
	for value.Load() < wanted {
		select {
		case <-deadline:
			t.Fatalf("워커 시작 = %d, want %d", value.Load(), wanted)
		case <-time.After(time.Millisecond):
		}
	}
}
