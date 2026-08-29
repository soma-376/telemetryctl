package dashboard

// Totals 는 한 구간·한 축의 지표 합계다. Today 카드와 Breakdown 행이 함께 쓴다.
//
// 컬럼을 골라 담지 않고 전부 담는 이유는 화면이 여러 장이고 각 장이 다른 조합을 쓰기
// 때문이다 — Today 는 비용·토큰·세션·활동 시간, Insights 는 툴 수락/거부, Activity 는
// 라인 수를 본다. 한 벌로 주고 화면이 고르게 두는 편이 조회 메서드를 화면마다 늘리는 것보다
// 낫다.
//
// # v3 에 출처가 없는 필드
//
// v3 는 rollup_hourly 를 두지 않고 승격 테이블(llm_calls · tool_calls · file_changes)을
// 조회 시점에 GROUP BY 한다 (ADR 0009). 그래서 아래 네 필드는 **어떤 행에서도 0 이 아닌
// 값을 받지 못한다.**
//
//	APIErrors    — v3 events 에 status_code · success 를 담는 자리가 없다
//	Retries      — 같은 이유로 attempt 가 저장되지 않는다
//	Commits      — 커밋 수를 실어 오는 이벤트가 승격 대상이 아니다
//	PullRequests — 같은 이유
//
// 필드를 지우지 않는 이유는 ADR 0009 가 abandoned·handoff 를 남긴 것과 같다 — 지우면
// GUI TypeScript 바인딩과 `stats --json` 출력이 깨진다. 산출하지 않는다는 사실을 여기
// 주석과 스키마 문서에 남긴다.
type Totals struct {
	CostUSD             float64 `json:"cost_usd"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`

	APIRequests int64 `json:"api_requests"`
	// APIErrors·Retries·Commits·PullRequests 는 v3 에 출처가 없어 항상 0 이다 (위 주석).
	APIErrors    int64 `json:"api_errors"`
	Retries      int64 `json:"retries"`
	LinesAdded   int64 `json:"lines_added"`
	LinesRemoved int64 `json:"lines_removed"`
	Commits      int64 `json:"commits"`
	PullRequests int64 `json:"pull_requests"`
	Prompts      int64 `json:"prompts"`
	ToolCalls    int64 `json:"tool_calls"`
	ToolAccepts  int64 `json:"tool_accepts"`
	ToolRejects  int64 `json:"tool_rejects"`

	ActiveSeconds   float64 `json:"active_seconds"`
	SessionsStarted int64   `json:"sessions_started"`
}

// Tokens 는 입력+출력 토큰이다. 캐시 토큰은 더하지 않는다 — cache_read 는 이미 한 번 센
// 입력을 다시 읽은 양이라 함께 더하면 "쓴 토큰" 이 실제보다 부풀어 보인다.
func (t Totals) Tokens() int64 { return t.InputTokens + t.OutputTokens }

// add 는 다른 합계를 누적한다. 승격 테이블마다 따로 집계한 부분 합계를 Go 에서 합칠 때와
// 시간대 버킷팅에서 쓴다. 리플렉션 없이 나열해 필드가 늘 때 눈으로 걸리게 둔다.
func (t *Totals) add(o Totals) {
	t.CostUSD += o.CostUSD
	t.InputTokens += o.InputTokens
	t.OutputTokens += o.OutputTokens
	t.CacheReadTokens += o.CacheReadTokens
	t.CacheCreationTokens += o.CacheCreationTokens

	t.APIRequests += o.APIRequests
	t.APIErrors += o.APIErrors
	t.Retries += o.Retries
	t.LinesAdded += o.LinesAdded
	t.LinesRemoved += o.LinesRemoved
	t.Commits += o.Commits
	t.PullRequests += o.PullRequests
	t.Prompts += o.Prompts
	t.ToolCalls += o.ToolCalls
	t.ToolAccepts += o.ToolAccepts
	t.ToolRejects += o.ToolRejects

	t.ActiveSeconds += o.ActiveSeconds
	t.SessionsStarted += o.SessionsStarted
}
