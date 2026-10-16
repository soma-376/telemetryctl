package dashboard

import (
	"encoding/json"
	"testing"
)

// 제로값이 미관측이어야 한다. 이 성질이 깨지면 집계 누산기의 초기 상태가 "0줄을 관측했다" 가 된다.
func TestLineCountZeroValueIsUnobserved(t *testing.T) {
	var c LineCount
	if n, ok := c.Get(); ok || n != 0 {
		t.Fatalf("제로값 = (%d, %v), want (0, false)", n, ok)
	}
	if c.Observed() {
		t.Fatal("제로값이 관측됨으로 보고됐다")
	}
}

// 관측된 0 과 미관측은 값으로도 달라야 한다. 둘이 == 로 같아지면 테이블 주도 단언이 통과해 버린다.
func TestLineCountObservedZeroDiffersFromUnobserved(t *testing.T) {
	if ObservedLines(0) == (LineCount{}) {
		t.Fatal("관측된 0 과 미관측이 같은 값이다 — 이 타입이 존재할 이유가 사라진다")
	}
}

func TestLineCountOr(t *testing.T) {
	tests := []struct {
		name string
		in   LineCount
		want int64
	}{
		{name: "미관측은 fallback", in: LineCount{}, want: -1},
		{name: "관측된 0 은 0", in: ObservedLines(0), want: 0},
		{name: "관측된 값", in: ObservedLines(42), want: 42},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Or(-1); got != tc.want {
				t.Fatalf("Or(-1) = %d, want %d", got, tc.want)
			}
		})
	}
}

// JSON 계약이 이 티켓의 핵심이다. 미관측은 null, 관측된 0 은 0 이어야 하고 둘이 절대 같은
// 바이트로 나가면 안 된다.
func TestLineCountJSONRoundTrip(t *testing.T) {
	type holder struct {
		Additions LineCount `json:"additions"`
	}

	tests := []struct {
		name string
		in   LineCount
		want string
	}{
		{name: "미관측은 null", in: LineCount{}, want: `{"additions":null}`},
		{name: "관측된 0 은 0", in: ObservedLines(0), want: `{"additions":0}`},
		{name: "관측된 양수", in: ObservedLines(37), want: `{"additions":37}`},
		{name: "관측된 음수", in: ObservedLines(-3), want: `{"additions":-3}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(holder{Additions: tc.in})
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(b) != tc.want {
				t.Fatalf("JSON = %s, want %s", b, tc.want)
			}

			var back holder
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if back.Additions != tc.in {
				gotN, gotOK := back.Additions.Get()
				wantN, wantOK := tc.in.Get()
				t.Fatalf("왕복 결과 = (%d, %v), want (%d, %v)", gotN, gotOK, wantN, wantOK)
			}
		})
	}
}

// 정수도 null 도 아닌 값은 조용히 0 이 되면 안 된다. 오류 문자열에 SQL 도 원문도 실리지 않는다.
func TestLineCountUnmarshalRejectsNonInteger(t *testing.T) {
	var c LineCount
	if err := c.UnmarshalJSON([]byte(`"열일곱"`)); err == nil {
		t.Fatal("문자열이 줄 수로 받아들여졌다")
	}
	if n, ok := c.Get(); ok || n != 0 {
		t.Fatalf("실패한 파싱이 값을 남겼다: (%d, %v)", n, ok)
	}
}

func TestLineCountPlus(t *testing.T) {
	tests := []struct {
		name string
		base LineCount
		add  int64
		want LineCount
	}{
		{name: "미관측에 더하면 관측된 값이 된다", base: LineCount{}, add: 5, want: ObservedLines(5)},
		{name: "관측값 누적", base: ObservedLines(5), add: 7, want: ObservedLines(12)},
		{name: "0 을 더해도 관측 상태는 유지", base: ObservedLines(3), add: 0, want: ObservedLines(3)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.base.plus(tc.add); got != tc.want {
				t.Fatalf("plus(%d) = %+v, want %+v", tc.add, got, tc.want)
			}
		})
	}
}
