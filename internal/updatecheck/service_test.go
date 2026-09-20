package updatecheck

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type checkerFunc func(context.Context) (Result, error)

func (f checkerFunc) Check(ctx context.Context) (Result, error) { return f(ctx) }

func TestServicePreservesSuccessAcrossFailuresAndRecovers(t *testing.T) {
	now := time.Date(2026, 9, 18, 9, 0, 0, 0, time.FixedZone("KST", 9*3600))
	result, checkErr := Result{LatestVersion: "2.0.0", UpdateAvailable: true}, error(nil)
	s := NewService("1.0.0", true, checkerFunc(func(context.Context) (Result, error) { return result, checkErr }), func() time.Time { return now })
	if got := s.Snapshot(); got.Status != StatusChecking || got.UpdateAvailable != nil || got.LastAttemptAt != "" || got.LastSuccessAt != "" {
		t.Fatalf("초기 상태=%+v", got)
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	initial := s.Snapshot()
	if initial.LastSuccessAt != "2026-09-18T00:00:00Z" || initial.Status != StatusReady {
		t.Fatalf("최초 성공=%+v", initial)
	}
	for _, tc := range []struct {
		err    error
		status string
	}{{ErrUnsupported, StatusUnsupported}, {errors.New("private error"), StatusError}} {
		now = now.Add(24 * time.Hour)
		checkErr = tc.err
		_ = s.Refresh(context.Background())
		got := s.Snapshot()
		if got.Status != tc.status || got.LatestVersion != initial.LatestVersion || got.UpdateAvailable == nil || !*got.UpdateAvailable || got.LastSuccessAt != initial.LastSuccessAt || got.LastAttemptAt == initial.LastAttemptAt {
			t.Fatalf("실패 후 성공값 보존 실패=%+v", got)
		}
	}
	now = now.Add(24 * time.Hour)
	checkErr, result = nil, Result{LatestVersion: "1.0.0", UpdateAvailable: false}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := s.Snapshot()
	if got.Status != StatusReady || got.UpdateAvailable == nil || *got.UpdateAvailable || got.LatestVersion != "1.0.0" || got.LastSuccessAt != now.UTC().Format(time.RFC3339) {
		t.Fatalf("복구 결과=%+v", got)
	}
	*got.UpdateAvailable = true
	if *s.Snapshot().UpdateAvailable {
		t.Fatal("호출자가 snapshot을 수정해 원본도 바뀌었다")
	}
}

func TestDisabledServiceDoesNotCheck(t *testing.T) {
	s := NewService("1.0.0", false, checkerFunc(func(context.Context) (Result, error) {
		t.Fatal("비활성 상태에서 외부 조회가 실행됐다")
		return Result{}, nil
	}), nil)
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := s.Snapshot()
	if got.Status != StatusDisabled || got.LastAttemptAt != "" || got.UpdateAvailable != nil {
		t.Fatalf("비활성 상태=%+v", got)
	}
}

func TestSnapshotRemainsReadableDuringCheck(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := NewService("1.0.0", true, checkerFunc(func(context.Context) (Result, error) {
		close(entered)
		<-release
		return Result{LatestVersion: "2.0.0", UpdateAvailable: true}, nil
	}), nil)
	done := make(chan error, 1)
	go func() { done <- s.Refresh(context.Background()) }()
	<-entered
	if got := s.Snapshot(); got.Status != StatusChecking || got.LastAttemptAt == "" || got.UpdateAvailable != nil {
		t.Errorf("조회 중 상태=%+v", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotConcurrentReaders(t *testing.T) {
	s := NewService("1.0.0", true, checkerFunc(func(context.Context) (Result, error) {
		return Result{LatestVersion: "2.0.0", UpdateAvailable: true}, nil
	}), nil)
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 100 {
				got := s.Snapshot()
				if got.UpdateAvailable != nil {
					*got.UpdateAvailable = false
				}
			}
		})
	}
	for range 50 {
		if err := s.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	readers.Wait()
	if !*s.Snapshot().UpdateAvailable {
		t.Fatal("원본 상태가 독자에게 노출됐다")
	}
}
