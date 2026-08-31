package tray

import (
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// DefaultInterval 은 스냅샷 자동 갱신 주기다. 한도 창이 5시간·7일 단위로 움직이므로
// 1분 지연은 화면에 드러나지 않는다.
const DefaultInterval = 60 * time.Second

// State 는 트레이 아이콘이 말하는 한 단어다. 값 문자열이 프런트엔드와의 계약이다.
type State string

const (
	StateMonitoring State = "monitoring"
	// StatePaused 는 로컬 데이터는 있는데 데몬이 돌고 있지 않다는 뜻이다.
	StatePaused State = "paused"
	// StateNotInstalled 는 로컬 DB 자체가 없다는 뜻이다. 오류가 아니다 (ADR 0004).
	StateNotInstalled State = "not_installed"
)

// StaleLocalQuery 는 로컬 DB 조회가 실패했다는 뜻의 StaleReason 값이다.
const StaleLocalQuery = "local_query_failed"

type Monitoring struct {
	State             State `json:"state"`
	DatabaseAvailable bool  `json:"database_available"`
	// DaemonRunning 은 runtime.json 의 pid 가 살아 있다는 뜻이다. pid 는 재사용되므로 "아마" 다.
	DaemonRunning bool `json:"daemon_running"`
	// DaemonStale 은 runtime.json 은 있는데 그 프로세스가 없다는 뜻이다 — 비정상 종료다.
	DaemonStale bool `json:"daemon_stale"`
	// LastEventAt 은 데이터의 신선도다. 스냅샷을 만든 시각(RefreshedAt)과 다른 값이다.
	LastEventAt     int64 `json:"last_event_at"`
	RunningSessions int64 `json:"running_sessions"`
}

// Query 는 스냅샷 한 장의 조회 조건이다.
type Query struct {
	// TZ 는 "오늘" 의 경계를 정한다. 빈 문자열은 UTC, 잘못된 이름은 에러다.
	TZ string `json:"tz"`
	// RecentLimit 는 최근 세션 목록의 길이 상한이다. 0 이하는 기본값이다.
	RecentLimit int `json:"recent_limit"`
}

// Snapshot 은 트레이가 한 번에 받는 전부다.
//
// 트레이는 창을 열지 않고 곁눈질하는 화면이라 필요한 것이 한 응답에 다 들어 있어야 한다.
// 다섯 번 물으면 다섯 개의 실패 지점과 다섯 개의 로딩 상태가 생긴다.
type Snapshot struct {
	TZ   string `json:"tz"`
	Date string `json:"date"`

	Monitoring Monitoring `json:"monitoring"`

	// RefreshedAt 은 마지막으로 **성공한** 갱신, CheckedAt 은 마지막 **시도** 시각이다.
	// 갱신이 실패하면 CheckedAt 만 움직이고 Stale 이 선다.
	RefreshedAt int64  `json:"refreshed_at"`
	CheckedAt   int64  `json:"checked_at"`
	Stale       bool   `json:"stale"`
	StaleReason string `json:"stale_reason"`

	ActiveAgents   []string `json:"active_agents"`
	ActiveSessions int64    `json:"active_sessions"`

	// Recent 는 Home 과 같은 정의·같은 질의다. 변환 없이 그대로 싣는다 (ADR 0004).
	Recent          []dashboard.RecentSession `json:"recent_sessions"`
	RecentTruncated bool                      `json:"recent_truncated"`

	// Limits 는 벤더별 사용 한도다. 실패한 벤더도 unavailable 로 자리를 지킨다 —
	// 빠지면 화면이 "아직 로딩 중" 과 구분하지 못한다.
	Limits           []vendorlimit.Result `json:"limits"`
	LimitsObservedAt string               `json:"limits_observed_at"`
	Tightest         TightestLimit        `json:"tightest_limit"`
}

// emptySnapshot 은 아직 한 번도 성공하지 못했을 때의 모양이다. 슬라이스를 non-nil 로 두어
// JSON 에 null 이 나가지 않게 한다.
func emptySnapshot(tz string) Snapshot {
	return Snapshot{
		TZ:           tz,
		Monitoring:   Monitoring{State: StateNotInstalled},
		ActiveAgents: []string{},
		Recent:       []dashboard.RecentSession{},
		Limits:       []vendorlimit.Result{},
	}
}
