package dashboard

// Tray 스냅샷 (PROJ-96).
//
// 트레이는 창을 열지 않고 곁눈질하는 화면이다. 그래서 필요한 것이 **한 응답**에 다 들어
// 있어야 한다 — 모니터링 상태, 마지막 갱신 시각, 활성·최근 세션, 벤더 한도, 가장 빠듯한
// 한도. 다섯 번 물으면 다섯 개의 실패 지점과 다섯 개의 로딩 상태가 생기고, 트레이는 그
// 복잡도를 감당할 화면이 아니다.
//
// # 새 SQL 을 쓰지 않는다
//
// 로컬 부분은 이미 있는 Status(status.go)와 Home(home.go)을 그대로 부른다. 같은 질문에
// 두 벌의 질의를 두면 트레이와 본 화면이 서로 다른 숫자를 말하기 시작하고, 어느 쪽이
// 맞는지 판정할 근거가 없다 (ADR 0004 의 "CLI 와 GUI 가 같은 함수로 같은 숫자를 낸다").
//
// # 부분 장애가 전체를 지우지 않는다
//
// 벤더 한도는 internal/vendorlimit 이 모은다. 그 패키지는 **error 를 반환하지 않고**
// 벤더마다 State·Reason 을 돌려준다 — 한 벤더가 실패해도 다른 벤더와 최근 세션 표시는
// 그대로 살아 있어야 하기 때문이다. 여기서는 그 결과를 손대지 않고 그대로 실어 보내고,
// 「가장 빠듯한 한도」 선택에서만 available 이 아닌 벤더를 후보에서 제외한다.
//
// # 새로고침 실패 시 마지막 정상 스냅샷을 유지한다
//
// 로컬 조회가 실패하면 트레이를 비우지 않고 **직전 정상 스냅샷을 그대로 두고 Stale 만
// 세운다.** 트레이에서 숫자가 사라지는 것은 "지금 값을 못 읽었다" 가 아니라 "활동이 없다"
// 로 읽히고, 그 오해는 사용자가 도구를 끄게 만든다. CheckedAt 과 RefreshedAt 이 갈라져
// 있는 것이 그 사실을 화면에 드러내는 수단이다.

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// DefaultTrayInterval 은 스냅샷 자동 갱신 주기다.
//
// # 왜 60초인가
//
// 아래가 전부 벤더 한도 조회에서 온다. 로컬 조회만 있으면 더 자주 돌아도 되지만, 한
// 응답으로 묶인 이상 주기는 가장 비싼 쪽에 맞춰야 한다.
//
//   - 벤더 한도는 **남의 비공개 API** 다. 초 단위로 두드리면 차단당할 여지가 있고,
//     차단은 우리가 아니라 사용자의 계정에 걸린다.
//   - 한도 창은 5시간·7일 단위로 움직인다. 그 값이 1분 사이에 의미 있게 변할 일이 없다.
//   - 트레이는 사용자가 계속 보고 있는 화면이 아니다. 1분 지연은 인지되지 않는다.
//
// 트레이의 "새로고침" 같은 명시적 조작은 이 주기를 기다리지 않고 Refresh 를 부른다.
const DefaultTrayInterval = 60 * time.Second

// TrayState 는 트레이 아이콘이 말하는 한 단어다. 값 문자열이 프런트엔드와의 계약이다.
type TrayState string

const (
	// TrayStateMonitoring 은 데몬이 살아 있어 지금도 수집하고 있다는 뜻이다.
	TrayStateMonitoring TrayState = "monitoring"
	// TrayStatePaused 는 로컬 데이터는 있는데 데몬이 돌고 있지 않다는 뜻이다.
	// 과거 데이터는 그대로 보이지만 새 활동은 들어오지 않는다.
	TrayStatePaused TrayState = "paused"
	// TrayStateNotInstalled 는 로컬 DB 자체가 없다는 뜻이다 — 미설치이거나 로컬
	// 파이프라인을 켠 적이 없다. 오류가 아니다 (ADR 0004).
	TrayStateNotInstalled TrayState = "not_installed"
)

// TrayMonitoring 은 트레이가 그리는 모니터링 상태다.
type TrayMonitoring struct {
	State TrayState `json:"state"`
	// DatabaseAvailable 은 로컬 DB 를 열었다는 뜻이다.
	DatabaseAvailable bool `json:"database_available"`
	// DaemonRunning 은 runtime.json 의 pid 가 살아 있다는 뜻이다. pid 는 재사용되므로
	// 이 값은 "아마" 다 (Status.Daemon 주석).
	DaemonRunning bool `json:"daemon_running"`
	// DaemonStale 은 runtime.json 은 있는데 그 프로세스가 없다는 뜻이다 — 비정상 종료다.
	DaemonStale bool `json:"daemon_stale"`
	// LastEventAt 은 마지막으로 수집된 이벤트 시각(UTC unix 초)이다. 0 이면 아직 없음.
	// **데이터의 신선도**이고, 스냅샷을 만든 시각(RefreshedAt)과 다른 값이다.
	LastEventAt int64 `json:"last_event_at"`
	// RunningSessions 는 지금 진행 중인(ended_at IS NULL) 세션 수다.
	RunningSessions int64 `json:"running_sessions"`
}

// TightestLimit 은 「가장 빠듯한 한도」 하나다.
//
// # 선택 규칙 — 결정론이어야 한다
//
// 같은 입력에 늘 같은 답을 내야 한다. 그러지 않으면 트레이를 볼 때마다 다른 창이 강조되고
// 사용자는 그 표시를 믿지 않게 된다. 순서대로 다음을 본다.
//
//  1. **후보**: State 가 available 인 벤더의 창만. unavailable 벤더는 숫자 자체가 없으므로
//     이길 수도, 다른 벤더의 선택을 흔들 수도 없다.
//  2. **사용률이 높은 쪽**이 이긴다 (UsedRatio 내림차순). "빠듯하다" 의 1차 정의다.
//  3. 사용률이 같으면 **초기화가 빠른 쪽**이 이긴다 (ResetsInSeconds 오름차순).
//     같은 90% 라도 5분 뒤 풀리는 창보다 6일 뒤 풀리는 창이 더 아프다.
//     초기화 시각을 **모르는 창**(ResetsInSeconds == 0)은 아는 창보다 항상 뒤로 간다 —
//     0 을 "0초 뒤" 로 읽으면 정보가 없는 창이 늘 이겨 버린다.
//  4. 그래도 같으면 **벤더 이름 오름차순** → **창 종류**(5시간 → 주 → 월 → 미상) →
//     **Label 오름차순** → **입력 순서**. 여기까지 오면 전순서라 결과가 하나로 확정된다.
type TightestLimit struct {
	// Found 가 false 면 후보가 하나도 없었다는 뜻이다. 나머지 필드는 영값이다.
	Found  bool                   `json:"found"`
	Vendor string                 `json:"vendor"`
	Period vendorlimit.PeriodKind `json:"period"`
	Label  string                 `json:"label"`
	// UsedRatio 는 0.0~1.0 사용률이다. 한도를 넘겨 쓴 경우 1.0 을 넘을 수 있다.
	UsedRatio float64 `json:"used_ratio"`
	// ResetsAt 은 RFC3339 UTC 다. 모르면 빈 문자열, ResetsInSeconds 는 0 이다.
	ResetsAt        string `json:"resets_at"`
	ResetsInSeconds int64  `json:"resets_in_seconds"`
}

// TrayQuery 는 스냅샷 한 장의 조회 조건이다.
type TrayQuery struct {
	// TZ 는 "오늘" 의 경계를 정하는 시간대다. 빈 문자열은 UTC, 잘못된 이름은 에러다.
	TZ string `json:"tz"`
	// RecentLimit 는 최근 세션 목록의 길이 상한이다. 0 이하는 기본값이다.
	RecentLimit int `json:"recent_limit"`
}

// TraySnapshot 은 트레이가 한 번에 받는 전부다.
type TraySnapshot struct {
	TZ string `json:"tz"`
	// Date 는 TZ 기준 스냅샷이 다루는 날짜다.
	Date string `json:"date"`

	Monitoring TrayMonitoring `json:"monitoring"`

	// RefreshedAt 은 **마지막으로 성공한** 갱신 시각(UTC unix 초)이다. 화면의
	// "몇 분 전 기준" 이 이것을 쓴다.
	RefreshedAt int64 `json:"refreshed_at"`
	// CheckedAt 은 마지막 **시도** 시각이다. 갱신이 실패하면 RefreshedAt 은 그대로이고
	// 이 값만 움직인다.
	CheckedAt int64 `json:"checked_at"`
	// Stale 은 마지막 시도가 실패해 여기 담긴 값이 RefreshedAt 시점의 것이라는 뜻이다.
	Stale bool `json:"stale"`
	// StaleReason 은 Stale 의 기계 판독 가능한 원인이다. Stale 이 false 면 빈 문자열이다.
	StaleReason string `json:"stale_reason"`

	// ActiveAgents·ActiveSessions 는 지금 진행 중인 세션의 벤더와 수다.
	ActiveAgents   []string `json:"active_agents"`
	ActiveSessions int64    `json:"active_sessions"`

	// Recent 는 오늘 시작한 세션의 요약이다 (Home 과 같은 정의·같은 질의).
	Recent []RecentSession `json:"recent_sessions"`
	// RecentTruncated 가 true 면 목록이 RecentLimit 에서 잘렸다.
	RecentTruncated bool `json:"recent_truncated"`

	// Limits 는 벤더별 사용 한도다. **실패한 벤더도 unavailable 로 자리를 지킨다** —
	// 빠지면 화면이 "아직 로딩 중" 과 구분하지 못한다 (vendorlimit.Snapshot).
	Limits []vendorlimit.Result `json:"limits"`
	// LimitsObservedAt 은 한도를 관측한 시각(RFC3339 UTC)이다.
	LimitsObservedAt string `json:"limits_observed_at"`
	// Tightest 는 available 한 창들 중 가장 빠듯한 하나다.
	Tightest TightestLimit `json:"tightest_limit"`
}

// StaleReason 값. 화면 문구는 여기서 파생시킨다.
const (
	// TrayStaleLocalQuery 는 로컬 DB 조회가 실패했다는 뜻이다.
	TrayStaleLocalQuery = "local_query_failed"
)

// emptyTraySnapshot 은 아직 한 번도 성공하지 못했을 때의 모양이다.
//
// 슬라이스를 전부 non-nil 로 둔다. nil 이면 JSON 에서 null 이 되어 프런트엔드가 분기해야
// 한다 (absent_test.go 가 모든 조회에 대해 붙들고 있는 규칙과 같다).
func emptyTraySnapshot(tz string) TraySnapshot {
	return TraySnapshot{
		TZ:           tz,
		Monitoring:   TrayMonitoring{State: TrayStateNotInstalled},
		ActiveAgents: []string{},
		Recent:       []RecentSession{},
		Limits:       []vendorlimit.Result{},
	}
}

// TrayMonitor 는 스냅샷을 만들고 마지막 정상값을 들고 있는다.
//
// 상태를 갖는 이유가 곧 이 티켓의 요구사항이다 — 갱신 주기를 지키는 것과, 실패했을 때
// 직전 정상 스냅샷을 유지하는 것. 둘 다 "지난번에 무엇이었나" 를 기억해야 할 수 있다.
// 동시 사용에 안전하다.
type TrayMonitor struct {
	mu sync.Mutex

	reader *Reader
	// interval 은 자동 갱신 주기다. Snapshot 이 이 값 안이면 캐시를 그대로 준다.
	interval time.Duration

	// last 는 마지막으로 돌려준 스냅샷이다. hasGood 이 true 면 그 안에 한 번은 성공한
	// 값이 들어 있다.
	last    TraySnapshot
	hasGood bool
	// lastQuery 는 last 를 만든 조건이다. 조건이 바뀌면 주기와 무관하게 다시 만든다 —
	// 시간대가 다른 스냅샷을 캐시라고 돌려주면 화면이 남의 날짜를 그린다.
	lastQuery TrayQuery
	// checkedAt 은 마지막 **시도** 시각이다. 실패해도 갱신되므로 실패가 매 호출마다
	// 재시도로 번지지 않는다.
	checkedAt time.Time
	hasTried  bool

	// collect 는 벤더 한도 조회 seam 이다. 테스트가 네트워크에 닿지 않게 한다.
	collect func(context.Context) vendorlimit.Snapshot
	now     func() time.Time
}

// NewTrayMonitor 는 r 을 보는 트레이 스냅샷 제공자를 만든다.
//
// 벤더 한도는 internal/vendorlimit 의 기본 구성으로 조회한다 — 홈 디렉터리는 hostenv 가
// 판별하고 HTTP 클라이언트는 타임아웃이 걸린 기본값이다.
func NewTrayMonitor(r *Reader) *TrayMonitor {
	return &TrayMonitor{
		reader:   r,
		interval: DefaultTrayInterval,
		now:      time.Now,
		collect: func(ctx context.Context) vendorlimit.Snapshot {
			return vendorlimit.Collect(ctx, vendorlimit.Options{})
		},
	}
}

// Snapshot 은 갱신 주기를 지키는 조회다.
//
// 직전 스냅샷이 주기 안이고 조건이 같으면 그대로 돌려준다. 트레이는 메뉴를 열 때마다
// 불리는 화면이라, 그때마다 벤더 API 를 두드리면 주기를 정한 의미가 없다.
func (m *TrayMonitor) Snapshot(ctx context.Context, q TrayQuery) (TraySnapshot, error) {
	if _, err := loadLocation(q.TZ); err != nil {
		// 시간대 오타는 갱신 실패가 아니라 호출자 버그다. 마지막 정상값으로 덮지 않는다.
		return TraySnapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.hasTried && m.lastQuery == q && m.now().Sub(m.checkedAt) < m.interval {
		return m.last, nil
	}
	return m.refreshLocked(ctx, q), nil
}

// Refresh 는 주기를 무시하고 즉시 다시 만든다. 트레이의 "새로고침" 이 부른다.
func (m *TrayMonitor) Refresh(ctx context.Context, q TrayQuery) (TraySnapshot, error) {
	if _, err := loadLocation(q.TZ); err != nil {
		return TraySnapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshLocked(ctx, q), nil
}

// refreshLocked 는 실제 갱신이다. **error 를 돌려주지 않는다.**
//
// 갱신 실패는 화면이 사라질 이유가 아니라 상태다. 로컬 조회가 실패하면 직전 정상
// 스냅샷을 그대로 두고 Stale 과 CheckedAt 만 갱신한다. 한 번도 성공한 적이 없으면
// 빈 모양을 Stale 로 돌려준다 — 그때도 슬라이스는 non-nil 이라 화면은 분기 없이 그린다.
func (m *TrayMonitor) refreshLocked(ctx context.Context, q TrayQuery) TraySnapshot {
	now := m.now()
	m.checkedAt = now
	m.hasTried = true
	m.lastQuery = q

	local, err := trayLocalOf(ctx, m.reader, q)
	if err != nil {
		out := m.last
		if !m.hasGood {
			out = emptyTraySnapshot(q.TZ)
		}
		out.CheckedAt = now.Unix()
		out.Stale = true
		out.StaleReason = TrayStaleLocalQuery
		m.last = out
		return out
	}

	// 벤더 한도 조회는 실패해도 error 를 내지 않는다. 벤더마다 unavailable 로 표시될 뿐이고
	// 그 사실이 최근 세션이나 다른 벤더의 표시를 막지 않는다.
	limits := m.collect(ctx)
	if limits.Results == nil {
		limits.Results = []vendorlimit.Result{}
	}

	out := local
	out.RefreshedAt = now.Unix()
	out.CheckedAt = now.Unix()
	out.Limits = limits.Results
	out.LimitsObservedAt = limits.ObservedAt
	out.Tightest = tightestLimit(limits.Results)

	m.last = out
	m.hasGood = true
	return out
}

// trayLocalOf 는 스냅샷의 로컬 부분이다 — Status 와 Home 을 그대로 쓴다.
func trayLocalOf(ctx context.Context, r *Reader, q TrayQuery) (TraySnapshot, error) {
	st, err := r.Status(ctx)
	if err != nil {
		return TraySnapshot{}, err
	}
	home, err := r.Home(ctx, HomeQuery{TZ: q.TZ, RecentLimit: q.RecentLimit})
	if err != nil {
		return TraySnapshot{}, err
	}

	return TraySnapshot{
		TZ:   home.TZ,
		Date: home.Date,
		Monitoring: TrayMonitoring{
			State:             trayStateOf(st),
			DatabaseAvailable: st.Available,
			DaemonRunning:     st.Daemon.Running,
			DaemonStale:       st.Daemon.Stale,
			LastEventAt:       st.NewestEventAt,
			RunningSessions:   st.RunningSessions,
		},
		ActiveAgents:    home.ActiveAgents,
		ActiveSessions:  home.ActiveSessions,
		Recent:          home.Recent,
		RecentTruncated: home.RecentTruncated,
		Limits:          []vendorlimit.Result{},
	}, nil
}

// trayStateOf 는 상태 한 단어를 정한다.
//
// DB 부재가 데몬 생존보다 앞선다. 데몬이 막 떠서 아직 DB 를 만들지 않은 순간이 실제로
// 있고, 그때 "monitoring" 이라고 말하면 화면은 데이터가 곧 보일 것처럼 그린다.
func trayStateOf(st Status) TrayState {
	switch {
	case !st.Available:
		return TrayStateNotInstalled
	case st.Daemon.Running:
		return TrayStateMonitoring
	default:
		return TrayStatePaused
	}
}

// ── 가장 빠듯한 한도 ────────────────────────────────────────────────────────

// tightestLimit 은 available 한 벤더의 창 중 가장 빠듯한 하나를 고른다.
// 규칙과 그 근거는 TightestLimit 의 주석에 있다.
func tightestLimit(results []vendorlimit.Result) TightestLimit {
	var (
		best  TightestLimit
		found bool
	)
	for _, res := range results {
		if res.State != vendorlimit.StateAvailable {
			// unavailable 벤더는 숫자가 없다. 후보가 되면 0% 창이 "가장 빠듯한 한도" 로
			// 뽑히거나, 동률 규칙을 흔들어 결과를 바꾼다.
			continue
		}
		for _, w := range res.Windows {
			cand := TightestLimit{
				Found:           true,
				Vendor:          string(res.Vendor),
				Period:          w.Period,
				Label:           w.Label,
				UsedRatio:       w.UsedRatio,
				ResetsAt:        w.ResetsAt,
				ResetsInSeconds: w.ResetsInSeconds,
			}
			if !found || tighter(cand, best) {
				best, found = cand, true
			}
		}
	}
	return best
}

// tighter 는 a 가 b 보다 빠듯한지다. 전순서라 같은 입력에 늘 같은 답을 낸다.
func tighter(a, b TightestLimit) bool {
	if a.UsedRatio != b.UsedRatio {
		return a.UsedRatio > b.UsedRatio
	}
	if ra, rb := resetKey(a), resetKey(b); ra != rb {
		return ra < rb
	}
	if a.Vendor != b.Vendor {
		return a.Vendor < b.Vendor
	}
	if pa, pb := periodRank(a.Period), periodRank(b.Period); pa != pb {
		return pa < pb
	}
	// 여기까지 같으면 Label 이 마지막 갈림길이다. 그것도 같으면 먼저 본 쪽을 유지한다
	// (입력 순서 = vendorlimit.SupportedVendors 순서 → Windows 순서).
	return a.Label < b.Label
}

// resetKey 는 초기화까지 남은 초를 비교 가능한 값으로 만든다.
//
// 0 은 "모른다" 이고 "0초 뒤" 가 아니다 (vendorlimit.Window.ResetsInSeconds). 그대로
// 비교하면 정보가 없는 창이 언제나 이긴다. 그래서 모르는 값은 맨 뒤로 보낸다.
func resetKey(t TightestLimit) int64 {
	if t.ResetsInSeconds <= 0 {
		return math.MaxInt64
	}
	return t.ResetsInSeconds
}

// periodRank 는 창 종류의 고정 순서다. 짧은 창이 앞이다 — 같은 사용률·같은 초기화
// 시각이면 짧은 창 쪽이 먼저 다시 찬다.
func periodRank(p vendorlimit.PeriodKind) int {
	switch p {
	case vendorlimit.PeriodFiveHour:
		return 0
	case vendorlimit.PeriodWeekly:
		return 1
	case vendorlimit.PeriodMonthly:
		return 2
	default:
		return 3
	}
}
