package otlpdecode

import (
	"encoding/json"
	"strings"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/your-org/pulsemetry/internal/event"
)

// lineBudget 은 LCS 에 허용하는 최대 셀 수다. 넘으면 미관측으로 둔다 — 이 계산은 수신
// 경로 안에서 도니까, 거대한 편집 하나가 큐를 밀리게 하는 것보다 숫자를 포기하는 편이 낫다.
const lineBudget = 4_000_000

// toolInputLines 는 Claude Code 의 tool_input 에서 추가·삭제 줄 수를 센다.
//
// 벤더는 줄 수를 주지 않는다. 주는 것은 편집 전후 원문이고(Edit 의 old_string·new_string,
// Write 의 content) 줄 수는 거기서만 나온다. 그래서 원문이 손에 있는 이 시점에 세고
// 숫자만 남긴다 — 원문은 여기서부터 스크럽 대상이다 (ADR 0003).
//
// 셀 수 없는 경우는 0 이 아니라 미관측이다. replace_all 편집이 그 경우다: 치환이 몇 번
// 일어났는지는 파일 원문이 있어야 알 수 있고 우리에게는 없다. 0 으로 적으면 "바뀐 게
// 없다" 는 뜻이 되어 집계가 조용히 틀린다 (dashboard 의 LineCount).
func toolInputLines(v *commonpb.AnyValue) (additions, deletions event.Opt[int64]) {
	s, ok := v.GetValue().(*commonpb.AnyValue_StringValue)
	if !ok {
		return additions, deletions
	}
	var in struct {
		Content    *string `json:"content"`
		OldString  *string `json:"old_string"`
		NewString  *string `json:"new_string"`
		ReplaceAll bool    `json:"replace_all"`
	}
	if json.Unmarshal([]byte(s.StringValue), &in) != nil {
		return additions, deletions
	}

	// Write 는 파일 전체를 쓴다. 덮어쓴 경우의 삭제 줄 수는 이전 내용이 있어야 알 수 있다.
	if in.Content != nil {
		return event.Some(int64(countLines(*in.Content))), deletions
	}

	if in.OldString == nil || in.NewString == nil || in.ReplaceAll {
		return additions, deletions
	}
	old, updated := splitLines(*in.OldString), splitLines(*in.NewString)
	if len(old)*len(updated) > lineBudget {
		return additions, deletions
	}
	common := commonLines(old, updated)
	return event.Some(int64(len(updated) - common)), event.Some(int64(len(old) - common))
}

// splitLines 는 줄 단위로 자른다. 마지막 줄바꿈은 줄을 하나 더 만들지 않는다.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

func countLines(s string) int { return len(splitLines(s)) }

// commonLines 는 두 줄 목록의 최장 공통 부분수열 길이다.
//
// 경로는 복원하지 않는다 — 추가·삭제 줄 수는 길이만으로 나오므로(len(new)-common,
// len(old)-common) 행 두 개면 충분하고, 큰 편집에서 O(n*m) 메모리를 쓰지 않는다.
func commonLines(old, updated []string) int {
	if len(old) == 0 || len(updated) == 0 {
		return 0
	}
	previous := make([]int, len(updated)+1)
	current := make([]int, len(updated)+1)
	for i := 1; i <= len(old); i++ {
		for j := 1; j <= len(updated); j++ {
			if old[i-1] == updated[j-1] {
				current[j] = previous[j-1] + 1
				continue
			}
			current[j] = max(previous[j], current[j-1])
		}
		previous, current = current, previous
	}
	return previous[len(updated)]
}
