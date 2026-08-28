package dashboard

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// PROJ-92 — 단계 묶기·비율·세션 유형.
//
// 이 파일의 중심은 **결정론**이다. 같은 fixture 를 몇 번을 돌려도, 어떤 순서로 넘겨도
// 분류·단계 비율·세션 유형이 같아야 한다. 맵 순회 순서와 부동소수 동률이 그 성질을 깨는
// 두 가지 원인이라 둘 다 직접 겨냥한다.

// phaseTurn 은 분류가 정해진 턴 하나를 만든다. 규칙을 다시 검증하지 않고 단계·비율만
// 보기 위한 최소 입력이다.
func phaseTurn(index, start, end int64, w WorkType) TurnSignals {
	t := TurnSignals{TurnID: 1000 + index, TurnIndex: index, StartedAt: start, EndedAt: end}
	switch w {
	case WorkTypeDebugging:
		t.Tools = []ToolSignal{{ToolName: "Bash", Target: "go test ./...", Success: classifyOK(false)}}
	case WorkTypeImplementation:
		t.Tools = []ToolSignal{classifyEditTool(workspaceA + "/apply.go")}
	case WorkTypeVerification:
		t.Tools = []ToolSignal{classifyShellTool("go test ./...", classifyOK(true))}
	case WorkTypeExploration:
		t.Tools = []ToolSignal{{ToolName: "Read", Success: classifyOK(true)}}
	default:
		t.Tools = []ToolSignal{{ToolName: "QuantumFrobnicator", Success: classifyOK(true)}}
	}
	return t
}

// phaseSummary 는 실패 메시지에 싣는 사람이 읽는 요약이다. 단계가 어긋났을 때 어떤 턴이
// 어떤 근거로 그렇게 묶였는지 여기서 바로 보인다.
func phaseSummary(t *testing.T, got SessionClassification) string {
	t.Helper()
	b, err := json.MarshalIndent(struct {
		WorkType WorkType        `json:"work_type"`
		Reason   string          `json:"work_type_reason"`
		Turns    []TurnClass     `json:"turns"`
		Phases   []Phase         `json:"phases"`
		Shares   []WorkTypeShare `json:"shares"`
	}{got.WorkType, got.WorkTypeReason, got.Turns, got.Phases, got.Shares}, "  ", "  ")
	if err != nil {
		return "(요약 직렬화 실패: " + err.Error() + ")"
	}
	return string(b)
}

// 연속된 같은 분류가 한 단계로 묶이고, 분류가 바뀌면 단계도 바뀐다.
func TestBuildPhasesGroupsConsecutiveTurns(t *testing.T) {
	got := ClassifyTurns(7, []TurnSignals{
		phaseTurn(0, 1000, 1100, WorkTypeExploration),
		phaseTurn(1, 1100, 1200, WorkTypeExploration),
		phaseTurn(2, 1200, 1500, WorkTypeImplementation),
		phaseTurn(3, 1500, 1600, WorkTypeExploration),
	})

	if got.SessionID != 7 {
		t.Errorf("SessionID = %d, want 7", got.SessionID)
	}
	if len(got.Phases) != 3 {
		t.Fatalf("단계 = %d, want 3\n%s", len(got.Phases), phaseSummary(t, got))
	}
	want := []struct {
		workType WorkType
		start    int64
		end      int64
		turns    int
		duration int64
	}{
		{WorkTypeExploration, 0, 1, 2, 200},
		{WorkTypeImplementation, 2, 2, 1, 300},
		{WorkTypeExploration, 3, 3, 1, 100},
	}
	for i, w := range want {
		p := got.Phases[i]
		if p.Index != i || p.WorkType != w.workType || p.StartTurnIndex != w.start ||
			p.EndTurnIndex != w.end || p.TurnCount != w.turns || p.DurationSec != w.duration {
			t.Errorf("단계 %d = %+v, want %+v\n%s", i, p, w, phaseSummary(t, got))
		}
		if p.Reason == "" {
			t.Errorf("단계 %d 의 Reason 이 비었다\n%s", i, phaseSummary(t, got))
		}
	}
	// 비율의 합은 정확히 1000 이다. 화면의 막대에 빈틈이 남으면 안 된다.
	var sum int64
	for _, p := range got.Phases {
		sum += p.SharePermille
	}
	if sum != 1000 {
		t.Errorf("단계 비율 합 = %d, want 1000\n%s", sum, phaseSummary(t, got))
	}
}

// 세션 유형은 누적 시간이 가장 긴 분류다.
func TestSessionWorkTypeIsLongestAccumulatedDuration(t *testing.T) {
	got := ClassifyTurns(1, []TurnSignals{
		// 탐색이 턴 수는 더 많지만 시간은 짧다.
		phaseTurn(0, 1000, 1010, WorkTypeExploration),
		phaseTurn(1, 1010, 1020, WorkTypeExploration),
		phaseTurn(2, 1020, 1030, WorkTypeExploration),
		phaseTurn(3, 1030, 1330, WorkTypeImplementation),
	})
	if got.WorkType != WorkTypeImplementation {
		t.Fatalf("세션 유형 = %s, want implementation\n%s", got.WorkType, phaseSummary(t, got))
	}
	if !got.DurationKnown {
		t.Errorf("DurationKnown = false — 시각을 알고 있다\n%s", phaseSummary(t, got))
	}
	if got.TotalDurationSec != 330 {
		t.Errorf("총 길이 = %d초, want 330\n%s", got.TotalDurationSec, phaseSummary(t, got))
	}
	// 근거가 실패 메시지에 보이는 형태로 남아야 한다.
	if got.WorkTypeReason == "" {
		t.Error("WorkTypeReason 이 비었다 — 세션 유형의 근거를 확인할 수 없다")
	}
}

// 누적 시간이 같으면 혼합 턴과 같은 우선순위를 쓴다: 디버깅 → 구현 → 검증 → 탐색.
func TestSessionWorkTypeTieUsesPriority(t *testing.T) {
	tests := []struct {
		name  string
		turns []TurnSignals
		want  WorkType
	}{
		{
			name: "구현과 검증이 동률이면 구현",
			turns: []TurnSignals{
				phaseTurn(0, 1000, 1100, WorkTypeVerification),
				phaseTurn(1, 1100, 1200, WorkTypeImplementation),
			},
			want: WorkTypeImplementation,
		},
		{
			name: "디버깅과 구현이 동률이면 디버깅",
			turns: []TurnSignals{
				phaseTurn(0, 1000, 1100, WorkTypeImplementation),
				phaseTurn(1, 1100, 1200, WorkTypeDebugging),
			},
			want: WorkTypeDebugging,
		},
		{
			name: "탐색과 unknown 이 동률이면 탐색 — unknown 은 언제나 맨 뒤",
			turns: []TurnSignals{
				phaseTurn(0, 1000, 1100, WorkTypeUnknown),
				phaseTurn(1, 1100, 1200, WorkTypeExploration),
			},
			want: WorkTypeExploration,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyTurns(1, tt.turns)
			if got.WorkType != tt.want {
				t.Fatalf("세션 유형 = %s, want %s\n%s", got.WorkType, tt.want, phaseSummary(t, got))
			}
			if !strings.Contains(got.WorkTypeReason, "동률") {
				t.Errorf("동률인데 근거가 그 사실을 말하지 않는다: %q", got.WorkTypeReason)
			}
		})
	}
}

// turns.ended_at 은 현재 쓰기 경로가 채우지 않는다. 끝점 사다리가 그 사실을 견뎌야 한다.
func TestTurnDurationLadder(t *testing.T) {
	tests := []struct {
		name      string
		turn      TurnSignals
		nextStart int64
		want      int64
	}{
		{
			name: "ended_at 이 있으면 그것을 쓴다",
			turn: TurnSignals{StartedAt: 100, EndedAt: 160, LastSeenAt: 130},
			want: 60,
		},
		{
			name: "ended_at 이 없으면 마지막 활동 시각이 끝이다",
			turn: TurnSignals{StartedAt: 100, LastSeenAt: 130},
			want: 30,
		},
		{
			name:      "활동도 없으면 다음 턴 시작까지가 길이다 — 순수 문답 턴",
			turn:      TurnSignals{StartedAt: 100},
			nextStart: 145,
			want:      45,
		},
		{
			name:      "다음 턴이 이미 시작했으면 거기서 자른다 — 턴은 겹치지 않는다",
			turn:      TurnSignals{StartedAt: 100, LastSeenAt: 900},
			nextStart: 150,
			want:      50,
		},
		{
			name: "마지막 턴은 활동 시각까지만 본다 — 세션 뒤의 유휴를 얹지 않는다",
			turn: TurnSignals{StartedAt: 100},
			want: 0,
		},
		{
			name: "시작을 모르면 길이도 모른다",
			turn: TurnSignals{EndedAt: 500, LastSeenAt: 400},
			want: 0,
		},
		{
			name: "끝이 시작보다 앞서면 0 이다",
			turn: TurnSignals{StartedAt: 500, EndedAt: 100},
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := turnDurationSec(tt.turn, tt.nextStart); got != tt.want {
				t.Errorf("turnDurationSec(%+v, next=%d) = %d, want %d",
					tt.turn, tt.nextStart, got, tt.want)
			}
		})
	}
}

// 세션 길이를 하나도 모르면 비율과 주 유형을 턴 수로 가른다. 그러지 않으면 한 턴짜리
// 디버깅이 스무 턴짜리 탐색을 우선순위만으로 이긴다.
func TestZeroDurationFallsBackToTurnCount(t *testing.T) {
	turns := []TurnSignals{
		{TurnID: 1, TurnIndex: 0, Tools: []ToolSignal{{ToolName: "Read", Success: classifyOK(true)}}},
		{TurnID: 2, TurnIndex: 1, Tools: []ToolSignal{{ToolName: "Grep", Success: classifyOK(true)}}},
		{TurnID: 3, TurnIndex: 2, Tools: []ToolSignal{{ToolName: "LS", Success: classifyOK(true)}}},
		{TurnID: 4, TurnIndex: 3, Tools: []ToolSignal{
			classifyShellTool("go test ./...", classifyOK(false)),
		}},
	}
	got := ClassifyTurns(1, turns)

	if got.DurationKnown {
		t.Fatalf("DurationKnown = true — 시각이 하나도 없다\n%s", phaseSummary(t, got))
	}
	if got.WorkType != WorkTypeExploration {
		t.Fatalf("세션 유형 = %s, want exploration (턴 수 3 대 1)\n%s",
			got.WorkType, phaseSummary(t, got))
	}
	if !strings.Contains(got.WorkTypeReason, "턴 수 기준") {
		t.Errorf("근거가 턴 수 기준임을 말하지 않는다: %q", got.WorkTypeReason)
	}
	var sum int64
	for _, s := range got.Shares {
		sum += s.SharePermille
	}
	if sum != 1000 {
		t.Errorf("유형 비율 합 = %d, want 1000\n%s", sum, phaseSummary(t, got))
	}
}

// 유형 비율은 **우선순위 고정 순서**로 나온다. 값으로 정렬하면 동률에서 순서가 흔들려
// 화면 범례의 색이 새로고침마다 자리를 바꾼다.
func TestSharesUseFixedPriorityOrder(t *testing.T) {
	got := ClassifyTurns(1, []TurnSignals{
		phaseTurn(0, 1000, 1100, WorkTypeExploration),
		phaseTurn(1, 1100, 1200, WorkTypeVerification),
		phaseTurn(2, 1200, 1300, WorkTypeDebugging),
	})
	want := []WorkType{WorkTypeDebugging, WorkTypeVerification, WorkTypeExploration}
	if len(got.Shares) != len(want) {
		t.Fatalf("유형 = %d개, want %d\n%s", len(got.Shares), len(want), phaseSummary(t, got))
	}
	for i, w := range want {
		if got.Shares[i].WorkType != w {
			t.Fatalf("Shares[%d] = %s, want %s\n%s",
				i, got.Shares[i].WorkType, w, phaseSummary(t, got))
		}
	}
	// 나타나지 않은 유형은 행을 만들지 않는다.
	for _, s := range got.Shares {
		if s.WorkType == WorkTypeImplementation {
			t.Errorf("없던 유형이 비율에 들어갔다\n%s", phaseSummary(t, got))
		}
	}
}

// 최대잉여법은 합이 정확히 1000 이고 같은 무게에 같은 배분을 준다.
func TestPermilleShares(t *testing.T) {
	tests := []struct {
		name    string
		weights []int64
		want    []int64
	}{
		{name: "빈 입력", weights: nil, want: []int64{}},
		{name: "합이 0 이면 전부 0", weights: []int64{0, 0}, want: []int64{0, 0}},
		{name: "정확히 나뉘는 경우", weights: []int64{1, 1, 2}, want: []int64{250, 250, 500}},
		// 1000/3 = 333.33… 남는 1 은 나머지가 같으므로 앞선 항목이 가져간다.
		{name: "3등분의 나머지는 앞에서부터", weights: []int64{1, 1, 1}, want: []int64{334, 333, 333}},
		{name: "음수는 무게로 세지 않는다", weights: []int64{-5, 10}, want: []int64{0, 1000}},
		{name: "7등분", weights: []int64{1, 1, 1, 1, 1, 1, 1},
			want: []int64{143, 143, 143, 143, 143, 143, 142}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := permilleShares(tt.weights)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("permilleShares(%v) = %v, want %v", tt.weights, got, tt.want)
			}
			var sum int64
			for _, v := range got {
				sum += v
			}
			if len(tt.weights) > 0 && sum != 0 && sum != 1000 {
				t.Errorf("합 = %d, want 1000", sum)
			}
		})
	}
}

// 인수조건: 고정 fixture 에서 분류·단계 비율·세션 유형이 **항상** 같다.
// 반복 실행과 입력 순서 뒤집기를 함께 본다 — 맵 순회 순서가 결과에 새어들면 둘 중
// 하나에서 반드시 걸린다.
func TestClassifyTurnsIsDeterministic(t *testing.T) {
	fixture := []TurnSignals{
		phaseTurn(0, 1000, 1100, WorkTypeExploration),
		phaseTurn(1, 1100, 1400, WorkTypeImplementation),
		phaseTurn(2, 1400, 1700, WorkTypeVerification),
		phaseTurn(3, 1700, 1800, WorkTypeDebugging),
		phaseTurn(4, 1800, 1900, WorkTypeUnknown),
		phaseTurn(5, 1900, 2200, WorkTypeImplementation),
	}

	want := ClassifyTurns(42, fixture)
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	const rounds = 50
	for i := range rounds {
		got := ClassifyTurns(42, fixture)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%d회차 결과가 다르다\n  got:  %s\n  want: %s", i, phaseSummary(t, got), phaseSummary(t, want))
		}
		gotJSON, merr := json.Marshal(got)
		if merr != nil {
			t.Fatalf("Marshal: %v", merr)
		}
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("%d회차 JSON 이 다르다\n  got:  %s\n  want: %s", i, gotJSON, wantJSON)
		}
	}

	// 입력 순서에 의존하지 않는다. 역순과 회전을 넣어도 같은 답이어야 한다.
	reversed := make([]TurnSignals, len(fixture))
	for i, turn := range fixture {
		reversed[len(fixture)-1-i] = turn
	}
	rotated := append(append([]TurnSignals{}, fixture[3:]...), fixture[:3]...)
	for name, order := range map[string][]TurnSignals{"역순": reversed, "회전": rotated} {
		got := ClassifyTurns(42, order)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s 입력에서 결과가 다르다\n  got:  %s\n  want: %s",
				name, phaseSummary(t, got), phaseSummary(t, want))
		}
	}

	// 원본 슬라이스를 건드리지 않는다 — 호출자의 값이 조용히 재정렬되면 안 된다.
	if fixture[0].TurnIndex != 0 || fixture[5].TurnIndex != 5 {
		t.Error("ClassifyTurns 가 인자 슬라이스를 재정렬했다")
	}
}

// 턴이 없는 세션은 에러가 아니라 빈 분류다.
func TestClassifyTurnsWithoutTurns(t *testing.T) {
	got := ClassifyTurns(9, nil)
	if got.WorkType != WorkTypeUnknown {
		t.Errorf("WorkType = %s, want unknown", got.WorkType)
	}
	if got.SessionID != 9 || got.TurnCount != 0 || got.TotalDurationSec != 0 {
		t.Errorf("빈 분류가 아니다: %+v", got)
	}
	// JSON 에서 null 이 되면 프런트엔드의 .map 이 그대로 터진다.
	if got.Turns == nil || got.Phases == nil || got.Shares == nil {
		t.Errorf("슬라이스가 nil 이다: %+v", got)
	}
	if got.WorkTypeReason == "" {
		t.Error("WorkTypeReason 이 비었다")
	}
}
