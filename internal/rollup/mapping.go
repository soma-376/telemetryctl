package rollup

import (
	"strings"

	"github.com/your-org/pulsemetry/internal/event"
)

// mapping 은 OTel 시그널 이름 하나가 rollup_hourly 의 어느 컬럼으로 가는지 정의한다.
type mapping struct {
	// needsValue 가 true 면 Measure.Value 를 temporality 규칙으로 해석한 값이 v 로 들어온다.
	// false 면 "이벤트 1건 = 1회" 인 로그라 v 는 항상 1 이다. 로그 레코드에는 데이터포인트
	// 값이 없으므로 값을 요구하면 전부 UnusableValue 로 버려진다.
	needsValue bool
	// apply 는 기여분을 b 에 채운다. type·decision 같은 속성으로 컬럼을 고르는 매핑에서
	// 그 속성값을 모르면 false 를 돌려 Unmapped 로 남긴다 — 모르는 값을 아무 컬럼에나
	// 넣거나 조용히 0 으로 흘리는 것보다 카운트에 잡히는 편이 낫다.
	apply func(b *Bucket, e event.Event, v float64) bool
}

// mappings 는 시그널 이름 → 컬럼 매핑을 관리하는 유일한 지점이다.
// 이 표가 흩어지면 어느 컬럼이 어느 시그널에서 오는지 아무도 추적하지 못하고,
// 같은 수치가 두 경로로 들어와 조용히 배로 잡힌다.
//
// **컬럼 하나에는 출처가 하나뿐이다.** 특히:
//   - cost_usd 는 claude_code.cost.usage 메트릭만 채운다. claude_code.api_request 로그에도
//     cost_usd 가 실려 오지만 둘 다 더하면 정확히 2배가 된다 (리스크 표 "비용 10배").
//   - 토큰 4종은 claude_code.token.usage 메트릭만 채운다. 같은 이유로 api_request 로그의
//     토큰 필드는 여기서 쓰지 않는다 (세션 상세는 4단계 session 이 따로 다룬다).
//
// 여기 없는 이름은 집계되지 않고 Stats.Unmapped 로 센다. Codex 등 claude_code 이외 벤더의
// 시그널 이름은 3단계 otlpdecode 의 testdata 로 확정된 뒤에 이 표에 추가한다 —
// 추측으로 넣으면 틀린 컬럼에 조용히 쌓인다.
var mappings = map[string]mapping{
	// ── 메트릭 ────────────────────────────────────────────────────────────────
	"claude_code.session.count": {needsValue: true, apply: func(b *Bucket, _ event.Event, v float64) bool {
		b.SessionsStarted = roundInt(v)
		return true
	}},
	"claude_code.cost.usage": {needsValue: true, apply: func(b *Bucket, _ event.Event, v float64) bool {
		b.CostUSD = v
		return true
	}},
	"claude_code.active_time.total": {needsValue: true, apply: func(b *Bucket, _ event.Event, v float64) bool {
		b.ActiveSeconds = v
		return true
	}},
	"claude_code.commit.count": {needsValue: true, apply: func(b *Bucket, _ event.Event, v float64) bool {
		b.Commits = roundInt(v)
		return true
	}},
	"claude_code.pull_request.count": {needsValue: true, apply: func(b *Bucket, _ event.Event, v float64) bool {
		b.PullRequests = roundInt(v)
		return true
	}},
	// type: added | removed
	"claude_code.lines_of_code.count": {needsValue: true, apply: func(b *Bucket, e event.Event, v float64) bool {
		switch normToken(e.Attr.Type) {
		case "added":
			b.LinesAdded = roundInt(v)
		case "removed":
			b.LinesRemoved = roundInt(v)
		default:
			return false
		}
		return true
	}},
	// type: input | output | cacheRead | cacheCreation
	"claude_code.token.usage": {needsValue: true, apply: func(b *Bucket, e event.Event, v float64) bool {
		switch normToken(e.Attr.Type) {
		case "input":
			b.InputTokens = roundInt(v)
		case "output":
			b.OutputTokens = roundInt(v)
		case "cacheread":
			b.CacheReadTokens = roundInt(v)
		case "cachecreation":
			b.CacheCreationTokens = roundInt(v)
		default:
			return false
		}
		return true
	}},
	// decision: accept | reject.
	// 알려진 한계: 이 메트릭은 코드 편집 툴(Edit·MultiEdit·Write·NotebookEdit)만 다룬다.
	// claude_code.tool_decision 로그가 나머지 툴까지 덮지만 편집 툴 구간이 겹쳐 둘 다 세면
	// 편집 승인이 2배가 된다. 겹치지 않게 나눌 방법이 벤더 툴 목록 하드코딩뿐이라,
	// 지금은 정확한 부분집합만 세고 나머지는 세지 않는다.
	"claude_code.code_edit_tool.decision": {needsValue: true, apply: func(b *Bucket, e event.Event, v float64) bool {
		switch normToken(e.Attr.Decision) {
		case "accept", "accepted":
			b.ToolAccepts = roundInt(v)
		case "reject", "rejected":
			b.ToolRejects = roundInt(v)
		default:
			return false
		}
		return true
	}},

	// ── 로그 ─────────────────────────────────────────────────────────────────
	"claude_code.user_prompt": {apply: func(b *Bucket, _ event.Event, _ float64) bool {
		b.Prompts = 1
		return true
	}},
	"claude_code.tool_result": {apply: func(b *Bucket, _ event.Event, _ float64) bool {
		b.ToolCalls = 1
		return true
	}},
	"claude_code.api_request": {apply: func(b *Bucket, e event.Event, _ float64) bool {
		b.APIRequests = 1
		b.Retries = retryCount(e)
		return true
	}},
	"claude_code.api_error": {apply: func(b *Bucket, e event.Event, _ float64) bool {
		b.APIErrors = 1
		b.Retries = retryCount(e)
		return true
	}},
}

// retryCount 는 attempt 속성으로 재시도 1건을 센다.
// 한 요청의 시도 1,2,3 은 각각 api_request 또는 api_error 를 정확히 하나씩 만들므로
// "attempt >= 2 인 이벤트 수" 가 곧 재시도 횟수다. attempt 가 미설정이면 재시도 여부를
// 알 수 없다는 뜻이라 0 을 센다 — Opt 의 미설정을 1 로 보면 모든 요청이 재시도가 된다.
func retryCount(e event.Event) int64 {
	if n, ok := e.Measure.Attempt.Get(); ok && n >= 2 {
		return 1
	}
	return 0
}

// normToken 은 type·decision 속성값의 표기 흔들림을 흡수한다. Claude Code 는 cacheRead 로
// 보내지만 벤더·버전이 cache_read 로 보내도 같은 컬럼으로 가야 한다.
// 모르는 값은 여전히 매핑 실패로 남겨 Unmapped 카운트에 잡히게 한다.
func normToken(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_' || c == '-' || c == ' ':
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c - 'A' + 'a')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
