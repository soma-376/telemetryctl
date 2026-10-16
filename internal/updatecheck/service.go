package updatecheck

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Service 는 마지막 성공 결과를 메모리에 보관한다. 주기 실행은 데몬이 소유한다.
type Service struct {
	mu      sync.RWMutex
	snap    Snapshot
	checker Checker
	now     func() time.Time
}

func NewService(version string, enabled bool, checker Checker, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	status := StatusChecking
	if !enabled {
		status = StatusDisabled
	}
	return &Service{snap: Snapshot{Status: status, CurrentVersion: version}, checker: checker, now: now}
}

// Snapshot 은 네트워크 조회 없이 호출자가 원본을 바꿀 수 없는 복사본을 반환한다.
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.snap
	if out.UpdateAvailable != nil {
		available := *out.UpdateAvailable
		out.UpdateAvailable = &available
	}
	return out
}

// Refresh 는 데몬의 단일 워커만 호출한다. 실패해도 마지막 성공값을 보존한다.
func (s *Service) Refresh(ctx context.Context) error {
	s.mu.Lock()
	if s.snap.Status == StatusDisabled {
		s.mu.Unlock()
		return nil
	}
	s.snap.Status = StatusChecking
	s.snap.LastAttemptAt = s.now().UTC().Format(time.RFC3339)
	s.mu.Unlock()

	result, err := s.checker.Check(ctx)
	if err == nil && strings.TrimSpace(result.LatestVersion) == "" {
		err = ErrInvalidResponse
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.snap.Status = StatusError
		if errors.Is(err, ErrUnsupported) {
			s.snap.Status = StatusUnsupported
		}
		return err
	}
	s.snap.Status = StatusReady
	s.snap.LatestVersion = result.LatestVersion
	s.snap.UpdateAvailable = &result.UpdateAvailable
	s.snap.LastSuccessAt = s.now().UTC().Format(time.RFC3339)
	return nil
}
