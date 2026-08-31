package tray

// 스냅샷 한 장을 조립한다. **데몬 쪽 코드다.**
//
// 로컬 SQLite 를 읽는 것은 데몬이고, GUI 는 그 결과를 HTTP 로 받는다 (internal/localapi).
// 여기서 벤더 API 를 두드리지 않는다 — 한도는 데몬의 갱신기가 이미 넣어 둔 것을 읽을 뿐이다.

import (
	"context"
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// Builder 는 조회 서비스를 보고 스냅샷을 만든다. 캐시하지 않는다 — 부를 때마다 지금 값이다.
type Builder struct {
	svc *dashboard.Service
	now func() time.Time
	// limits 는 한도 조회 seam 이다. 테스트가 DB 에 닿지 않게 한다.
	limits func(context.Context) vendorlimit.Snapshot
}

func NewBuilder(svc *dashboard.Service) *Builder {
	b := &Builder{svc: svc, now: time.Now}
	b.limits = b.vendorLimits
	return b
}

// Snapshot 은 트레이 한 장에 필요한 전부다.
func (b *Builder) Snapshot(ctx context.Context, q Query) (Snapshot, error) {
	out, err := localSnapshot(ctx, b.svc, q)
	if err != nil {
		return Snapshot{}, err
	}

	// 한도 조회는 실패해도 error 를 내지 않는다. 벤더마다 unavailable 로 표시될 뿐이고
	// 그 사실이 최근 세션이나 다른 벤더의 표시를 막지 않는다.
	limits := b.limits(ctx)
	if limits.Results == nil {
		limits.Results = []vendorlimit.Result{}
	}
	out.Limits = limits.Results
	out.LimitsObservedAt = limits.ObservedAt
	out.Tightest = TightestOf(limits.Results)
	return out, nil
}

func (b *Builder) vendorLimits(ctx context.Context) vendorlimit.Snapshot {
	if db, ok := b.svc.Querier(); ok {
		return VendorLimits(ctx, db, b.now())
	}
	return VendorLimits(ctx, nil, b.now())
}

// localSnapshot 은 스냅샷의 로컬 부분이다 — Status 와 RecentActivity 를 그대로 쓴다.
// 같은 질문에 두 벌의 질의를 두면 트레이와 본 화면이 서로 다른 숫자를 말하기 시작한다
// (ADR 0004).
func localSnapshot(ctx context.Context, svc *dashboard.Service, q Query) (Snapshot, error) {
	st, err := svc.Status(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	recent, err := svc.RecentActivity(ctx, dashboard.RecentQuery{TZ: q.TZ, Limit: q.RecentLimit})
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		TZ:   recent.TZ,
		Date: recent.Date,
		Monitoring: Monitoring{
			State:             stateOf(st),
			DatabaseAvailable: st.Available,
			DaemonRunning:     st.Daemon.Running,
			DaemonStale:       st.Daemon.Stale,
			LastEventAt:       st.NewestEventAt,
			RunningSessions:   st.RunningSessions,
		},
		ActiveAgents:    recent.ActiveAgents,
		ActiveSessions:  recent.ActiveSessions,
		Recent:          recent.Sessions,
		RecentTruncated: recent.Truncated,
		Limits:          []vendorlimit.Result{},
	}, nil
}

// stateOf 는 상태 한 단어를 정한다. DB 부재가 데몬 생존보다 앞선다 — 데몬이 막 떠서 아직
// DB 를 만들지 않은 순간에 "monitoring" 이라 하면 화면은 데이터가 곧 보일 것처럼 그린다.
func stateOf(st dashboard.Status) State {
	switch {
	case !st.Available:
		return StateNotInstalled
	case st.Daemon.Running:
		return StateMonitoring
	default:
		return StatePaused
	}
}
