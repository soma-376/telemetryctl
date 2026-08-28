package vendorlimit

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// codexUsageBody 는 관측된 모양의 정상 응답이다.
const codexUsageBody = `{
  "rate_limits": {
    "primary":   {"used_percent": 12.5, "window_minutes": 300,   "resets_in_seconds": 3600},
    "secondary": {"used_percent": 40.0, "window_minutes": 10080, "resets_in_seconds": 86400}
  },
  "plan_type": "plus",
  "credits": {"enabled": true, "used_percent": 5.0},
  "unknown_future_field": [1, 2, 3]
}`

func TestCodexAdapter정상응답을공통모델로옮긴다(t *testing.T) {
	t.Parallel()
	up := jsonUpstream(t, codexUsageBody)

	home := newHome(t)
	writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))

	got := codexAdapter{baseURL: up.srv.URL}.probe(context.Background(), probeEnvFor(home))

	if got.State != StateAvailable {
		t.Fatalf("state = %q, reason = %q, detail = %q", got.State, got.Reason, got.Detail)
	}
	if got.Vendor != VendorCodex || got.Plan != "plus" {
		t.Errorf("vendor·plan 이 다르다: %+v", got)
	}
	if up.lastPath != codexUsagePath {
		t.Errorf("경로 = %q, want %q", up.lastPath, codexUsagePath)
	}
	if up.lastAuth != "Bearer "+codexCanary {
		t.Errorf("Authorization 헤더 = %q", up.lastAuth)
	}
	if got := up.lastHeaders.Get(codexAccountHeader); got != accountCanary {
		t.Errorf("%s = %q", codexAccountHeader, got)
	}

	if len(got.Windows) != 2 {
		t.Fatalf("창 = %d개: %+v", len(got.Windows), got.Windows)
	}
	primary, secondary := got.Windows[0], got.Windows[1]
	// 창 길이(분)가 종류로 옮겨졌는지 — Codex 정규화의 핵심이다.
	if primary.Period != PeriodFiveHour || primary.WindowMinutes != 300 || primary.Label != "primary" {
		t.Errorf("primary 창이 다르다: %+v", primary)
	}
	if primary.UsedRatio != 0.125 {
		t.Errorf("used_ratio = %v, want 0.125", primary.UsedRatio)
	}
	// 남은 초만 준 벤더의 응답에서 절대 시각이 파생돼야 한다.
	if primary.ResetsInSeconds != 3600 || primary.ResetsAt != "2026-08-28T04:04:05Z" {
		t.Errorf("초기화 시각이 다르다: %+v", primary)
	}
	if secondary.Period != PeriodWeekly || secondary.WindowMinutes != 10080 {
		t.Errorf("secondary 창이 다르다: %+v", secondary)
	}
	if !got.Extra.Supported || !got.Extra.Enabled || got.Extra.UsedRatio != 0.05 {
		t.Errorf("추가 한도가 다르다: %+v", got.Extra)
	}

	assertSerializable(t, got, codexCanary, accountCanary)
}

// 벤더가 알려주지 않는 값은 지어내지 않는다.
func TestCodexAdapter는모르는값을비워둔다(t *testing.T) {
	t.Parallel()
	up := jsonUpstream(t, `{"rate_limits":{"primary":{"used_percent":1,"window_minutes":43200}}}`)

	home := newHome(t)
	writeCodexAuth(t, home, `{"tokens":{"access_token":"`+codexCanary+`"}}`)

	got := codexAdapter{baseURL: up.srv.URL}.probe(context.Background(), probeEnvFor(home))

	if got.State != StateAvailable {
		t.Fatalf("state = %q (%q)", got.State, got.Detail)
	}
	if got.Plan != "" {
		t.Errorf("plan = %q — 모르는 플랜을 지어냈다", got.Plan)
	}
	if got.Extra.Supported {
		t.Errorf("추가 한도를 지어냈다: %+v", got.Extra)
	}
	if len(got.Windows) != 1 || got.Windows[0].Period != PeriodMonthly {
		t.Fatalf("창이 다르다: %+v", got.Windows)
	}
	if got.Windows[0].ResetsAt != "" || got.Windows[0].ResetsInSeconds != 0 {
		t.Errorf("초기화 시각을 지어냈다: %+v", got.Windows[0])
	}
	// account_id 가 없으면 헤더를 붙이지 않는다.
	if _, ok := up.lastHeaders[http.CanonicalHeaderKey(codexAccountHeader)]; ok {
		t.Error("빈 account_id 로 헤더가 붙었다")
	}
}

func TestCodexAdapter실패경로는벤더만unavailable로만든다(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		setup      func(t *testing.T, home string) string
		wantReason Reason
	}{
		{
			name:       "auth.json 없음",
			setup:      func(*testing.T, string) string { return "" },
			wantReason: ReasonCredentialMissing,
		},
		{
			name: "권한 오류",
			setup: func(t *testing.T, home string) string {
				writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))
				makeUnreadable(t, codexCredentialPath(home))
				return ""
			},
			wantReason: ReasonCredentialUnreadable,
		},
		{
			name: "auth.json 형식 변경",
			setup: func(t *testing.T, home string) string {
				writeCodexAuth(t, home, `{"auth":{"bearer":"`+codexCanary+`"}}`)
				return ""
			},
			wantReason: ReasonCredentialMalformed,
		},
		{
			name: "토큰 만료는 401 로만 안다",
			setup: func(t *testing.T, home string) string {
				writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))
				return statusUpstream(t, http.StatusUnauthorized).srv.URL
			},
			wantReason: ReasonTokenExpired,
		},
		{
			name: "엔드포인트가 사라지면 상위 상태",
			setup: func(t *testing.T, home string) string {
				writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))
				return statusUpstream(t, http.StatusNotFound).srv.URL
			},
			wantReason: ReasonUpstreamStatus,
		},
		{
			name: "네트워크 장애",
			setup: func(t *testing.T, home string) string {
				writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))
				return deadUpstream(t)
			},
			wantReason: ReasonNetwork,
		},
		{
			name: "rate_limits 가 사라졌다",
			setup: func(t *testing.T, home string) string {
				writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))
				return jsonUpstream(t, `{"usage":{"five_hour":10}}`).srv.URL
			},
			wantReason: ReasonResponseUnrecognized,
		},
		{
			name: "본문이 JSON 이 아니다",
			setup: func(t *testing.T, home string) string {
				writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))
				return jsonUpstream(t, `502 Bad Gateway`).srv.URL
			},
			wantReason: ReasonResponseUnrecognized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := newHome(t)
			base := tc.setup(t, home)
			if base == "" {
				base = jsonUpstream(t, codexUsageBody).srv.URL
			}

			got := codexAdapter{baseURL: base}.probe(context.Background(), probeEnvFor(home))

			if got.State != StateUnavailable {
				t.Fatalf("state = %q, want unavailable: %+v", got.State, got)
			}
			if got.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q (detail=%q)", got.Reason, tc.wantReason, got.Detail)
			}
			if got.Vendor != VendorCodex {
				t.Errorf("vendor = %q", got.Vendor)
			}
			if got.Windows == nil {
				t.Error("Windows 가 nil — JSON 에서 null 이 된다")
			}
			assertNoSecret(t, "실패 결과", allStrings(got), codexCanary, accountCanary, home)
			assertSerializable(t, got, codexCanary, accountCanary, home)
		})
	}
}

// 조회가 오래 걸려도 호출자의 컨텍스트가 끊기면 즉시 돌아온다.
func TestCodexAdapter는컨텍스트취소를따른다(t *testing.T) {
	t.Parallel()
	blocked := make(chan struct{})
	up := newUpstream(t, func(http.ResponseWriter, *http.Request) { <-blocked })
	t.Cleanup(func() { close(blocked) })

	home := newHome(t)
	writeCodexAuth(t, home, codexAuthJSON(codexCanary, accountCanary))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	got := codexAdapter{baseURL: up.srv.URL}.probe(ctx, probeEnvFor(home))
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("취소가 무시됐다: %v 걸렸다", elapsed)
	}
	if got.State != StateUnavailable || got.Reason != ReasonNetwork {
		t.Fatalf("state = %q, reason = %q", got.State, got.Reason)
	}
	assertNoSecret(t, "취소 결과", allStrings(got), codexCanary)
}
