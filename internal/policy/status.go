// Package policy는 실행 중인 데몬의 적용 정책과 서버 최신 판의 차이를 보여준다.
package policy

import (
	"sync"
	"time"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/heartbeat"
)

const FreshFor = 3 * time.Minute

type Snapshot struct {
	AppliedRevision  int              `json:"applied_revision"`
	AppliedAt        string           `json:"applied_at"`
	ExpectedRevision *int             `json:"expected_revision"`
	Status           string           `json:"status"`
	LastCheckedAt    string           `json:"last_checked_at"`
	Signals          contract.Signals `json:"signals"`
	Privacy          contract.Privacy `json:"privacy"`
}

type Service struct {
	mu        sync.RWMutex
	snap      Snapshot
	checkedAt time.Time
	now       func() time.Time
}

func New(revision int, appliedAt string, manifest contract.Manifest, enabled bool, now func() time.Time) *Service {
	status := "checking"
	if !enabled {
		status = "disabled"
	}
	return &Service{snap: Snapshot{AppliedRevision: revision, AppliedAt: appliedAt, Status: status,
		Signals: manifest.Signals, Privacy: manifest.Privacy}, now: now}
}

// Record는 보고 워커만 호출한다. 확인 실패 때도 마지막으로 확인한 판은 보존한다.
func (s *Service) Record(response heartbeat.Response, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.snap.Status = "error"
		return
	}
	s.checkedAt = s.now().UTC()
	s.snap.LastCheckedAt = s.checkedAt.Format(time.RFC3339)
	s.snap.ExpectedRevision = nil
	s.snap.Status = "unknown"
	if response.ExpectedConfigRevision == nil {
		return
	}
	revision := *response.ExpectedConfigRevision
	s.snap.ExpectedRevision = &revision
	switch {
	case revision > s.snap.AppliedRevision:
		s.snap.Status = "pending"
	case revision == s.snap.AppliedRevision:
		s.snap.Status = "current"
	default:
		s.snap.Status = "ahead"
	}
}

func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.snap
	if out.ExpectedRevision != nil {
		revision := *out.ExpectedRevision
		out.ExpectedRevision = &revision
	}
	if out.Status != "error" && !s.checkedAt.IsZero() && s.now().Sub(s.checkedAt) > FreshFor {
		out.Status = "stale"
	}
	return out
}
