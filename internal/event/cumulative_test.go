package event

import "testing"

// 이 규칙은 rollup 과 session 이 함께 쓴다. 여기서 틀리면 Today 카드와 세션 상세가
// 동시에, 같은 방향으로 틀린다 — 그래서 판정 하나하나를 표로 고정한다.
func TestCumulativeStep(t *testing.T) {
	const (
		watch = UnixNano(1_000_000_000_000)
		early = watch - 3_600_000_000_000 // 관측 시작 한 시간 전
		later = watch + 60_000_000_000    // 관측 시작 1분 뒤
	)

	tests := []struct {
		name      string
		state     CumulativeState
		point     CumulativePoint
		wantDelta float64
		wantKind  CumulativeKind
		wantNext  CumulativeState
	}{
		{
			name:      "첫 관측 + 관측 시작 이후에 시작한 계열 → 값 전체",
			point:     CumulativePoint{Value: 100, Start: watch, WatchFrom: watch},
			wantDelta: 100, wantKind: CumulativeFirstFull,
			wantNext: CumulativeState{Known: true, Value: 100, Start: watch},
		},
		{
			name:      "첫 관측 + 우리보다 먼저 시작한 계열 → 기준선",
			point:     CumulativePoint{Value: 100, Start: early, WatchFrom: watch},
			wantDelta: 0, wantKind: CumulativeBaseline,
			wantNext: CumulativeState{Known: true, Value: 100, Start: early},
		},
		{
			name:      "첫 관측 + start_time 없음 → 기준선(판정 불가라 안전한 쪽)",
			point:     CumulativePoint{Value: 100, WatchFrom: watch},
			wantDelta: 0, wantKind: CumulativeBaseline,
			wantNext: CumulativeState{Known: true, Value: 100},
		},
		{
			name:      "첫 관측 + start_time 이 음수(이상값) → 기준선",
			point:     CumulativePoint{Value: 100, Start: -1, WatchFrom: watch},
			wantDelta: 0, wantKind: CumulativeBaseline,
			wantNext: CumulativeState{Known: true, Value: 100, Start: -1},
		},
		{
			name:      "보통의 증가 → 차이만",
			state:     CumulativeState{Known: true, Value: 100, Start: watch},
			point:     CumulativePoint{Value: 150, Start: watch, WatchFrom: watch},
			wantDelta: 50, wantKind: CumulativeIncrement,
			wantNext: CumulativeState{Known: true, Value: 150, Start: watch},
		},
		{
			name:      "같은 값 반복 → 0 을 더한다",
			state:     CumulativeState{Known: true, Value: 100, Start: watch},
			point:     CumulativePoint{Value: 100, Start: watch, WatchFrom: watch},
			wantDelta: 0, wantKind: CumulativeIncrement,
			wantNext: CumulativeState{Known: true, Value: 100, Start: watch},
		},
		{
			name:  "수집 구간이 바뀌면 값이 커져도 리셋",
			state: CumulativeState{Known: true, Value: 150, Start: watch},
			point: CumulativePoint{Value: 180, Start: later, WatchFrom: watch},
			// 값의 증감만 보면 차이 30 으로 읽혀 재시작 이후 누적 150 이 사라진다.
			wantDelta: 180, wantKind: CumulativeReset,
			wantNext: CumulativeState{Known: true, Value: 180, Start: later},
		},
		{
			name:      "수집 구간이 바뀌고 값도 줄면 리셋",
			state:     CumulativeState{Known: true, Value: 150, Start: watch},
			point:     CumulativePoint{Value: 20, Start: later, WatchFrom: watch},
			wantDelta: 20, wantKind: CumulativeReset,
			wantNext: CumulativeState{Known: true, Value: 20, Start: later},
		},
		{
			name:  "같은 구간에서 값이 줄면 리셋이 아니다 — 0 을 더하고 기준선 유지",
			state: CumulativeState{Known: true, Value: 150, Start: watch},
			point: CumulativePoint{Value: 120, Start: watch, WatchFrom: watch},
			// next 가 s 그대로여야 다음 포인트의 차이가 부풀지 않는다.
			wantDelta: 0, wantKind: CumulativeOutOfOrder,
			wantNext: CumulativeState{Known: true, Value: 150, Start: watch},
		},
		{
			name:      "start_time 없이 값이 줄면 리셋으로 본다(폴백)",
			state:     CumulativeState{Known: true, Value: 150},
			point:     CumulativePoint{Value: 20, WatchFrom: watch},
			wantDelta: 20, wantKind: CumulativeReset,
			wantNext: CumulativeState{Known: true, Value: 20},
		},
		{
			name:      "직전에만 start_time 이 있고 지금은 없으면 값 증감으로 폴백",
			state:     CumulativeState{Known: true, Value: 150, Start: watch},
			point:     CumulativePoint{Value: 200, WatchFrom: watch},
			wantDelta: 50, wantKind: CumulativeIncrement,
			wantNext: CumulativeState{Known: true, Value: 200},
		},
		{
			name:      "구간이 뒤로 간 포인트는 리셋이 아니다 — 값 증감으로 폴백",
			state:     CumulativeState{Known: true, Value: 150, Start: later},
			point:     CumulativePoint{Value: 200, Start: watch, WatchFrom: watch},
			wantDelta: 50, wantKind: CumulativeIncrement,
			wantNext: CumulativeState{Known: true, Value: 200, Start: watch},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.state
			delta, next, kind := tt.state.Step(tt.point)
			// 값 타입이라 입력 상태는 변하지 않는다 — 계열 상태가 두 곳에서 갱신되면
			// 리셋 판정의 순서 의존성이 추적 불가능해진다.
			if tt.state != before {
				t.Fatalf("Step 이 수신자를 고쳤다: %+v → %+v", before, tt.state)
			}
			if delta != tt.wantDelta {
				t.Errorf("delta = %v, want %v", delta, tt.wantDelta)
			}
			if kind != tt.wantKind {
				t.Errorf("kind = %d, want %d", kind, tt.wantKind)
			}
			if next != tt.wantNext {
				t.Errorf("next = %+v, want %+v", next, tt.wantNext)
			}
			// 입력 상태는 값 타입이라 절대 변하지 않는다 — 계열 상태가 두 곳에서
			// 갱신되면 리셋 판정의 순서 의존성이 추적 불가능해진다.
			if tt.state.Known && next.Known && &tt.state == &next {
				t.Fatal("Step 이 상태를 제자리에서 고쳤다")
			}
		})
	}
}

// 어떤 입력에서도 증분이 음수가 되지 않는다. 음수가 한 번 들어가면 화면에 음수 비용이 뜨고,
// 그 값이 UPSERT 로 누적된 뒤에는 어느 이벤트 때문인지 되짚을 수 없다.
func TestCumulativeStepNeverReturnsNegative(t *testing.T) {
	const watch = UnixNano(1_000)
	starts := []UnixNano{0, watch - 1, watch, watch + 1}
	values := []float64{0, 1, 7, 7.5, 100}

	for _, prevStart := range starts {
		for _, prevValue := range values {
			for _, known := range []bool{false, true} {
				state := CumulativeState{Known: known, Value: prevValue, Start: prevStart}
				for _, start := range starts {
					for _, v := range values {
						delta, _, kind := state.Step(CumulativePoint{Value: v, Start: start, WatchFrom: watch})
						if delta < 0 {
							t.Fatalf("음수 증분 %v: state=%+v point=(v=%v start=%d) kind=%d",
								delta, state, v, start, kind)
						}
						if delta > v {
							t.Fatalf("증분 %v 가 누적값 %v 를 초과: state=%+v kind=%d", delta, v, state, kind)
						}
					}
				}
			}
		}
	}
}
