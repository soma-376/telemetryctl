package dashboard

// Totals 는 rollup_hourly 수치 컬럼의 합계다. Today 카드와 Breakdown 행이 함께 쓴다.
//
// 컬럼을 골라 담지 않고 전부 담는 이유는 화면이 여러 장이고 각 장이 다른 조합을 쓰기
// 때문이다 — Today 는 비용·토큰·세션·활동 시간, Insights 는 툴 수락/거부, Activity 는
// 라인 수를 본다. 한 벌로 주고 화면이 고르게 두는 편이 조회 메서드를 화면마다 늘리는 것보다
// 낫다.
//
// 필드 순서는 rollup.Bucket 및 rollup_hourly DDL 과 같다. rollupSumColumns 와
// totalsDest 가 이 순서에 의존한다.
type Totals struct {
	CostUSD             float64 `json:"cost_usd"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`

	APIRequests  int64 `json:"api_requests"`
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

// add 는 다른 합계를 누적한다. 시간대 버킷팅처럼 Go 쪽에서 다시 묶을 때 쓴다.
// 리플렉션 없이 나열해 컬럼이 늘 때 눈으로 걸리게 둔다 (rollup.Bucket.add 와 같은 이유).
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

// rollupSumColumns 는 집계 조회용 SELECT 목록이다. 대상 행이 하나도 없으면 SUM 이 NULL 을
// 주므로 COALESCE 가 필요하다 — 없으면 "오늘 아직 데이터 없음" 이 스캔 에러가 된다.
const rollupSumColumns = `COALESCE(SUM(cost_usd),0),
  COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
  COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_creation_tokens),0),
  COALESCE(SUM(api_requests),0), COALESCE(SUM(api_errors),0), COALESCE(SUM(retries),0),
  COALESCE(SUM(lines_added),0), COALESCE(SUM(lines_removed),0),
  COALESCE(SUM(commits),0), COALESCE(SUM(pull_requests),0), COALESCE(SUM(prompts),0),
  COALESCE(SUM(tool_calls),0), COALESCE(SUM(tool_accepts),0), COALESCE(SUM(tool_rejects),0),
  COALESCE(SUM(active_seconds),0), COALESCE(SUM(sessions_started),0)`

// rollupRawColumns 는 행 단위 조회용 목록이다. 순서가 rollupSumColumns 와 같아야
// totalsDest 하나로 둘 다 스캔할 수 있다.
const rollupRawColumns = `cost_usd,
  input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens,
  api_requests, api_errors, retries, lines_added, lines_removed,
  commits, pull_requests, prompts, tool_calls, tool_accepts, tool_rejects,
  active_seconds, sessions_started`

// totalsDest 는 위 두 목록에 대응하는 스캔 대상이다.
func totalsDest(t *Totals) []any {
	return []any{
		&t.CostUSD,
		&t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheCreationTokens,
		&t.APIRequests, &t.APIErrors, &t.Retries, &t.LinesAdded, &t.LinesRemoved,
		&t.Commits, &t.PullRequests, &t.Prompts, &t.ToolCalls, &t.ToolAccepts, &t.ToolRejects,
		&t.ActiveSeconds, &t.SessionsStarted,
	}
}
