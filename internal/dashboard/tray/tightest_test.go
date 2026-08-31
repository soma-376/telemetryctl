package tray

import (
	"testing"

	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// 이 파일이 **「가장 빠듯한 한도」 선택의 계약** 이다 (PROJ-96).
//
//   - unavailable 벤더는 후보가 되지 않는다.
//   - 사용률 → 초기화까지 남은 시간 → 벤더 → 창 종류 → Label 순의 동률 규칙이 전순서다.
//   - 같은 입력에 늘 같은 답을 낸다.

// ── 가장 빠듯한 한도 ────────────────────────────────────────────────────────

func TestTightestLimitIsDeterministic(t *testing.T) {
	cases := []struct {
		name       string
		results    []vendorlimit.Result
		wantFound  bool
		wantVendor string
		wantLabel  string
	}{
		{
			name:      "후보가 없으면 없음",
			results:   []vendorlimit.Result{},
			wantFound: false,
		},
		{
			name: "unavailable 벤더만 있으면 없음",
			results: []vendorlimit.Result{
				unavailableResult(vendorlimit.VendorClaudeCode, vendorlimit.ReasonCredentialMissing,
					window(vendorlimit.PeriodFiveHour, "five_hour", 0.99, 60)),
			},
			wantFound: false,
		},
		{
			name: "사용률이 높은 쪽이 이긴다",
			results: []vendorlimit.Result{
				availableResult(vendorlimit.VendorClaudeCode,
					window(vendorlimit.PeriodFiveHour, "five_hour", 0.42, 3600),
					window(vendorlimit.PeriodWeekly, "seven_day", 0.87, 500000)),
				availableResult(vendorlimit.VendorCodex,
					window(vendorlimit.PeriodFiveHour, "primary", 0.51, 600)),
			},
			wantFound: true, wantVendor: "claude_code", wantLabel: "seven_day",
		},
		{
			name: "사용률이 같으면 초기화가 빠른 쪽",
			results: []vendorlimit.Result{
				availableResult(vendorlimit.VendorClaudeCode,
					window(vendorlimit.PeriodWeekly, "seven_day", 0.9, 500000)),
				availableResult(vendorlimit.VendorCodex,
					window(vendorlimit.PeriodFiveHour, "primary", 0.9, 300)),
			},
			wantFound: true, wantVendor: "codex", wantLabel: "primary",
		},
		{
			name: "초기화 시각을 모르는 창은 아는 창에 진다",
			results: []vendorlimit.Result{
				availableResult(vendorlimit.VendorClaudeCode,
					window(vendorlimit.PeriodFiveHour, "unknown_reset", 0.9, 0)),
				availableResult(vendorlimit.VendorCodex,
					window(vendorlimit.PeriodWeekly, "known_reset", 0.9, 999999)),
			},
			wantFound: true, wantVendor: "codex", wantLabel: "known_reset",
		},
		{
			name: "사용률·초기화가 같으면 벤더 이름 오름차순",
			results: []vendorlimit.Result{
				availableResult(vendorlimit.VendorCodex,
					window(vendorlimit.PeriodFiveHour, "primary", 0.75, 1200)),
				availableResult(vendorlimit.VendorClaudeCode,
					window(vendorlimit.PeriodFiveHour, "primary", 0.75, 1200)),
			},
			wantFound: true, wantVendor: "claude_code", wantLabel: "primary",
		},
		{
			name: "같은 벤더의 동률은 짧은 창이 이긴다",
			results: []vendorlimit.Result{
				availableResult(vendorlimit.VendorClaudeCode,
					window(vendorlimit.PeriodMonthly, "monthly", 0.75, 1200),
					window(vendorlimit.PeriodWeekly, "seven_day", 0.75, 1200),
					window(vendorlimit.PeriodFiveHour, "five_hour", 0.75, 1200)),
			},
			wantFound: true, wantVendor: "claude_code", wantLabel: "five_hour",
		},
		{
			name: "창 종류까지 같으면 Label 오름차순",
			results: []vendorlimit.Result{
				availableResult(vendorlimit.VendorClaudeCode,
					window(vendorlimit.PeriodWeekly, "seven_day_opus", 0.75, 1200),
					window(vendorlimit.PeriodWeekly, "seven_day", 0.75, 1200)),
			},
			wantFound: true, wantVendor: "claude_code", wantLabel: "seven_day",
		},
		{
			name: "unavailable 벤더의 더 높은 창이 선택을 흔들지 않는다",
			results: []vendorlimit.Result{
				unavailableResult(vendorlimit.VendorClaudeCode, vendorlimit.ReasonTokenExpired,
					window(vendorlimit.PeriodFiveHour, "five_hour", 1.0, 10)),
				availableResult(vendorlimit.VendorCodex,
					window(vendorlimit.PeriodFiveHour, "primary", 0.11, 7200)),
			},
			wantFound: true, wantVendor: "codex", wantLabel: "primary",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TightestOf(tc.results)
			if got.Found != tc.wantFound {
				t.Fatalf("Found = %v, want %v (%+v)", got.Found, tc.wantFound, got)
			}
			if !tc.wantFound {
				return
			}
			if got.Vendor != tc.wantVendor || got.Label != tc.wantLabel {
				t.Fatalf("선택 = %s/%s, want %s/%s", got.Vendor, got.Label, tc.wantVendor, tc.wantLabel)
			}
		})
	}
}

// 같은 입력을 여러 번 넣어도 같은 답이어야 한다. 순회 순서에 기대는 구현이면 여기서 흔들린다.
func TestTightestLimitRepeatsTheSameAnswer(t *testing.T) {
	results := []vendorlimit.Result{
		availableResult(vendorlimit.VendorClaudeCode,
			window(vendorlimit.PeriodFiveHour, "five_hour", 0.5, 900),
			window(vendorlimit.PeriodWeekly, "seven_day", 0.5, 900)),
		availableResult(vendorlimit.VendorCodex,
			window(vendorlimit.PeriodFiveHour, "primary", 0.5, 900),
			window(vendorlimit.PeriodMonthly, "monthly", 0.5, 900)),
	}
	first := TightestOf(results)
	for i := range 50 {
		if got := TightestOf(results); got != first {
			t.Fatalf("%d번째 호출이 다른 답을 냈다: %+v != %+v", i, got, first)
		}
	}
}
