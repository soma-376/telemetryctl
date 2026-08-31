package vendorlimit

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// 이 테스트가 Token 의 존재 이유를 고정한다. 어떤 흔한 출력 경로로도 원값이 나가면 안 된다.
func TestToken은어떤출력경로로도원값을내보내지않는다(t *testing.T) {
	t.Parallel()
	const secret = "sk-ant-oat01-LEAK-CANARY-must-never-appear"
	tok := newToken(secret)

	// 구조체에 담아 찍는 경우까지 본다 — 실수는 대개 토큰 하나가 아니라 그것을 품은
	// 구조체를 통째로 로깅할 때 일어난다.
	type holder struct {
		Name  string
		Token Token
	}
	h := holder{Name: "claude_code", Token: tok}

	encoded, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	outputs := map[string]string{
		"%v":            fmt.Sprintf("%v", tok),
		"%s":            fmt.Sprintf("%s", tok),
		"%#v":           fmt.Sprintf("%#v", tok),
		"%+v(구조체)":      fmt.Sprintf("%+v", h),
		"%#v(구조체)":      fmt.Sprintf("%#v", h),
		"String()":      tok.String(),
		"json(구조체)":     string(encoded),
		"errors.Errorf": fmt.Errorf("호출 실패: %v", tok).Error(),
	}
	for name, out := range outputs {
		if strings.Contains(out, secret) {
			t.Errorf("%s 로 토큰이 샜다: %s", name, out)
		}
		if !strings.Contains(out, redacted) {
			t.Errorf("%s 에 %q 가 없다 — 마스킹 경로를 타지 못했다: %s", name, redacted, out)
		}
	}

	// 전제 확인: 원값은 패키지 안에서는 그대로 쓸 수 있어야 한다. 아니면 위 단언이
	// "빈 토큰이라 안 샜다" 는 공허한 통과가 된다.
	if tok.reveal() != secret {
		t.Fatalf("reveal 이 원값을 잃었다: %q", tok.reveal())
	}
	if tok.empty() {
		t.Fatal("empty = true — 픽스처가 비었다")
	}
}

func TestNewToken은공백을다듬는다(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		raw   string
		want  string
		empty bool
	}{
		{name: "그대로", raw: "abc", want: "abc"},
		{name: "줄바꿈 제거", raw: "abc\n", want: "abc"},
		{name: "앞뒤 공백 제거", raw: "  abc  ", want: "abc"},
		{name: "공백뿐이면 빈 토큰", raw: "  \n ", want: "", empty: true},
		{name: "빈 문자열", raw: "", want: "", empty: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tok := newToken(tc.raw)
			if tok.reveal() != tc.want {
				t.Errorf("reveal = %q, want %q", tok.reveal(), tc.want)
			}
			if tok.empty() != tc.empty {
				t.Errorf("empty = %v, want %v", tok.empty(), tc.empty)
			}
		})
	}
}

func TestStripSecret은감싼오류에서비밀을지운다(t *testing.T) {
	t.Parallel()
	const secret = "TOKEN-XYZ"

	cause := fmt.Errorf("상위가 %s 를 거부했다", secret)
	got := stripSecret(cause, secret)
	if strings.Contains(got.Error(), secret) {
		t.Errorf("비밀이 남았다: %v", got)
	}
	if !strings.Contains(got.Error(), redacted) {
		t.Errorf("마스킹 흔적이 없다: %v", got)
	}

	// 비밀이 없으면 원본을 그대로 돌려줘야 한다 — errors.Is 사슬을 끊으면 안 된다.
	sentinel := errors.New("연결 거부")
	wrapped := fmt.Errorf("호출: %w", sentinel)
	if out := stripSecret(wrapped, secret); !errors.Is(out, sentinel) {
		t.Errorf("사슬이 끊겼다: %v", out)
	}
	if stripSecret(nil, secret) != nil {
		t.Error("nil 이 변형됐다")
	}
	if out := stripSecret(sentinel, ""); out != sentinel {
		t.Error("빈 비밀로 오류가 변형됐다")
	}
}

func TestRatioFromPercent는이상값을접는다(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		percent float64
		want    float64
	}{
		{name: "0%", percent: 0, want: 0},
		{name: "45%", percent: 45, want: 0.45},
		{name: "100%", percent: 100, want: 1},
		{name: "한도 초과는 그대로 넘긴다", percent: 150, want: 1.5},
		{name: "음수는 0", percent: -10, want: 0},
		{name: "NaN 은 0 — json.Marshal 을 통째로 죽인다", percent: math.NaN(), want: 0},
		{name: "Inf 는 0", percent: math.Inf(1), want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ratioFromPercent(tc.percent); got != tc.want {
				t.Errorf("ratioFromPercent(%v) = %v, want %v", tc.percent, got, tc.want)
			}
		})
	}
}

// NaN 이 하나라도 남으면 스냅샷 전체가 직렬화되지 않는다. 그 경로를 실제로 태워 본다.
func TestSnapshot은NaN이섞여도직렬화된다(t *testing.T) {
	t.Parallel()
	s := Snapshot{
		ObservedAt: formatTime(time.Unix(1_700_000_000, 0)),
		Results: []Result{{
			Vendor:  VendorClaudeCode,
			State:   StateAvailable,
			Windows: []Window{{Period: PeriodFiveHour, UsedRatio: ratioFromPercent(math.NaN())}},
		}},
	}
	if _, err := json.Marshal(s); err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
}

func TestPeriodFromMinutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		minutes int
		want    PeriodKind
	}{
		{name: "5시간", minutes: 300, want: PeriodFiveHour},
		{name: "경계가 흔들려도 5시간", minutes: 360, want: PeriodFiveHour},
		{name: "주간", minutes: 10080, want: PeriodWeekly},
		{name: "월간", minutes: 43200, want: PeriodMonthly},
		{name: "0 은 모름", minutes: 0, want: PeriodUnknown},
		{name: "음수는 모름", minutes: -1, want: PeriodUnknown},
		{name: "너무 긴 창은 모름", minutes: 60 * 24 * 365, want: PeriodUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := periodFromMinutes(tc.minutes); got != tc.want {
				t.Errorf("periodFromMinutes(%d) = %q, want %q", tc.minutes, got, tc.want)
			}
		})
	}
}

func TestUnavailable은성공경로와같은모양을유지한다(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()
	r := unavailable(VendorCodex, ReasonCredentialMissing, "자격증명 파일 없음", now)

	if r.State != StateUnavailable || r.Reason != ReasonCredentialMissing {
		t.Errorf("상태가 다르다: %+v", r)
	}
	if r.Windows == nil {
		t.Error("Windows 가 nil — JSON 에서 null 이 되어 화면이 분기해야 한다")
	}
	if r.ObservedAt != "2023-11-14T22:13:20Z" {
		t.Errorf("observed_at = %q", r.ObservedAt)
	}
	if formatTime(time.Time{}) != "" {
		t.Error("영값 시각이 문자열로 새어 나왔다")
	}
}
