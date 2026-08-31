package vendorlimit

import (
	"context"
	"strings"
	"time"
)

// # 비공개 API 가정 — Claude Code
//
// **이 블록이 Claude 쪽 가정의 유일한 보관소다.** 엔드포인트·헤더·응답 필드명이 바뀌면
// 이 파일만 고치면 되도록, 바깥 어디에도 같은 지식을 두지 않는다.
//
// 아래는 공개 문서가 아니라 **Claude Code 클라이언트가 실제로 하는 호출을 관측한 모양**이다.
// 언제든 예고 없이 바뀔 수 있고, 바뀌면 이 어댑터는 ReasonResponseUnrecognized 로 떨어진다 —
// 조용히 틀린 숫자를 보여주는 것보다 "지금 모른다" 고 말하는 편이 맞다.
//
//	GET https://api.anthropic.com/api/oauth/usage
//	Authorization: Bearer <claudeAiOauth.accessToken>
//	anthropic-beta: oauth-2025-04-20
//
//	200 {
//	  "limits": [
//	    {"kind": "session",       "percent":  5, "resets_at": "...", "scope": null},
//	    {"kind": "weekly_all",    "percent": 30, "resets_at": "...", "scope": null},
//	    {"kind": "weekly_scoped", "percent": 24, "resets_at": "...",
//	     "scope": {"model": {"display_name": "Fable"}}}
//	  ],
//	  "five_hour":  {"utilization":  5, "resets_at": "..."},
//	  "seven_day":  {"utilization": 30, "resets_at": "..."},
//	  "extra_usage": {"is_enabled": false, "utilization": null}
//	}
//
// # 모델별 한도는 limits 배열에 있다
//
// 예전에는 모델마다 최상위 필드가 있었다 (seven_day_opus). 모델이 늘면서 벤더가 배열로
// 바꿨고, 그 필드들은 지금 **null 로 남아 있다.** 이름으로 집으면 새 모델이 나올 때마다
// 이 파일을 고쳐야 하므로 배열을 먼저 읽는다 — Fable 이든 그다음이든 코드 변경 없이 붙는다.
//
// 최상위 five_hour·seven_day 는 배열과 같은 값을 중복해서 준다. 둘 다 넣으면 창이 두 번
// 세어지므로 **배열이 있으면 배열만** 쓰고, 없을 때만 옛 필드로 돌아간다.
//
// account 필드는 사라졌다. 플랜 이름은 자격증명 파일의 subscriptionType 으로 넘어간다.
//
// # 사람이 확인하는 방법
//
//  1. 로그인된 장비에서 Claude Code 를 `/usage` 로 실행해 화면 숫자를 확인한다.
//  2. 같은 장비에서 위 요청을 직접 던져(`curl -H "Authorization: Bearer $(jq -r
//     '.claudeAiOauth.accessToken' ~/.claude/.credentials.json)" -H 'anthropic-beta:
//     oauth-2025-04-20' https://api.anthropic.com/api/oauth/usage`) 두 숫자가 같은지 본다.
//  3. 다르면 실제 경로·필드명을 이 주석과 claudeUsageResponse 에 반영한다.
//
// 확실하지 않은 것: `utilization` 이 퍼센트(0~100)라고 가정했다. 비율(0~1)이라면 화면
// 숫자가 100배 작게 나온다 — 위 2번에서 가장 먼저 확인할 값이다.
// `extra_usage` 는 추가 한도를 켠 계정에서만 나타나는 것으로 보인다.
const (
	claudeAPIBase   = "https://api.anthropic.com"
	claudeUsagePath = "/api/oauth/usage"
	// claudeOAuthBeta 는 OAuth 토큰으로 API 를 호출할 때 요구되는 베타 플래그다.
	claudeOAuthBeta = "oauth-2025-04-20"
)

type claudeAdapter struct {
	// baseURL 은 테스트에서만 채운다. 비어 있으면 실제 엔드포인트다 —
	// 테스트가 이 값을 채우지 않으면 서버에 닿지 못해 즉시 실패하므로, 실수로
	// 실제 API 를 때리는 테스트가 조용히 통과하는 일은 없다.
	baseURL string
}

func (claudeAdapter) vendor() Vendor { return VendorClaudeCode }

func (a claudeAdapter) usageURL() string {
	if a.baseURL != "" {
		return a.baseURL + claudeUsagePath
	}
	return claudeAPIBase + claudeUsagePath
}

// claudeUsageWindow 는 창 하나의 관측된 모양이다.
type claudeUsageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

// claudeLimit 은 limits 배열의 한 항목이다. 모델별 한도가 여기로 온다.
type claudeLimit struct {
	// Kind 는 창의 종류다: session | weekly_all | weekly_scoped.
	Kind    string  `json:"kind"`
	Percent float64 `json:"percent"`
	// ResetsAt 은 소수점 초가 붙은 RFC3339 다. time.Parse 가 그대로 받는다.
	ResetsAt string `json:"resets_at"`
	// Scope 는 weekly_scoped 에서만 채워진다. 어느 모델의 한도인지가 여기 있다.
	Scope *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

// modelName 은 이 한도가 걸린 모델 이름이다. 모르면 빈 문자열이다.
func (l claudeLimit) modelName() string {
	if l.Scope == nil || l.Scope.Model == nil {
		return ""
	}
	return strings.TrimSpace(l.Scope.Model.DisplayName)
}

// claudeUsageResponse 는 응답의 관측된 모양이다. 모르는 필드는 무시한다 — 벤더가 필드를
// 추가했다고 화면이 죽으면 안 된다.
type claudeUsageResponse struct {
	// Limits 가 있으면 이것이 정본이다. 아래 세 필드는 폴백이다.
	Limits []claudeLimit `json:"limits"`

	FiveHour     *claudeUsageWindow `json:"five_hour"`
	SevenDay     *claudeUsageWindow `json:"seven_day"`
	SevenDayOpus *claudeUsageWindow `json:"seven_day_opus"`

	// Account 는 지금 응답에서 사라졌다. 남겨 두는 이유는 되돌아와도 그대로 읽기 위해서다.
	Account *struct {
		SubscriptionType string `json:"subscription_type"`
	} `json:"account"`
	ExtraUsage *struct {
		// Enabled 는 옛 이름, IsEnabled 는 지금 이름이다. 둘 중 하나만 오므로 OR 로 읽는다.
		Enabled     bool    `json:"enabled"`
		IsEnabled   bool    `json:"is_enabled"`
		Utilization float64 `json:"utilization"`
	} `json:"extra_usage"`
}

func (a claudeAdapter) probe(ctx context.Context, env probeEnv) Result {
	now := env.now()

	cred, err := loadClaudeCredential(env.home)
	if err != nil {
		return unavailable(VendorClaudeCode, reasonOf(err, ReasonCredentialUnreadable), err.Error(), now)
	}
	// 만료를 미리 아는 경우에는 호출하지 않는다. 어차피 401 로 돌아올 요청을 보내 봐야
	// 벤더 쪽 실패 카운터만 올린다. **여기서도 갱신하지 않는다** — 벤더 CLI 를 기다린다.
	if !cred.expiresAt.IsZero() && !cred.expiresAt.After(now) {
		return unavailable(VendorClaudeCode, ReasonTokenExpired,
			"액세스 토큰이 만료됐다 — Claude Code 가 갱신하면 다시 보인다", now)
	}

	var resp claudeUsageResponse
	headers := map[string]string{"anthropic-beta": claudeOAuthBeta}
	if err := getJSON(ctx, env.client, a.usageURL(), cred.token, headers, &resp); err != nil {
		return unavailable(VendorClaudeCode, transportReason(err), err.Error(), now)
	}

	windows := resp.windows(now)
	if len(windows) == 0 {
		// 2xx 를 받았는데 아는 창이 하나도 없다 — 응답 모양이 바뀌었다는 뜻이다.
		return unavailable(VendorClaudeCode, ReasonResponseUnrecognized,
			"응답에서 사용 한도 창을 찾지 못했다 — 벤더 API 가 바뀌었을 수 있다", now)
	}

	return Result{
		Vendor:     VendorClaudeCode,
		State:      StateAvailable,
		Plan:       resp.plan(cred.plan),
		Windows:    windows,
		Extra:      resp.extra(),
		ObservedAt: formatTime(now),
	}
}

// windows 는 응답의 창들을 공통 모델로 옮긴다.
//
// limits 배열이 있으면 그것만 쓴다. 최상위 five_hour·seven_day 가 같은 창을 중복해서
// 주므로 둘 다 넣으면 화면에 같은 한도가 두 줄로 나온다.
func (r claudeUsageResponse) windows(now time.Time) []Window {
	if out := r.limitWindows(now); len(out) > 0 {
		return out
	}
	return r.legacyWindows(now)
}

// limitWindows 는 limits 배열을 창으로 옮긴다.
//
// 모르는 kind 도 버리지 않고 PeriodUnknown 으로 넘긴다 — 벤더가 창을 추가했을 때 숫자가
// 조용히 사라지는 것보다 "모르는 창" 으로 보이는 편이 낫다 (PeriodUnknown 주석).
func (r claudeUsageResponse) limitWindows(now time.Time) []Window {
	out := []Window{}
	for _, l := range r.Limits {
		// kind 가 없으면 우리가 아는 모양이 아니다. 이런 항목까지 창으로 세면 응답이
		// 통째로 바뀌었을 때도 available 로 떨어져, 화면이 빈 막대를 진짜 한도로 그린다.
		if strings.TrimSpace(l.Kind) == "" {
			continue
		}
		period, label, minutes := claudeLimitShape(l)
		win := Window{
			Period:        period,
			Label:         label,
			WindowMinutes: minutes,
			UsedRatio:     ratioFromPercent(l.Percent),
			ResetsAt:      l.ResetsAt,
		}
		resolveResetTimes(&win, now)
		out = append(out, win)
	}
	return out
}

// claudeLimitShape 는 limits 항목 하나의 종류·이름·길이를 정한다.
//
// weekly_scoped 의 이름을 모델 이름으로 두는 것이 이 함수의 요점이다. 같은 PeriodWeekly
// 창이 둘 이상일 때 사람이 구분할 유일한 근거가 Label 이다 (Window.Label 주석).
func claudeLimitShape(l claudeLimit) (PeriodKind, string, int) {
	switch l.Kind {
	case "session":
		return PeriodFiveHour, "five_hour", 5 * 60
	case "weekly_all":
		return PeriodWeekly, "seven_day", 7 * 24 * 60
	case "weekly_scoped":
		if name := l.modelName(); name != "" {
			return PeriodWeekly, name, 7 * 24 * 60
		}
		return PeriodWeekly, l.Kind, 7 * 24 * 60
	default:
		// 길이를 0 으로 두는 것은 "모른다" 는 뜻이다 (Window.WindowMinutes).
		return PeriodUnknown, l.Kind, 0
	}
}

// legacyWindows 는 limits 배열이 없던 시절의 최상위 필드를 읽는다. 없는 창은 조용히
// 건너뛴다 — 플랜에 따라 seven_day_opus 가 아예 없는 계정이 있었다.
func (r claudeUsageResponse) legacyWindows(now time.Time) []Window {
	out := []Window{}
	add := func(label string, period PeriodKind, minutes int, w *claudeUsageWindow) {
		if w == nil {
			return
		}
		win := Window{
			Period:        period,
			Label:         label,
			WindowMinutes: minutes,
			UsedRatio:     ratioFromPercent(w.Utilization),
			ResetsAt:      w.ResetsAt,
		}
		resolveResetTimes(&win, now)
		out = append(out, win)
	}
	add("five_hour", PeriodFiveHour, 5*60, r.FiveHour)
	add("seven_day", PeriodWeekly, 7*24*60, r.SevenDay)
	add("seven_day_opus", PeriodWeekly, 7*24*60, r.SevenDayOpus)
	return out
}

// plan 은 응답의 플랜을 쓰되, 없으면 자격증명 파일의 subscriptionType 으로 대신한다.
// 둘 다 없으면 빈 문자열이다 — 모르는 값을 지어내지 않는다.
func (r claudeUsageResponse) plan(fallback string) string {
	if r.Account != nil && r.Account.SubscriptionType != "" {
		return r.Account.SubscriptionType
	}
	return fallback
}

func (r claudeUsageResponse) extra() ExtraAllowance {
	if r.ExtraUsage == nil {
		return ExtraAllowance{}
	}
	return ExtraAllowance{
		Supported: true,
		Enabled:   r.ExtraUsage.Enabled || r.ExtraUsage.IsEnabled,
		UsedRatio: ratioFromPercent(r.ExtraUsage.Utilization),
	}
}
