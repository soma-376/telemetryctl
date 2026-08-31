// Package vendorlimit 은 Claude Code·Codex 의 **구독 사용 한도**를 읽기 전용으로 조회해
// 하나의 공통 모델로 정규화한다 (PROJ-95).
//
// 여기서 다루는 값은 우리가 수집한 텔레메트리가 아니다. 벤더가 자기 계정에 대해
// 알려주는 "이번 창에서 얼마나 썼는가" 다. 로컬 DB 의 집계로는 절대 알 수 없는 값이라
// (한도는 벤더가 정하고 창은 벤더가 굴린다) 벤더에게 직접 물어보는 길밖에 없다.
//
// # 읽기 전용이라는 말의 범위
//
//   - Claude 자격증명 파일은 **열되 쓰지 않는다.** 토큰 갱신도 하지 않는다. 만료되면
//     unavailable 로 두고 Claude Code가 갱신해 주기를 기다린다.
//   - Claude 토큰은 **프로세스 메모리에만** 산다. SQLite·로그·오류 문자열·GUI 응답 어디에도
//     남기지 않는다. 그 규칙을 규율이 아니라 타입으로 지키는 것이 Token 이다 (claude_token.go).
//   - Codex 자격증명은 읽지 않는다. 인증과 토큰 갱신은 Codex App Server에 위임한다 (ADR 0011).
//
// # 왜 결과가 벤더별인가
//
// 한 벤더가 실패했다고 다른 벤더의 숫자까지 못 보게 되면 화면이 쓸모없어진다. 자격증명
// 파일이 없는 상태(로그인 안 함)는 정상 상태이지 오류가 아니고, 그런 사용자가 훨씬 많다.
// 그래서 조회는 error 대신 **벤더마다 State 와 Reason 을 담은 Result** 를
// 돌려준다. 호출자가 분기할 수 있게 Reason 은 기계 판독 가능한 상수다.
//
// # 지원 범위
//
// Claude Code 와 Codex 뿐이다. Gemini CLI·Cursor 는 이 티켓의 범위 밖이다.
package vendorlimit

import (
	"math"
	"time"

	"github.com/your-org/pulsemetry/internal/vendor"
)

// Vendor 는 로컬 파이프라인의 정식 벤더 ID를 그대로 쓴다. 별도 명명 타입을 만들지 않는다.
type Vendor = vendor.ID

const (
	VendorClaudeCode = vendor.ClaudeCode
	VendorCodex      = vendor.Codex
)

// SupportedVendors 는 이 패키지가 조회하는 벤더 전부다. 순서가 곧 Collect 결과의 순서다.
func SupportedVendors() []Vendor { return []Vendor{VendorClaudeCode, VendorCodex} }

// State 는 벤더 하나의 조회 성패다. 화면은 이 값만 보고 카드를 그릴지 안내 문구를 띄울지 정한다.
type State string

const (
	// StateAvailable 은 사용 한도를 실제로 읽어 왔다는 뜻이다.
	StateAvailable State = "available"
	// StateUnavailable 은 이 벤더의 숫자를 지금 알 수 없다는 뜻이다. 오류가 아니라 상태다.
	StateUnavailable State = "unavailable"
)

// Reason 은 unavailable 의 기계 판독 가능한 원인이다.
//
// 화면 문구는 이 값에서 파생시키고 Detail 문자열을 파싱하지 않는다. Detail 은 사람이 읽는
// 보조 설명이며 언제든 바뀔 수 있다. **어느 쪽에도 토큰 조각이 들어가지 않는다.**
type Reason string

const (
	ReasonNone Reason = ""
	// ReasonNotProbed 는 아직 한 번도 조회하지 않았다는 뜻이다. 데몬이 막 떴거나 꺼져
	// 있는 상태이지 사용자가 뭘 잘못한 것이 아니다.
	//
	// ReasonCredentialMissing 과 반드시 구분해야 한다. 둘을 뭉치면 로그인이 멀쩡한
	// 사용자에게 "로그인하지 않았습니다" 라고 말하게 된다.
	ReasonNotProbed Reason = "not_probed"
	// ReasonCredentialMissing 은 자격증명 파일이 없다는 뜻이다 — 해당 도구에 로그인하지
	// 않았거나 아예 설치하지 않은, 가장 흔한 정상 상태다.
	ReasonCredentialMissing Reason = "credential_missing"
	// ReasonCredentialUnreadable 은 파일은 있으나 읽을 권한이 없다는 뜻이다.
	ReasonCredentialUnreadable Reason = "credential_unreadable"
	// ReasonCredentialMalformed 는 파일을 읽었으나 우리가 아는 모양이 아니라는 뜻이다.
	// 벤더가 자격증명 파일 형식을 바꾸면 여기로 온다.
	ReasonCredentialMalformed Reason = "credential_malformed"
	// ReasonTokenExpired 는 토큰이 만료됐다는 뜻이다. 파일의 만료 시각으로 미리 알거나
	// 상위가 401·403 으로 거부해서 알게 된다. 우리는 갱신하지 않으므로 벤더 CLI 를 기다린다.
	ReasonTokenExpired Reason = "token_expired"
	// ReasonNetwork 는 요청이 상대에 닿지 못했다는 뜻이다 (DNS·연결 거부·타임아웃·취소).
	ReasonNetwork Reason = "network_error"
	// ReasonUpstreamStatus 는 상위가 2xx 가 아닌 응답을 줬다는 뜻이다 (401·403 제외).
	ReasonUpstreamStatus Reason = "upstream_status"
	// ReasonResponseUnrecognized 는 2xx 를 받았으나 본문이 우리가 아는 모양이 아니라는
	// 뜻이다. **비공개 API 가 바뀌면 여기로 온다** — 이 값이 화면에 늘어나기 시작하면
	// 어댑터를 고쳐야 한다는 신호다.
	ReasonResponseUnrecognized Reason = "response_unrecognized"
	// ReasonInternal 은 어댑터가 예상 못 한 방식으로 죽었다는 뜻이다. 한 벤더의 버그가
	// 다른 벤더의 숫자까지 지우지 않게 하는 마지막 그물이다.
	ReasonInternal Reason = "internal_error"
)

// PeriodKind 는 한도 창의 종류다. 벤더마다 창을 부르는 이름이 다르므로(Claude 는
// five_hour·seven_day, Codex 는 window_minutes 정수) 화면이 분기하지 않도록 여기서 통일한다.
type PeriodKind string

const (
	PeriodFiveHour PeriodKind = "five_hour"
	PeriodWeekly   PeriodKind = "weekly"
	PeriodMonthly  PeriodKind = "monthly"
	// PeriodUnknown 은 우리가 아는 창이 아니라는 뜻이다. 창을 버리지 않고 이 종류로
	// 넘기는 이유는, 벤더가 새 창을 추가했을 때 숫자가 조용히 사라지는 것보다
	// "모르는 창" 으로 보이는 편이 낫기 때문이다. Label 에 원래 이름이 남는다.
	PeriodUnknown PeriodKind = "unknown"
)

// periodFromMinutes 는 창 길이(분)를 PeriodKind 로 옮긴다. Codex 가 창을 분으로 준다.
// 벤더가 창 경계를 조금씩 흔들어도(주 = 10080분 근처) 같은 종류로 묶이도록 범위로 판단한다.
func periodFromMinutes(minutes int) PeriodKind {
	switch {
	case minutes <= 0:
		return PeriodUnknown
	case minutes <= 6*60:
		return PeriodFiveHour
	case minutes <= 14*24*60:
		return PeriodWeekly
	case minutes <= 40*24*60:
		return PeriodMonthly
	default:
		return PeriodUnknown
	}
}

// Window 는 한도 창 하나다. 화면의 진행 막대 한 줄이 이 구조체 하나다.
type Window struct {
	Period PeriodKind `json:"period"`
	// Label 은 벤더가 그 창을 부르는 원래 이름이다 (예: "seven_day_opus", "primary").
	// 같은 PeriodKind 의 창이 둘 이상일 때 사람이 구분할 유일한 근거다.
	Label string `json:"label"`
	// WindowMinutes 는 창 길이(분)다. 벤더가 알려주지 않으면 0 이다.
	WindowMinutes int `json:"window_minutes"`
	// UsedRatio 는 0.0~1.0 의 사용률이다. 벤더는 퍼센트로 주지만 화면이 매번 100 으로
	// 나누지 않도록 여기서 비율로 통일한다. 한도를 넘겨 쓴 경우 1.0 을 넘을 수 있다.
	UsedRatio float64 `json:"used_ratio"`
	// ResetsAt 은 창이 초기화되는 시각(RFC3339, UTC)이다. 모르면 빈 문자열이다.
	ResetsAt string `json:"resets_at"`
	// ResetsInSeconds 는 초기화까지 남은 초다. 모르면 0 이다.
	//
	// ResetsAt 과 둘 다 두는 이유: 벤더에 따라 절대 시각만 주거나(Claude) 남은 시간만
	// 준다(Codex). 화면이 벤더마다 분기하지 않도록 어댑터가 나머지 한쪽을 파생시킨다
	// (resolveResetTimes). 파생값은 우리 시계에 의존하므로 벤더가 준 쪽을 덮어쓰지 않는다.
	ResetsInSeconds int64 `json:"resets_in_seconds"`
}

// ExtraAllowance 는 플랜 한도를 넘겨 쓸 수 있는 **추가 한도**다 (Claude 의 extra usage,
// Codex 의 크레딧).
//
// Supported=false 는 "벤더가 이 계정에 대해 추가 한도를 알려주지 않았다" 는 뜻이고,
// Supported=true·Enabled=false 는 "알려줬는데 꺼져 있다" 는 뜻이다. 둘을 뭉치면 화면이
// "추가 한도 없음" 과 "모름" 을 구분하지 못한다.
type ExtraAllowance struct {
	Supported bool `json:"supported"`
	Enabled   bool `json:"enabled"`
	// UsedRatio 는 추가 한도 자체의 소진율(0.0~1.0)이다. 모르면 0 이다.
	UsedRatio float64 `json:"used_ratio"`
}

// Result 는 벤더 하나의 조회 결과다.
//
// # 여기에 토큰이 들어갈 자리는 없다
//
// 이 구조체는 Wails 바인딩을 통해 그대로 TS 필드가 된다 (ADR 0004). 필드를 추가할 때는
// "이 값이 화면 스크린샷으로 유출돼도 무해한가" 를 먼저 물어야 한다. 그 물음을 자동화한
// 것이 leak_test.go 다 — 결과 전체를 리플렉션으로 훑어 토큰 조각이 없음을 단언한다.
type Result struct {
	Vendor Vendor `json:"vendor"`
	State  State  `json:"state"`
	Reason Reason `json:"reason"`
	// Detail 은 사람이 읽는 짧은 설명이다. 화면 분기는 Reason 으로 하고 이 값을 파싱하지 않는다.
	Detail string `json:"detail"`
	// Plan 은 벤더가 알려준 구독 플랜 이름이다 (예: "max", "pro"). 모르면 빈 문자열이다.
	Plan string `json:"plan"`
	// Windows 는 항상 non-nil 이다. nil 이면 JSON 에서 null 이 되어 화면이 분기해야 한다.
	Windows    []Window       `json:"windows"`
	Extra      ExtraAllowance `json:"extra"`
	ObservedAt string         `json:"observed_at"`
}

// Snapshot 은 한 번의 조회로 모은 모든 벤더의 결과다.
type Snapshot struct {
	// Results 는 SupportedVendors 순서로 항상 모든 벤더가 들어 있다. 실패한 벤더도
	// unavailable Result 로 자리를 지킨다 — 빠지면 화면이 "아직 로딩 중" 과 구분하지 못한다.
	Results    []Result `json:"results"`
	ObservedAt string   `json:"observed_at"`
}

// unavailable 은 실패한 벤더의 Result 를 만든다. 성공 경로와 같은 모양을 유지해야
// 화면이 State 하나만 보고 분기할 수 있다.
func unavailable(v Vendor, reason Reason, detail string, now time.Time) Result {
	return Result{
		Vendor:     v,
		State:      StateUnavailable,
		Reason:     reason,
		Detail:     detail,
		Windows:    []Window{},
		ObservedAt: formatTime(now),
	}
}

// formatTime 은 밖으로 나가는 시각 표기를 RFC3339 UTC 하나로 고정한다.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ratioFromPercent 는 벤더의 퍼센트를 0.0~ 비율로 옮긴다.
//
// NaN·Inf 를 반드시 걸러야 한다. 벤더가 null 이나 이상값을 주면 float64 에 NaN 이 앉고,
// 그대로 두면 **json.Marshal 이 통째로 실패해** 멀쩡한 다른 벤더의 결과까지 화면에서 사라진다.
// 음수도 0 으로 접는다 — 진행 막대가 뒤로 그려지는 것보다 0 이 정직하다.
func ratioFromPercent(percent float64) float64 {
	return sanitizeRatio(percent / 100)
}

func sanitizeRatio(r float64) float64 {
	if math.IsNaN(r) || math.IsInf(r, 0) || r < 0 {
		return 0
	}
	return r
}
