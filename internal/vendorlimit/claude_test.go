package vendorlimit

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// claudeUsageBody 는 관측된 모양의 정상 응답이다.
func claudeUsageBody(resetsAt string) string {
	return fmt.Sprintf(`{
  "five_hour":      {"utilization": 45.2, "resets_at": %q},
  "seven_day":      {"utilization": 12.0, "resets_at": %q},
  "seven_day_opus": {"utilization": 3.5,  "resets_at": %q},
  "account":        {"subscription_type": "max"},
  "extra_usage":    {"enabled": true, "utilization": 10.0},
  "unknown_future_field": {"whatever": 1}
}`, resetsAt, resetsAt, resetsAt)
}

// probeEnvFor 는 어댑터 하나를 돌릴 환경을 만든다.
func probeEnvFor(home string) probeEnv {
	return probeEnv{home: home, client: newHTTPClient(), now: fixedNow}
}

func TestClaudeAdapter정상응답을공통모델로옮긴다(t *testing.T) {
	t.Parallel()
	resetsAt := testNow.Add(90 * time.Minute).Format(time.RFC3339)
	up := jsonUpstream(t, claudeUsageBody(resetsAt))

	home := newHome(t)
	writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, testNow.Add(time.Hour), "max"))

	got := claudeAdapter{baseURL: up.srv.URL}.probe(context.Background(), probeEnvFor(home))

	if got.State != StateAvailable {
		t.Fatalf("state = %q, reason = %q, detail = %q", got.State, got.Reason, got.Detail)
	}
	if got.Vendor != VendorClaudeCode || got.Plan != "max" {
		t.Errorf("vendor·plan 이 다르다: %+v", got)
	}
	if got.ObservedAt != "2026-08-28T03:04:05Z" {
		t.Errorf("observed_at = %q", got.ObservedAt)
	}

	// 요청이 실제로 우리가 문서화한 모양으로 나갔는지 본다.
	if up.lastPath != claudeUsagePath {
		t.Errorf("경로 = %q, want %q", up.lastPath, claudeUsagePath)
	}
	if up.lastAuth != "Bearer "+claudeCanary {
		t.Errorf("Authorization 헤더 = %q", up.lastAuth)
	}
	if up.lastHeaders.Get("anthropic-beta") != claudeOAuthBeta {
		t.Errorf("anthropic-beta = %q", up.lastHeaders.Get("anthropic-beta"))
	}

	if len(got.Windows) != 3 {
		t.Fatalf("창 = %d개: %+v", len(got.Windows), got.Windows)
	}
	five := got.Windows[0]
	if five.Period != PeriodFiveHour || five.Label != "five_hour" || five.WindowMinutes != 300 {
		t.Errorf("5시간 창이 다르다: %+v", five)
	}
	if five.UsedRatio != 0.452 {
		t.Errorf("used_ratio = %v, want 0.452 (퍼센트를 비율로 옮겼는가)", five.UsedRatio)
	}
	if five.ResetsAt != resetsAt {
		t.Errorf("resets_at = %q, want %q", five.ResetsAt, resetsAt)
	}
	// 절대 시각만 준 벤더의 응답에서 남은 초가 파생돼야 한다.
	if five.ResetsInSeconds != 5400 {
		t.Errorf("resets_in_seconds = %d, want 5400", five.ResetsInSeconds)
	}
	if got.Windows[1].Period != PeriodWeekly || got.Windows[2].Label != "seven_day_opus" {
		t.Errorf("주간 창이 다르다: %+v", got.Windows[1:])
	}
	if !got.Extra.Supported || !got.Extra.Enabled || got.Extra.UsedRatio != 0.1 {
		t.Errorf("추가 한도가 다르다: %+v", got.Extra)
	}

	assertSerializable(t, got, claudeCanary)
}

func TestClaudeAdapter실패경로는벤더만unavailable로만든다(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// setup 은 홈 디렉터리를 꾸미고 상위 서버 주소를 돌려준다. 빈 문자열이면 정상 서버를 쓴다.
		setup      func(t *testing.T, home string) string
		wantReason Reason
	}{
		{
			name:       "자격증명 파일 없음",
			setup:      func(*testing.T, string) string { return "" },
			wantReason: ReasonCredentialMissing,
		},
		{
			name: "권한 오류",
			setup: func(t *testing.T, home string) string {
				writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, testNow.Add(time.Hour), "max"))
				makeUnreadable(t, claudeCredentialPath(home))
				return ""
			},
			wantReason: ReasonCredentialUnreadable,
		},
		{
			name: "자격증명 형식 변경",
			setup: func(t *testing.T, home string) string {
				writeClaudeCredential(t, home, `{"oauth":{"token":"`+claudeCanary+`"}}`)
				return ""
			},
			wantReason: ReasonCredentialMalformed,
		},
		{
			name: "파일의 만료 시각이 지났으면 호출조차 하지 않는다",
			setup: func(t *testing.T, home string) string {
				writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, testNow.Add(-time.Minute), "max"))
				return deadUpstream(t) // 호출하면 네트워크 오류가 되므로 구분된다
			},
			wantReason: ReasonTokenExpired,
		},
		{
			name: "상위가 401 이면 만료",
			setup: func(t *testing.T, home string) string {
				writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, time.Time{}, "max"))
				return statusUpstream(t, http.StatusUnauthorized).srv.URL
			},
			wantReason: ReasonTokenExpired,
		},
		{
			name: "상위가 500 이면 상위 상태",
			setup: func(t *testing.T, home string) string {
				writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, time.Time{}, "max"))
				return statusUpstream(t, http.StatusInternalServerError).srv.URL
			},
			wantReason: ReasonUpstreamStatus,
		},
		{
			name: "네트워크 장애",
			setup: func(t *testing.T, home string) string {
				writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, time.Time{}, "max"))
				return deadUpstream(t)
			},
			wantReason: ReasonNetwork,
		},
		{
			name: "본문이 JSON 이 아니다",
			setup: func(t *testing.T, home string) string {
				writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, time.Time{}, "max"))
				return jsonUpstream(t, `<html>maintenance</html>`).srv.URL
			},
			wantReason: ReasonResponseUnrecognized,
		},
		{
			name: "API 가 바뀌어 아는 창이 하나도 없다",
			setup: func(t *testing.T, home string) string {
				writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, time.Time{}, "max"))
				return jsonUpstream(t, `{"limits":[{"name":"5h","pct":40}]}`).srv.URL
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
				base = jsonUpstream(t, claudeUsageBody(testNow.Format(time.RFC3339))).srv.URL
			}

			got := claudeAdapter{baseURL: base}.probe(context.Background(), probeEnvFor(home))

			if got.State != StateUnavailable {
				t.Fatalf("state = %q, want unavailable: %+v", got.State, got)
			}
			if got.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q (detail=%q)", got.Reason, tc.wantReason, got.Detail)
			}
			if got.Vendor != VendorClaudeCode {
				t.Errorf("vendor = %q", got.Vendor)
			}
			if got.Windows == nil {
				t.Error("Windows 가 nil — JSON 에서 null 이 된다")
			}
			// 실패 경로야말로 토큰이 새는 자리다. 결과 전체와 직렬화 결과 양쪽을 본다.
			assertNoSecret(t, "실패 결과", allStrings(got), claudeCanary, home)
			assertSerializable(t, got, claudeCanary, home)
		})
	}
}

func TestResolveResetTimes(t *testing.T) {
	t.Parallel()
	now := testNow
	tests := []struct {
		name        string
		in          Window
		wantAt      string
		wantIn      int64
		wantAtBlank bool
	}{
		{
			name:   "절대 시각만 주면 남은 초를 파생한다",
			in:     Window{ResetsAt: now.Add(time.Hour).Format(time.RFC3339)},
			wantAt: "2026-08-28T04:04:05Z", wantIn: 3600,
		},
		{
			name:   "남은 초만 주면 절대 시각을 파생한다",
			in:     Window{ResetsInSeconds: 3600},
			wantAt: "2026-08-28T04:04:05Z", wantIn: 3600,
		},
		{
			name:   "둘 다 주면 벤더 값을 덮어쓰지 않는다",
			in:     Window{ResetsAt: now.Add(time.Hour).Format(time.RFC3339), ResetsInSeconds: 60},
			wantAt: "2026-08-28T04:04:05Z", wantIn: 60,
		},
		{
			name:   "다른 시간대 표기는 UTC 로 통일한다",
			in:     Window{ResetsAt: "2026-08-28T13:04:05+09:00"},
			wantAt: "2026-08-28T04:04:05Z", wantIn: 3600,
		},
		{
			name:        "읽을 수 없는 시각은 버린다",
			in:          Window{ResetsAt: "다음 주 화요일쯤"},
			wantAtBlank: true,
		},
		{
			name:   "이미 지난 시각이면 남은 초는 0 이다",
			in:     Window{ResetsAt: now.Add(-time.Hour).Format(time.RFC3339)},
			wantAt: "2026-08-28T02:04:05Z", wantIn: 0,
		},
		{
			name:        "둘 다 없으면 그대로 비어 있다",
			in:          Window{},
			wantAtBlank: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := tc.in
			resolveResetTimes(&w, now)
			if tc.wantAtBlank {
				if w.ResetsAt != "" {
					t.Errorf("resets_at = %q, want 빈 문자열", w.ResetsAt)
				}
				return
			}
			if w.ResetsAt != tc.wantAt {
				t.Errorf("resets_at = %q, want %q", w.ResetsAt, tc.wantAt)
			}
			if w.ResetsInSeconds != tc.wantIn {
				t.Errorf("resets_in_seconds = %d, want %d", w.ResetsInSeconds, tc.wantIn)
			}
		})
	}
}
