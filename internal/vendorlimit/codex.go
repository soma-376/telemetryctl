package vendorlimit

import (
	"context"
	"time"
)

// # 비공개 API 가정 — Codex CLI
//
// **이 블록이 Codex 쪽 가정의 유일한 보관소다.** 바깥 어디에도 같은 지식을 두지 않는다.
//
// Codex 는 Claude 와 달리 창을 이름이 아니라 **길이(분)** 로 알려주고, 초기화도 절대 시각이
// 아니라 남은 초로 준다. 그래서 정규화가 기계적이다 — periodFromMinutes 가 창 길이를
// 종류로 옮기고 resolveResetTimes 가 절대 시각을 파생한다.
//
//	GET https://chatgpt.com/backend-api/codex/usage
//	Authorization: Bearer <tokens.access_token>
//	chatgpt-account-id: <tokens.account_id>
//
//	200 {
//	  "rate_limits": {
//	    "primary":   {"used_percent": 12.5, "window_minutes": 300,   "resets_in_seconds": 3600},
//	    "secondary": {"used_percent": 40.0, "window_minutes": 10080, "resets_in_seconds": 86400}
//	  },
//	  "plan_type": "plus",
//	  "credits": {"enabled": true, "used_percent": 5.0}
//	}
//
// # 사람이 확인하는 방법
//
//  1. 로그인된 장비에서 `codex` 를 띄우고 `/status` 의 사용량 표시를 확인한다.
//  2. Codex CLI 를 디버그 로그(`RUST_LOG=debug codex`)로 돌려 실제로 어느 호스트·경로에
//     사용량을 묻는지, 응답의 `rate_limits` 키가 어떤 모양인지 본다.
//  3. 다르면 경로·필드명을 이 주석과 codexUsageResponse 에 반영한다.
//
// 확실하지 않은 것 (Claude 쪽보다 확신이 낮다):
//   - **전용 usage 엔드포인트의 존재.** Codex 는 사용 한도를 별도 조회 없이 응답
//     페이로드에 얹어 주는 방식이었던 시기가 있다. 그렇다면 읽기 전용 조회로는 얻을 수
//     없고, 이 어댑터는 404 를 받아 ReasonUpstreamStatus 로 떨어진다 — 잘못된 숫자를
//     보여주지는 않는다. 위 2번이 이 가정을 가장 먼저 검증한다.
//   - `used_percent` 가 퍼센트(0~100)라는 가정.
//   - `plan_type`·`credits` 필드명. 없으면 플랜은 빈 문자열, 추가 한도는 Supported=false 다.
const (
	codexAPIBase   = "https://chatgpt.com"
	codexUsagePath = "/backend-api/codex/usage"
	// codexAccountHeader 는 계정 다중 소속 사용자를 위해 어느 계정으로 묻는지 지정한다.
	codexAccountHeader = "chatgpt-account-id"
)

type codexAdapter struct {
	// baseURL 은 테스트에서만 채운다 (claudeAdapter 와 같은 규칙).
	baseURL string
}

func (codexAdapter) vendor() Vendor { return VendorCodex }

func (a codexAdapter) usageURL() string {
	if a.baseURL != "" {
		return a.baseURL + codexUsagePath
	}
	return codexAPIBase + codexUsagePath
}

// codexRateLimit 은 창 하나의 관측된 모양이다.
type codexRateLimit struct {
	UsedPercent     float64 `json:"used_percent"`
	WindowMinutes   int     `json:"window_minutes"`
	ResetsInSeconds int64   `json:"resets_in_seconds"`
}

type codexUsageResponse struct {
	RateLimits *struct {
		Primary   *codexRateLimit `json:"primary"`
		Secondary *codexRateLimit `json:"secondary"`
	} `json:"rate_limits"`
	PlanType string `json:"plan_type"`
	Credits  *struct {
		Enabled     bool    `json:"enabled"`
		UsedPercent float64 `json:"used_percent"`
	} `json:"credits"`
}

func (a codexAdapter) probe(ctx context.Context, env probeEnv) Result {
	now := env.now()

	cred, err := loadCodexCredential(env.home)
	if err != nil {
		return unavailable(VendorCodex, reasonOf(err, ReasonCredentialUnreadable), err.Error(), now)
	}
	// Codex 자격증명에는 우리가 믿고 읽을 만료 시각이 없다 (credentials.go 참고).
	// 만료는 상위의 401 로만 안다.

	var resp codexUsageResponse
	headers := map[string]string{codexAccountHeader: cred.accountID}
	if err := getJSON(ctx, env.client, a.usageURL(), cred.token, headers, &resp); err != nil {
		return unavailable(VendorCodex, transportReason(err), err.Error(), now)
	}

	windows := resp.windows(now)
	if len(windows) == 0 {
		return unavailable(VendorCodex, ReasonResponseUnrecognized,
			"응답에서 사용 한도 창을 찾지 못했다 — 벤더 API 가 바뀌었을 수 있다", now)
	}

	return Result{
		Vendor:     VendorCodex,
		State:      StateAvailable,
		Plan:       resp.PlanType,
		Windows:    windows,
		Extra:      resp.extra(),
		ObservedAt: formatTime(now),
	}
}

func (r codexUsageResponse) windows(now time.Time) []Window {
	out := []Window{}
	if r.RateLimits == nil {
		return out
	}
	add := func(label string, rl *codexRateLimit) {
		if rl == nil {
			return
		}
		win := Window{
			Period:          periodFromMinutes(rl.WindowMinutes),
			Label:           label,
			WindowMinutes:   rl.WindowMinutes,
			UsedRatio:       ratioFromPercent(rl.UsedPercent),
			ResetsInSeconds: rl.ResetsInSeconds,
		}
		resolveResetTimes(&win, now)
		out = append(out, win)
	}
	add("primary", r.RateLimits.Primary)
	add("secondary", r.RateLimits.Secondary)
	return out
}

func (r codexUsageResponse) extra() ExtraAllowance {
	if r.Credits == nil {
		return ExtraAllowance{}
	}
	return ExtraAllowance{
		Supported: true,
		Enabled:   r.Credits.Enabled,
		UsedRatio: ratioFromPercent(r.Credits.UsedPercent),
	}
}
