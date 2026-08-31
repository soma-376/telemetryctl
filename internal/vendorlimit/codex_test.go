package vendorlimit

import (
	"context"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/codexapp"
)

type stubCodexReader struct {
	snapshot codexapp.RateLimitSnapshot
	err      error
}

func (s stubCodexReader) RateLimits(context.Context) (codexapp.RateLimitSnapshot, error) {
	return s.snapshot, s.err
}

func int64p(v int64) *int64    { return &v }
func stringp(v string) *string { return &v }

func normalCodexSnapshot() codexapp.RateLimitSnapshot {
	return codexapp.RateLimitSnapshot{
		PlanType:  stringp("plus"),
		Primary:   &codexapp.RateLimitWindow{UsedPercent: 12, WindowDurationMins: int64p(300), ResetsAt: int64p(testNow.Add(time.Hour).Unix())},
		Secondary: &codexapp.RateLimitWindow{UsedPercent: 40, WindowDurationMins: int64p(10080), ResetsAt: int64p(testNow.Add(24 * time.Hour).Unix())},
		Credits:   &codexapp.CreditsSnapshot{HasCredits: true},
	}
}

func TestCodexAdapter정상응답을공통모델로옮긴다(t *testing.T) {
	t.Parallel()
	env := probeEnv{codex: stubCodexReader{snapshot: normalCodexSnapshot()}, now: fixedNow}
	got := (codexAdapter{}).probe(context.Background(), env)
	if got.State != StateAvailable || got.Plan != "plus" || len(got.Windows) != 2 {
		t.Fatalf("결과가 다르다: %+v", got)
	}
	primary := got.Windows[0]
	if primary.Period != PeriodFiveHour || primary.WindowMinutes != 300 || primary.UsedRatio != 0.12 {
		t.Errorf("primary 창이 다르다: %+v", primary)
	}
	if primary.ResetsInSeconds != 3600 || primary.ResetsAt != "2026-08-28T04:04:05Z" {
		t.Errorf("초기화 시각이 다르다: %+v", primary)
	}
	if !got.Extra.Supported || !got.Extra.Enabled {
		t.Errorf("크레딧 상태가 다르다: %+v", got.Extra)
	}
}

func TestCodexAdapter는모르는값을비워둔다(t *testing.T) {
	t.Parallel()
	snapshot := codexapp.RateLimitSnapshot{Primary: &codexapp.RateLimitWindow{UsedPercent: 1, WindowDurationMins: int64p(43200)}}
	got := (codexAdapter{}).probe(context.Background(), probeEnv{codex: stubCodexReader{snapshot: snapshot}, now: fixedNow})
	if got.State != StateAvailable || got.Plan != "" || got.Extra.Supported {
		t.Fatalf("모르는 값을 지어냈다: %+v", got)
	}
	if len(got.Windows) != 1 || got.Windows[0].Period != PeriodMonthly || got.Windows[0].ResetsAt != "" {
		t.Fatalf("창이 다르다: %+v", got.Windows)
	}
}

func TestCodexAdapter실패경로는벤더만Unavailable로만든다(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want Reason
	}{
		{"프로세스 실행 실패", codexapp.ErrUnavailable, ReasonInternal},
		{"프로토콜 불일치", codexapp.ErrProtocol, ReasonResponseUnrecognized},
		{"컨텍스트 취소", context.Canceled, ReasonNetwork},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (codexAdapter{}).probe(context.Background(), probeEnv{codex: stubCodexReader{err: tc.err}, now: fixedNow})
			if got.State != StateUnavailable || got.Reason != tc.want || got.Windows == nil {
				t.Fatalf("결과가 다르다: %+v", got)
			}
		})
	}
	got := (codexAdapter{}).probe(context.Background(), probeEnv{codex: stubCodexReader{snapshot: codexapp.RateLimitSnapshot{}}, now: fixedNow})
	if got.Reason != ReasonResponseUnrecognized {
		t.Fatalf("빈 응답 reason = %q", got.Reason)
	}
}
