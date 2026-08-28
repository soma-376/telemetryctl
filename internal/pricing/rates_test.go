package pricing

import (
	"math"
	"testing"
)

// 공시 단가는 코드 변경으로만 바뀐다. 이 테스트가 그 변경을 눈에 띄게 만든다 —
// 단가를 고치면 여기가 함께 깨져야 하고, 깨지지 않았다면 표를 잘못 고친 것이다.
//
// ★ 아래 값들은 사람이 공시 페이지와 대조해야 하는 값이다 (rates.go 머리말 참조).
func TestDefaultRatesArePinned(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  Rate
	}{
		{
			name: "Opus 5", model: "claude-opus-5",
			want: Rate{InputPerMTokUSD: 5, OutputPerMTokUSD: 25, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 6.25},
		},
		{
			name: "Sonnet 4.6", model: "claude-sonnet-4-6",
			want: Rate{InputPerMTokUSD: 3, OutputPerMTokUSD: 15, CacheReadPerMTokUSD: 0.30, CacheWritePerMTokUSD: 3.75},
		},
		{
			name: "Haiku 4.5", model: "claude-haiku-4-5",
			want: Rate{InputPerMTokUSD: 1, OutputPerMTokUSD: 5, CacheReadPerMTokUSD: 0.10, CacheWritePerMTokUSD: 1.25},
		},
		{
			name: "GPT-5 Codex", model: "gpt-5-codex",
			want: Rate{InputPerMTokUSD: 1.25, OutputPerMTokUSD: 10, CacheReadPerMTokUSD: 0.125, CacheWritePerMTokUSD: 1.25},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got, ok := Default().Lookup(tt.model)
			if !ok {
				t.Fatalf("%s 가 표에 없다", tt.model)
			}
			if got != tt.want {
				t.Fatalf("%s 단가 = %+v, want %+v", tt.model, got, tt.want)
			}
		})
	}
}

func TestDefaultTableVersionIsTraceable(t *testing.T) {
	table := Default()
	if table.Version != DefaultVersion || table.Version == "" {
		t.Fatalf("판 번호 = %q", table.Version)
	}
	if table.EffectiveDate != DefaultEffectiveDate || len(table.EffectiveDate) != len("2006-01-02") {
		t.Fatalf("기준일 = %q", table.EffectiveDate)
	}
}

// 표의 모든 줄이 지켜야 하는 성질이다. 새 모델을 추가할 때 빠뜨리기 쉬운 것들을 잡는다.
func TestDefaultRatesAreWellFormed(t *testing.T) {
	table := Default()

	for _, key := range table.Models() {
		t.Run(key, func(t *testing.T) {
			_, rate, ok := table.Lookup(key)
			if !ok {
				t.Fatalf("%q 를 자기 키로 못 찾는다 — 정규화되지 않은 키다", key)
			}
			if Canonical(key) != key {
				t.Fatalf("표의 키가 정규화 결과와 다르다: Canonical(%q) = %q", key, Canonical(key))
			}
			if rate.InputPerMTokUSD <= 0 || rate.OutputPerMTokUSD <= 0 {
				t.Fatalf("입력·출력 단가는 필수다: %+v", rate)
			}
			if rate.CacheReadPerMTokUSD <= 0 || rate.CacheWritePerMTokUSD <= 0 {
				t.Fatalf("캐시 단가가 비었다 — 비었으면 캐시 토큰이 있는 호출이 unavailable 이 된다: %+v", rate)
			}
			if rate.CacheReadPerMTokUSD > rate.InputPerMTokUSD {
				t.Fatalf("캐시 읽기가 입력보다 비싸다 — 절감액이 음수가 된다: %+v", rate)
			}
			for _, v := range []float64{
				rate.InputPerMTokUSD, rate.OutputPerMTokUSD,
				rate.CacheReadPerMTokUSD, rate.CacheWritePerMTokUSD,
			} {
				// 0.001 USD/MTok = 토큰당 1 nano 다. 그보다 잔 단가는 반올림되어 사라진다.
				if math.Abs(v*1000-math.Round(v*1000)) > 1e-9 {
					t.Fatalf("단가 %v 가 0.001 USD/MTok 의 배수가 아니다 — 토큰당 nano 로 옮길 때 잘린다", v)
				}
			}
		})
	}
}

// 별칭은 반드시 표의 실제 줄을 가리켜야 하고, 사슬이면 안 된다.
func TestDefaultAliasesResolve(t *testing.T) {
	table := Default()
	for alias, target := range table.Aliases() {
		if Canonical(alias) != alias {
			t.Fatalf("별칭 %q 가 정규화 결과와 다르다 — 영원히 안 걸린다", alias)
		}
		if _, ok := table.Aliases()[target]; ok {
			t.Fatalf("별칭 %q 가 다른 별칭 %q 를 가리킨다", alias, target)
		}
		key, _, ok := table.Lookup(alias)
		if !ok || key != target {
			t.Fatalf("별칭 %q → %q 가 풀리지 않는다 (key=%q ok=%v)", alias, target, key, ok)
		}
	}
}
