package dashboard

import (
	"sort"
	"strconv"
	"strings"
)

// 턴 분류를 화면용 단계(phase)로 묶고 세션의 주 작업 유형을 낸다 (PROJ-92).
//
// # 시간을 어떻게 아는가
//
// v3 의 turns 에는 started_at 과 ended_at 이 있지만 **쓰기 경로가 ended_at 을 채우지
// 않는다** (store/resolve.go 의 upsertTurnSQL 컬럼 목록에 없다). 그래서 턴 길이를
// ended_at 하나에 기대면 모든 턴이 0 초가 되고, 그 순간 "누적 시간이 가장 긴 분류" 라는
// 세션 유형 규칙이 통째로 동률로 무너진다.
//
// 그래서 끝점을 사다리로 정한다 (turnDurationSec).
//
//  1. turns.ended_at 이 있으면 그것 — 언젠가 쓰기 경로가 채우면 그날부터 정답이 된다
//  2. 없으면 턴 안에서 관측된 마지막 활동 시각 (events.occurred_at · tool_calls.called_at)
//  3. 둘 다 없고 다음 턴이 이미 시작했으면 그 시각 — 도구를 하나도 안 쓴 순수 문답 턴이다
//
// 어느 경우에도 **다음 턴의 시작을 넘지 않는다.** 턴은 겹치지 않으므로 넘는 값은 관측이
// 아니라 오류다. 마지막 턴에는 다음 턴이 없어 2번까지만 간다 — 세션이 끝난 뒤의 유휴
// 시간을 마지막 턴에 얹지 않기 위해서다.
//
// # 왜 부동소수를 안 쓰는가
//
// 비율은 천분율 정수다. 실수로 비교하면 0.1+0.2 같은 값이 동률 판정을 실행마다 다르게
// 만들 수 있고, 그 흔들림은 재현되지 않아 디버깅이 불가능하다. 나머지 배분은 최대잉여법을
// 고정 순서로 돌린다 (permilleShares).

// Phase 는 연속된 같은 분류의 턴 묶음이다 — 화면 「세션 흐름」의 한 칸.
type Phase struct {
	// Index 는 세션 안에서의 순번이다 (0부터).
	Index    int      `json:"index"`
	WorkType WorkType `json:"work_type"`

	StartTurnIndex int64 `json:"start_turn_index"`
	EndTurnIndex   int64 `json:"end_turn_index"`
	TurnCount      int   `json:"turn_count"`

	// StartedAt 은 첫 턴의 시작(UTC 초)이다. 알 수 없으면 0.
	StartedAt   int64 `json:"started_at"`
	DurationSec int64 `json:"duration_sec"`
	// SharePermille 은 세션 전체 대비 이 단계의 비율(천분율)이다. 단계 전체의 합은 1000 이다.
	SharePermille int64 `json:"share_permille"`

	// Reason 은 이 단계가 그 유형인 근거다. 속한 턴들의 규칙 이름을 중복 없이 모은 것이다.
	Reason string `json:"reason"`
}

// WorkTypeShare 는 세션 안에서 작업 유형 하나가 차지하는 몫이다 — 「작업 유형 비율」.
type WorkTypeShare struct {
	WorkType      WorkType `json:"work_type"`
	DurationSec   int64    `json:"duration_sec"`
	TurnCount     int      `json:"turn_count"`
	PhaseCount    int      `json:"phase_count"`
	SharePermille int64    `json:"share_permille"`
}

// SessionClassification 은 세션 하나의 분류 결과 전부다.
type SessionClassification struct {
	SessionID int64 `json:"session_id"`
	// WorkType 은 세션의 주 작업 유형이다. 턴이 하나도 없으면 unknown 이다.
	WorkType WorkType `json:"work_type"`
	// WorkTypeReason 은 그 유형이 어떻게 뽑혔는지다. 동률로 우선순위가 개입했는지,
	// 시간을 몰라 턴 수로 갈랐는지가 여기 적힌다.
	WorkTypeReason string `json:"work_type_reason"`

	TurnCount        int   `json:"turn_count"`
	TotalDurationSec int64 `json:"total_duration_sec"`
	// DurationKnown 이 false 면 세션 전체 길이가 0 초라 비율과 주 유형을 **턴 수**로
	// 갈랐다는 뜻이다. turns.ended_at 이 비어 있고 활동 시각도 없는 세션이 그렇다.
	DurationKnown bool `json:"duration_known"`

	Turns  []TurnClass     `json:"turns"`
	Phases []Phase         `json:"phases"`
	Shares []WorkTypeShare `json:"shares"`
}

// ClassifyTurns 는 턴 신호 묶음을 세션 하나의 분류로 만든다. DB 도 시계도 건드리지 않는다.
//
// 인자 순서에 의존하지 않는다 — 안에서 (turn_index, turn_id) 로 다시 정렬한다. 호출자가
// 어떤 순서로 넘기든 같은 답이 나와야 결정론이 성립한다.
func ClassifyTurns(sessionID int64, turns []TurnSignals) SessionClassification {
	out := SessionClassification{
		SessionID: sessionID,
		WorkType:  WorkTypeUnknown,
		Turns:     []TurnClass{},
		Phases:    []Phase{},
		Shares:    []WorkTypeShare{},
	}

	ordered := sortedTurnSignals(turns)
	if len(ordered) == 0 {
		out.WorkTypeReason = "턴이 없다"
		return out
	}

	out.Turns = make([]TurnClass, 0, len(ordered))
	for i, t := range ordered {
		next := int64(0)
		if i+1 < len(ordered) {
			next = ordered[i+1].StartedAt
		}
		c := ClassifyTurn(t)
		c.StartedAt = t.StartedAt
		c.DurationSec = turnDurationSec(t, next)
		out.Turns = append(out.Turns, c)
		out.TotalDurationSec += c.DurationSec
	}
	out.TurnCount = len(out.Turns)
	out.DurationKnown = out.TotalDurationSec > 0

	out.Phases = buildPhases(out.Turns, out.DurationKnown)
	out.Shares = buildShares(out.Turns, out.Phases, out.DurationKnown)
	out.WorkType, out.WorkTypeReason = decideSessionWorkType(out.Shares, out.DurationKnown)
	return out
}

// sortedTurnSignals 는 입력을 (turn_index, turn_id) 오름차순으로 복사한다.
// 원본을 건드리지 않는 이유는 호출자의 슬라이스가 다른 곳에서도 쓰이기 때문이다.
func sortedTurnSignals(turns []TurnSignals) []TurnSignals {
	out := make([]TurnSignals, len(turns))
	copy(out, turns)
	sort.Slice(out, func(i, j int) bool {
		if out[i].TurnIndex != out[j].TurnIndex {
			return out[i].TurnIndex < out[j].TurnIndex
		}
		return out[i].TurnID < out[j].TurnID
	})
	return out
}

// turnDurationSec 는 턴 길이(초)다. 사다리는 파일 머리말에 적어 두었다.
// nextStart 가 0 이면 다음 턴이 없다는 뜻이다.
func turnDurationSec(t TurnSignals, nextStart int64) int64 {
	if t.StartedAt <= 0 {
		return 0
	}
	end := int64(0)
	switch {
	case t.EndedAt > 0:
		end = t.EndedAt
	case t.LastSeenAt > 0:
		end = t.LastSeenAt
	case nextStart > t.StartedAt:
		// 도구를 하나도 안 쓴 턴이다. 다음 턴이 시작할 때까지를 길이로 본다.
		end = nextStart
	}
	// 턴은 겹치지 않는다. 다음 턴이 이미 시작했으면 거기서 자른다.
	if nextStart > 0 && end > nextStart {
		end = nextStart
	}
	if end <= t.StartedAt {
		return 0
	}
	return end - t.StartedAt
}

// buildPhases 는 연속된 같은 분류의 턴을 하나로 묶는다.
func buildPhases(turns []TurnClass, durationKnown bool) []Phase {
	phases := []Phase{}
	for _, t := range turns {
		last := len(phases) - 1
		if last >= 0 && phases[last].WorkType == t.WorkType {
			if phases[last].StartedAt == 0 {
				phases[last].StartedAt = t.StartedAt
			}
			phases[last].EndTurnIndex = t.TurnIndex
			phases[last].TurnCount++
			phases[last].DurationSec += t.DurationSec
			phases[last].Reason = mergePhaseReason(phases[last].Reason, t)
			continue
		}
		phases = append(phases, Phase{
			Index:          len(phases),
			WorkType:       t.WorkType,
			StartTurnIndex: t.TurnIndex,
			EndTurnIndex:   t.TurnIndex,
			TurnCount:      1,
			StartedAt:      t.StartedAt,
			DurationSec:    t.DurationSec,
			Reason:         mergePhaseReason("", t),
		})
	}

	// 비율의 기준은 세션 전체 길이다. 길이를 하나도 모르면 턴 수로 대신 가른다 —
	// 0/0 을 그리는 것보다 "몇 턴이었나" 가 화면에 쓸모 있다.
	weights := make([]int64, len(phases))
	for i, p := range phases {
		if durationKnown {
			weights[i] = p.DurationSec
		} else {
			weights[i] = int64(p.TurnCount)
		}
	}
	for i, share := range permilleShares(weights) {
		phases[i].SharePermille = share
	}
	return phases
}

// maxPhaseReasonRules 는 단계 근거에 싣는 규칙 수 상한이다. 긴 단계는 규칙이 반복될 뿐
// 새 정보가 없다.
const maxPhaseReasonRules = 4

// mergePhaseReason 은 단계 근거에 턴의 규칙 이름을 중복 없이 더한다.
//
// 단계 유형과 같은 유형의 근거만 싣는다 — 그것이 이 단계를 그 유형으로 만든 근거다.
// unknown 단계는 그런 근거가 없으므로 있는 규칙을 그대로 싣는다.
func mergePhaseReason(reason string, t TurnClass) string {
	rules := strings.Split(reason, ", ")
	if reason == "" {
		rules = nil
	}
	if len(rules) > 0 && rules[len(rules)-1] == "..." {
		return reason
	}
	for _, e := range t.Evidence {
		if t.WorkType != WorkTypeUnknown && e.WorkType != t.WorkType {
			continue
		}
		if hasRuleName(rules, e.Rule) {
			continue
		}
		if len(rules) >= maxPhaseReasonRules {
			rules = append(rules, "...")
			break
		}
		rules = append(rules, e.Rule)
	}
	return strings.Join(rules, ", ")
}

// buildShares 는 작업 유형별 몫이다. 결과는 **우선순위 고정 순서**로 나온다 — 값으로
// 정렬하면 동률에서 순서가 흔들리고, 화면의 범례 색이 새로고침마다 자리를 바꾼다.
func buildShares(turns []TurnClass, phases []Phase, durationKnown bool) []WorkTypeShare {
	n := len(workTypeOrder)
	var (
		dur    = make([]int64, n)
		cnt    = make([]int, n)
		phaseN = make([]int, n)
		seen   = make([]bool, n)
	)
	for _, t := range turns {
		r := WorkTypeRank(t.WorkType)
		if r >= n {
			continue // 표에 없는 유형. 여기 올 일은 없지만 인덱스를 넘기지 않는다.
		}
		dur[r] += t.DurationSec
		cnt[r]++
		seen[r] = true
	}
	for _, p := range phases {
		if r := WorkTypeRank(p.WorkType); r < n {
			phaseN[r]++
		}
	}

	out := make([]WorkTypeShare, 0, n)
	weights := make([]int64, 0, n)
	for r, w := range workTypeOrder {
		if !seen[r] {
			continue
		}
		out = append(out, WorkTypeShare{
			WorkType:    w,
			DurationSec: dur[r],
			TurnCount:   cnt[r],
			PhaseCount:  phaseN[r],
		})
		if durationKnown {
			weights = append(weights, dur[r])
		} else {
			weights = append(weights, int64(cnt[r]))
		}
	}
	for i, share := range permilleShares(weights) {
		out[i].SharePermille = share
	}
	return out
}

// decideSessionWorkType 은 세션의 주 작업 유형이다.
//
// 규칙은 하나다 — **누적 시간이 가장 긴 분류**. 동률이면 혼합 턴과 같은 우선순위
// (디버깅 → 구현 → 검증 → 탐색)를 쓴다. shares 가 이미 우선순위 순서라 앞에서부터
// 훑으며 **더 큰 값에만** 자리를 내주면 그 규칙이 그대로 성립한다.
//
// durationKnown 이 false 면 무게가 턴 수다. 세션 길이를 하나도 모를 때만 벌어지는 일이고,
// 그때는 시간 규칙이 전부 0 초 동률로 무너져 우선순위 하나로 답이 정해진다 — 턴이 한 번뿐인
// 디버깅이 스무 턴짜리 탐색을 이기는 결과가 나온다. 무게를 바꿔 그것을 막는다.
func decideSessionWorkType(shares []WorkTypeShare, durationKnown bool) (WorkType, string) {
	if len(shares) == 0 {
		return WorkTypeUnknown, "턴이 없다"
	}

	weight := func(s WorkTypeShare) int64 {
		if durationKnown {
			return s.DurationSec
		}
		return int64(s.TurnCount)
	}

	best := shares[0]
	tied := false
	for _, s := range shares[1:] {
		switch {
		case weight(s) > weight(best):
			best, tied = s, false
		case weight(s) == weight(best):
			tied = true
		}
	}

	var b strings.Builder
	b.WriteString(string(best.WorkType))
	b.WriteString(" 누적 ")
	if durationKnown {
		b.WriteString(strconv.FormatInt(best.DurationSec, 10))
		b.WriteString("초")
	} else {
		b.WriteString(strconv.Itoa(best.TurnCount))
		b.WriteString("턴 (세션 길이 미상 → 턴 수 기준)")
	}
	b.WriteString(" · ")
	b.WriteString(strconv.Itoa(best.TurnCount))
	b.WriteString("턴 · ")
	b.WriteString(strconv.Itoa(best.PhaseCount))
	b.WriteString("단계")
	if tied {
		b.WriteString(" · 동률 → 우선순위")
	}
	return best.WorkType, b.String()
}

// permilleShares 는 무게를 천분율 정수로 배분한다.
//
// 최대잉여법이다. 내림한 뒤 남은 1 들을 **나머지가 큰 순서**로 하나씩 나눠 주고, 나머지가
// 같으면 앞선 항목이 가져간다. 부동소수도 맵 순회도 쓰지 않으므로 같은 무게가 언제나 같은
// 배분을 준다. 합은 항상 정확히 1000 이다 — 화면의 비율 막대에 빈틈이 남지 않는다.
//
// 무게 합이 0 이면 나눌 것이 없으므로 전부 0 이다.
func permilleShares(weights []int64) []int64 {
	out := make([]int64, len(weights))
	var total int64
	for _, w := range weights {
		if w > 0 {
			total += w
		}
	}
	if total <= 0 {
		return out
	}

	type remainder struct {
		index int
		rem   int64
	}
	rems := make([]remainder, 0, len(weights))
	var assigned int64
	for i, w := range weights {
		if w <= 0 {
			continue
		}
		base := w * 1000 / total
		out[i] = base
		assigned += base
		rems = append(rems, remainder{index: i, rem: w*1000 - base*total})
	}

	left := 1000 - assigned
	if left <= 0 {
		return out
	}
	sort.Slice(rems, func(i, j int) bool {
		if rems[i].rem != rems[j].rem {
			return rems[i].rem > rems[j].rem
		}
		return rems[i].index < rems[j].index
	})
	for i := 0; i < len(rems) && int64(i) < left; i++ {
		out[rems[i].index]++
	}
	return out
}
