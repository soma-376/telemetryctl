package dashboard

import (
	"math"
	"reflect"
	"testing"
)

// 인수조건: 「벤더 점유율 합계와 반올림 규칙이 일관된다」.
//
// 합계가 상수여야 화면이 "62% · 38%" 를 그대로 믿을 수 있다. 값마다 따로 반올림하면
// 99% 나 101% 가 나오는 날이 생기고, 그때 어느 값이 틀렸는지 되짚을 근거가 없다.
func TestSharePermilleSumsToConstant(t *testing.T) {
	tests := []struct {
		name   string
		values []int64
		names  []string
		want   []int
	}{
		{
			name:   "정확히 나눠떨어지는 경우",
			values: []int64{750, 250},
			names:  []string{"claude_code", "codex"},
			want:   []int{750, 250},
		},
		{
			name:   "항목이 하나뿐이면 그 하나가 전부 갖는다",
			values: []int64{7},
			names:  []string{"claude_code"},
			want:   []int{SharePermilleTotal},
		},
		{
			// 1/3 씩 나누면 각 333, 남는 1 을 나머지가 같은 셋 중 이름이 앞선 항목이 받는다.
			name:   "동률의 나머지는 이름 오름차순으로 가른다",
			values: []int64{1, 1, 1},
			names:  []string{"codex", "aider", "claude_code"},
			want:   []int{333, 334, 333},
		},
		{
			// floor 는 각각 142/285/571 이고 합이 998 이다. 남은 2 를 나머지가 큰 둘이 받는다.
			name:   "최대잔여법이 남은 몫을 나눠 준다",
			values: []int64{1, 2, 4},
			names:  []string{"a", "b", "c"},
			want:   []int{143, 286, 571},
		},
		{
			name:   "값이 0 인 항목은 0 을 받는다",
			values: []int64{10, 0, 0},
			names:  []string{"a", "b", "c"},
			want:   []int{SharePermilleTotal, 0, 0},
		},
		{
			// 활동이 없는 날이다. 1000 을 임의로 배분하면 화면이 "아무것도 안 한 날의 100%" 를 그린다.
			name:   "전부 0 이면 합도 0 이다",
			values: []int64{0, 0},
			names:  []string{"a", "b"},
			want:   []int{0, 0},
		},
		{
			// 음수는 있을 수 없는 값이다. 그대로 두면 다른 항목의 점유율이 부풀어 합이 1000 을 넘는다.
			name:   "음수는 0 으로 본다",
			values: []int64{-5, 10},
			names:  []string{"a", "b"},
			want:   []int{0, SharePermilleTotal},
		},
		{
			name:   "빈 입력",
			values: []int64{},
			names:  []string{},
			want:   []int{},
		},
		{
			// nano-USD 는 int64 다. 1000 을 곱하면 int64 가 넘치는 크기라 128비트 중간곱이
			// 아니면 점유율이 음수가 된다.
			name:   "int64 를 넘기는 중간곱에서도 정확하다",
			values: []int64{math.MaxInt64 / 2, math.MaxInt64 / 2},
			names:  []string{"a", "b"},
			want:   []int{500, 500},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sharePermille(tc.values, tc.names)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("sharePermille(%v) = %v, want %v", tc.values, got, tc.want)
			}
			sum := 0
			for _, v := range got {
				if v < 0 {
					t.Errorf("음수 점유율이 있다: %v", got)
				}
				sum += v
			}
			want := SharePermilleTotal
			if !hasPositive(tc.values) {
				// 기준 합계가 0 이면 합도 0 이다 — 이것도 문서화된 상수다.
				want = 0
			}
			if sum != want {
				t.Errorf("합 = %d, want %d (%v)", sum, want, got)
			}
		})
	}
}

// 이름 목록이 값보다 짧아도 터지지 않아야 한다. 호출자 실수로 조회 전체가 실패하면
// 화면은 점유율이 아니라 에러 토스트를 본다.
func TestSharePermilleToleratesShortNames(t *testing.T) {
	got := sharePermille([]int64{1, 1}, nil)
	sum := 0
	for _, v := range got {
		sum += v
	}
	if sum != SharePermilleTotal {
		t.Errorf("합 = %d, want %d (%v)", sum, SharePermilleTotal, got)
	}
}

func hasPositive(values []int64) bool {
	for _, v := range values {
		if v > 0 {
			return true
		}
	}
	return false
}
