// Package vendorlimit 은 Claude Code·Codex 의 **구독 사용 한도**를 읽기 전용으로 조회해
// 하나의 공통 모델로 정규화한다 (PROJ-95).
//
// 여기서 다루는 값은 우리가 수집한 텔레메트리가 아니다. 벤더가 자기 계정에 대해
// 알려주는 "이번 창에서 얼마나 썼는가" 다. 로컬 DB 의 집계로는 절대 알 수 없는 값이라
// (한도는 벤더가 정하고 창은 벤더가 굴린다) 벤더에게 직접 물어보는 길밖에 없다.
//
// # 읽기 전용이라는 말의 범위
//
//   - 자격증명 파일을 **열되 쓰지 않는다.** 토큰 갱신도 하지 않는다. 만료되면 그냥
//     unavailable 로 두고 벤더 CLI 가 갱신해 주기를 기다린다. 우리가 refresh_token 을
//     쓰기 시작하면 벤더 CLI 와 갱신 경합이 나고, 우리 쪽 실수 한 번이 사용자의 로그인
//     세션을 통째로 날린다. 그 위험을 지불할 이유가 없다.
//   - 토큰은 **프로세스 메모리에만** 산다. SQLite·로그·오류 문자열·GUI 응답 어디에도
//     남기지 않는다. 그 규칙을 규율이 아니라 타입으로 지키는 것이 Token 이다 (token.go).
//
// # 왜 결과가 벤더별인가
//
// 한 벤더가 실패했다고 다른 벤더의 숫자까지 못 보게 되면 화면이 쓸모없어진다. 자격증명
// 파일이 없는 상태(로그인 안 함)는 정상 상태이지 오류가 아니고, 그런 사용자가 훨씬 많다.
// 그래서 Collect 는 error 를 반환하지 않고 **벤더마다 State 와 Reason 을 담은 Result** 를
// 돌려준다. 호출자가 분기할 수 있게 Reason 은 기계 판독 가능한 상수다.
//
// # 지원 범위
//
// Claude Code 와 Codex 뿐이다. Gemini CLI·Cursor 는 이 티켓의 범위 밖이다.
package vendorlimit

import (
	"context"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/your-org/pulsemetry/internal/hostenv"
)

// Vendor 는 조회 대상 도구다. 값은 로컬 파이프라인의 vendor 표기와 같아야 한다
// (internal/otlpdecode/attrs.go 의 정규화 결과) — 화면이 두 데이터를 같은 키로 잇는다.
type Vendor string

const (
	VendorClaudeCode Vendor = "claude_code"
	VendorCodex      Vendor = "codex"
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

// Result 는 vendor 를 찾아 준다. 화면·CLI 가 슬라이스를 매번 훑지 않게 한다.
func (s Snapshot) Result(v Vendor) (Result, bool) {
	for _, r := range s.Results {
		if r.Vendor == v {
			return r, true
		}
	}
	return Result{}, false
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

// Options 는 Collect 의 구성이다. 영값이 곧 운영 기본값이라 호출자가 아무것도 채우지
// 않아도 동작한다.
type Options struct {
	// HomeDir 는 자격증명 파일을 찾을 홈 디렉터리다. 비어 있으면 hostenv 로 판별한다.
	HomeDir string
	// Client 는 조회에 쓸 HTTP 클라이언트다. 비어 있으면 타임아웃이 걸린 기본 클라이언트다.
	// **타임아웃 없는 클라이언트를 넘기지 않는다** — 조회 하나가 호출자를 영원히 잡는다.
	Client *http.Client
	// Vendors 는 조회 대상이다. 비어 있으면 SupportedVendors 전부다.
	// 지원하지 않는 벤더(Gemini CLI·Cursor)는 조용히 건너뛴다.
	Vendors []Vendor

	// now 는 테스트가 시각을 고정하기 위한 자리다.
	now func() time.Time
	// baseURLs 는 테스트가 모의 서버를 가리키기 위한 자리다. 비공개 필드라 패키지 밖에서는
	// 채울 수 없다 — 운영 코드가 조회 대상을 바꿔치기할 길을 열어 두지 않는다.
	baseURLs map[Vendor]string
}

// Collect 는 벤더별 사용 한도를 모아 온다.
//
// **error 를 반환하지 않는다.** 실패는 벤더마다 State·Reason 으로 표현된다 — 자격증명이
// 없는 벤더 하나 때문에 다른 벤더의 숫자까지 화면에서 사라지면 안 되고, 애초에 "로그인하지
// 않음" 은 오류가 아니라 정상 상태다.
//
// 벤더별 조회는 서로 독립적인 네트워크 호출이므로 동시에 던진다. 결과 순서는 요청 순서를
// 유지한다 — 화면 카드가 매 조회마다 자리를 바꾸면 안 된다.
func Collect(ctx context.Context, opts Options) Snapshot {
	now := opts.now
	if now == nil {
		now = time.Now
	}
	observed := now()

	adapters := adaptersFor(opts.Vendors, opts.baseURLs)
	snap := Snapshot{Results: make([]Result, len(adapters)), ObservedAt: formatTime(observed)}

	home, err := resolveHome(opts.HomeDir)
	if err != nil {
		// 홈을 모르면 어느 벤더의 자격증명도 찾을 수 없다. 그래도 모양은 유지한다.
		for i, a := range adapters {
			snap.Results[i] = unavailable(a.vendor(), ReasonInternal, "홈 디렉터리를 판별하지 못했다", observed)
		}
		return snap
	}

	client := opts.Client
	if client == nil {
		client = newHTTPClient()
	}
	env := probeEnv{home: home, client: client, now: now}

	var wg sync.WaitGroup
	for i, a := range adapters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap.Results[i] = safeProbe(ctx, a, env)
		}()
	}
	wg.Wait()
	return snap
}

// adaptersFor 는 요청된 벤더의 어댑터를 SupportedVendors 순서로 고른다.
func adaptersFor(want []Vendor, baseURLs map[Vendor]string) []adapter {
	all := []adapter{
		claudeAdapter{baseURL: baseURLs[VendorClaudeCode]},
		codexAdapter{baseURL: baseURLs[VendorCodex]},
	}
	if len(want) == 0 {
		return all
	}
	requested := make(map[Vendor]struct{}, len(want))
	for _, v := range want {
		requested[v] = struct{}{}
	}
	out := make([]adapter, 0, len(all))
	for _, a := range all {
		if _, ok := requested[a.vendor()]; ok {
			out = append(out, a)
		}
	}
	return out
}

// resolveHome 은 자격증명 파일을 찾을 홈 디렉터리를 정한다.
func resolveHome(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	env, err := hostenv.Detect()
	if err != nil {
		return "", err
	}
	return env.HomeDir, nil
}

// safeProbe 는 어댑터의 패닉이 다른 벤더의 결과까지 지우지 않게 막는다.
//
// 어댑터는 남의 JSON 을 다루므로 우리가 예상하지 못한 모양에서 깨질 수 있고, 고루틴 안의
// 패닉은 **프로세스 전체를 죽인다.** 티켓이 요구하는 "해당 벤더만 unavailable" 을 지키려면
// 이 그물이 필요하다.
//
// 패닉 값을 Detail 에 싣지 않는다. 무엇이 담겨 있는지 보장할 수 없고, 토큰을 다루던
// 자리에서 난 패닉이면 그 값이 곧 토큰일 수 있다.
func safeProbe(ctx context.Context, a adapter, env probeEnv) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			res = unavailable(a.vendor(), ReasonInternal, "조회 중 예기치 않게 실패했다", env.now())
		}
	}()
	return a.probe(ctx, env)
}
