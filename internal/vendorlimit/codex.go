package vendorlimit

import (
	"context"
	"errors"
	"time"

	"github.com/your-org/pulsemetry/internal/codexapp"
)

// codexAdapter 는 Codex App Server 결과를 화면 공통 모델로 옮긴다.
// 인증, 토큰 갱신과 상위 HTTP 요청은 Codex 프로세스가 소유한다 (ADR 0011).
type codexAdapter struct{}

func (codexAdapter) vendor() Vendor { return VendorCodex }

func (codexAdapter) probe(ctx context.Context, env probeEnv) Result {
	now := env.now()
	if env.codex == nil {
		return unavailable(VendorCodex, ReasonInternal, "Codex App Server 클라이언트가 없다", now)
	}
	snapshot, err := env.codex.RateLimits(ctx)
	if err != nil {
		return unavailable(VendorCodex, codexReason(err), "Codex App Server에서 사용 한도를 읽지 못했다", now)
	}
	windows := codexWindows(snapshot, now)
	if len(windows) == 0 {
		return unavailable(VendorCodex, ReasonResponseUnrecognized,
			"응답에서 사용 한도 창을 찾지 못했다 — Codex App Server가 바뀌었을 수 있다", now)
	}
	plan := ""
	if snapshot.PlanType != nil {
		plan = *snapshot.PlanType
	}
	return Result{
		Vendor: VendorCodex, State: StateAvailable, Plan: plan,
		Windows: windows, Extra: codexExtra(snapshot), ObservedAt: formatTime(now),
	}
}

func codexReason(err error) Reason {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ReasonNetwork
	case errors.Is(err, codexapp.ErrProtocol):
		return ReasonResponseUnrecognized
	default:
		return ReasonInternal
	}
}

func codexWindows(snapshot codexapp.RateLimitSnapshot, now time.Time) []Window {
	out := []Window{}
	add := func(label string, source *codexapp.RateLimitWindow) {
		if source == nil {
			return
		}
		minutes := 0
		if source.WindowDurationMins != nil {
			minutes = int(*source.WindowDurationMins)
		}
		win := Window{
			Period: periodFromMinutes(minutes), Label: label, WindowMinutes: minutes,
			UsedRatio: ratioFromPercent(float64(source.UsedPercent)),
		}
		if source.ResetsAt != nil {
			reset := time.Unix(*source.ResetsAt, 0).UTC()
			win.ResetsAt = formatTime(reset)
			if remain := int64(reset.Sub(now).Seconds()); remain > 0 {
				win.ResetsInSeconds = remain
			}
		}
		out = append(out, win)
	}
	add("primary", snapshot.Primary)
	add("secondary", snapshot.Secondary)
	return out
}

func codexExtra(snapshot codexapp.RateLimitSnapshot) ExtraAllowance {
	if snapshot.Credits == nil {
		return ExtraAllowance{}
	}
	return ExtraAllowance{Supported: true, Enabled: snapshot.Credits.HasCredits || snapshot.Credits.Unlimited}
}
