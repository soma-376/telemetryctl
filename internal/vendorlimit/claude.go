package vendorlimit

import (
	"context"
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
//	  "five_hour":       {"utilization": 45.2, "resets_at": "2026-08-28T08:00:00Z"},
//	  "seven_day":       {"utilization": 12.0, "resets_at": "..."},
//	  "seven_day_opus":  {"utilization":  3.5, "resets_at": "..."},
//	  "account":         {"subscription_type": "max"},
//	  "extra_usage":     {"enabled": true, "utilization": 10.0}
//	}
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

// claudeUsageResponse 는 응답의 관측된 모양이다. 모르는 필드는 무시한다 — 벤더가 필드를
// 추가했다고 화면이 죽으면 안 된다.
type claudeUsageResponse struct {
	FiveHour     *claudeUsageWindow `json:"five_hour"`
	SevenDay     *claudeUsageWindow `json:"seven_day"`
	SevenDayOpus *claudeUsageWindow `json:"seven_day_opus"`
	Account      *struct {
		SubscriptionType string `json:"subscription_type"`
	} `json:"account"`
	ExtraUsage *struct {
		Enabled     bool    `json:"enabled"`
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

// windows 는 응답의 창들을 공통 모델로 옮긴다. 없는 창은 조용히 건너뛴다 —
// 플랜에 따라 seven_day_opus 가 아예 없는 계정이 있다.
func (r claudeUsageResponse) windows(now time.Time) []Window {
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
		Enabled:   r.ExtraUsage.Enabled,
		UsedRatio: ratioFromPercent(r.ExtraUsage.Utilization),
	}
}
