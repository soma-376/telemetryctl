package pricing

// ─────────────────────────────────────────────────────────────────────────────
// ★ 사람이 검증해야 하는 숫자다 ★
//
// 아래 단가는 **공시 페이지를 직접 확인하지 않고 채운 값**이다. 이 패키지는 IO 를 하지
// 않으므로 실행 중에 단가를 확인할 방법이 없고, 표를 채운 시점에도 확인하지 못했다.
//
//   - Claude 계열: 2026-06-24 기준으로 알려진 입력·출력 단가를 옮겼다. 캐시 단가는
//     Anthropic 이 공시해 온 배수 규칙(캐시 쓰기 5분 TTL = 입력 × 1.25,
//     캐시 읽기 = 입력 × 0.1)으로 **유도한 값**이며 줄마다 확인하지 않았다.
//   - GPT/Codex 계열: 확인 시점이 더 오래됐고 근거가 약하다. 캐시 쓰기에 별도 요금이
//     없다는 전제로 입력 단가와 같게 뒀다(그래서 쓰기 절감액이 0 으로 나온다).
//
// **배포 전에 아래 두 곳과 한 줄씩 대조해야 한다.**
//
//   - https://www.anthropic.com/pricing  (Claude API 단가)
//   - https://openai.com/api/pricing/    (GPT·Codex 단가)
//
// 대조해서 고칠 때는 (1) 이 줄들을 고치고 (2) DefaultVersion 을 올리고
// (3) DefaultEffectiveDate 를 확인한 날짜로 바꾸고 (4) rates_test.go 의 고정값을 함께
// 고친다. 그래야 단가 변경이 리뷰 가능한 diff 로 남는다.
//
// 컨텍스트 구간별 프리미엄(예: 200K 초과 요청의 상향 단가)은 **다루지 않는다.**
// llm_calls 에 그 구분이 없어 판정할 수 없다 — 있는 척하는 것보다 없는 게 낫다.
// ─────────────────────────────────────────────────────────────────────────────

const (
	// DefaultVersion 은 기본 가격표의 판 번호다. 단가를 한 줄이라도 고치면 올린다.
	DefaultVersion = "v1"
	// DefaultEffectiveDate 는 이 단가의 확인 기준일이다. 위 경고대로 사람 검증 전이다.
	DefaultEffectiveDate = "2026-06-24"
)

// 단가는 100만 토큰당 USD 다.
//
// | 모델 계열          | 입력  | 출력  | 캐시 읽기 | 캐시 쓰기 |
// |--------------------|-------|-------|-----------|-----------|
// | Fable 5            | 10.00 | 50.00 |      1.00 |     12.50 |
// | Opus 5 · 4.8 · 4.7 |  5.00 | 25.00 |      0.50 |      6.25 |
// | Opus 4 · 4.1       | 15.00 | 75.00 |      1.50 |     18.75 |
// | Sonnet 5           |  2.00 | 10.00 |      0.20 |      2.50 |
// | Sonnet 4.x · 3.x   |  3.00 | 15.00 |      0.30 |      3.75 |
// | Haiku 4.5          |  1.00 |  5.00 |      0.10 |      1.25 |
var defaultRates = map[string]Rate{
	// ── Anthropic ────────────────────────────────────────────────────────────
	"claude-fable-5":  {InputPerMTokUSD: 10, OutputPerMTokUSD: 50, CacheReadPerMTokUSD: 1.00, CacheWritePerMTokUSD: 12.50},
	"claude-opus-5":   {InputPerMTokUSD: 5, OutputPerMTokUSD: 25, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 6.25},
	"claude-opus-4-8": {InputPerMTokUSD: 5, OutputPerMTokUSD: 25, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 6.25},
	"claude-opus-4-7": {InputPerMTokUSD: 5, OutputPerMTokUSD: 25, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 6.25},
	"claude-opus-4-6": {InputPerMTokUSD: 5, OutputPerMTokUSD: 25, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 6.25},
	"claude-opus-4-5": {InputPerMTokUSD: 5, OutputPerMTokUSD: 25, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 6.25},
	"claude-opus-4-1": {InputPerMTokUSD: 15, OutputPerMTokUSD: 75, CacheReadPerMTokUSD: 1.50, CacheWritePerMTokUSD: 18.75},
	"claude-opus-4":   {InputPerMTokUSD: 15, OutputPerMTokUSD: 75, CacheReadPerMTokUSD: 1.50, CacheWritePerMTokUSD: 18.75},
	"claude-3-opus":   {InputPerMTokUSD: 15, OutputPerMTokUSD: 75, CacheReadPerMTokUSD: 1.50, CacheWritePerMTokUSD: 18.75},

	"claude-sonnet-5":   {InputPerMTokUSD: 2, OutputPerMTokUSD: 10, CacheReadPerMTokUSD: 0.20, CacheWritePerMTokUSD: 2.50},
	"claude-sonnet-4-6": {InputPerMTokUSD: 3, OutputPerMTokUSD: 15, CacheReadPerMTokUSD: 0.30, CacheWritePerMTokUSD: 3.75},
	"claude-sonnet-4-5": {InputPerMTokUSD: 3, OutputPerMTokUSD: 15, CacheReadPerMTokUSD: 0.30, CacheWritePerMTokUSD: 3.75},
	"claude-sonnet-4":   {InputPerMTokUSD: 3, OutputPerMTokUSD: 15, CacheReadPerMTokUSD: 0.30, CacheWritePerMTokUSD: 3.75},
	"claude-3-7-sonnet": {InputPerMTokUSD: 3, OutputPerMTokUSD: 15, CacheReadPerMTokUSD: 0.30, CacheWritePerMTokUSD: 3.75},
	"claude-3-5-sonnet": {InputPerMTokUSD: 3, OutputPerMTokUSD: 15, CacheReadPerMTokUSD: 0.30, CacheWritePerMTokUSD: 3.75},

	"claude-haiku-4-5": {InputPerMTokUSD: 1, OutputPerMTokUSD: 5, CacheReadPerMTokUSD: 0.10, CacheWritePerMTokUSD: 1.25},
	"claude-3-5-haiku": {InputPerMTokUSD: 0.80, OutputPerMTokUSD: 4, CacheReadPerMTokUSD: 0.08, CacheWritePerMTokUSD: 1.00},
	"claude-3-haiku":   {InputPerMTokUSD: 0.25, OutputPerMTokUSD: 1.25, CacheReadPerMTokUSD: 0.03, CacheWritePerMTokUSD: 0.30},

	// ── OpenAI (Codex) ───────────────────────────────────────────────────────
	// 캐시 쓰기에 별도 요금이 없다는 전제로 입력 단가와 같게 뒀다. 이 전제가 틀리면
	// 쓰기 절감액이 0 이 아니라 음수여야 한다 — 검증 시 함께 확인할 것.
	"gpt-5.1":       {InputPerMTokUSD: 1.25, OutputPerMTokUSD: 10, CacheReadPerMTokUSD: 0.125, CacheWritePerMTokUSD: 1.25},
	"gpt-5.1-codex": {InputPerMTokUSD: 1.25, OutputPerMTokUSD: 10, CacheReadPerMTokUSD: 0.125, CacheWritePerMTokUSD: 1.25},
	"gpt-5":         {InputPerMTokUSD: 1.25, OutputPerMTokUSD: 10, CacheReadPerMTokUSD: 0.125, CacheWritePerMTokUSD: 1.25},
	"gpt-5-codex":   {InputPerMTokUSD: 1.25, OutputPerMTokUSD: 10, CacheReadPerMTokUSD: 0.125, CacheWritePerMTokUSD: 1.25},
	"gpt-5-mini":    {InputPerMTokUSD: 0.25, OutputPerMTokUSD: 2, CacheReadPerMTokUSD: 0.025, CacheWritePerMTokUSD: 0.25},
	"gpt-5-nano":    {InputPerMTokUSD: 0.05, OutputPerMTokUSD: 0.40, CacheReadPerMTokUSD: 0.005, CacheWritePerMTokUSD: 0.05},
	"gpt-4.1":       {InputPerMTokUSD: 2, OutputPerMTokUSD: 8, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 2},
	"gpt-4.1-mini":  {InputPerMTokUSD: 0.40, OutputPerMTokUSD: 1.60, CacheReadPerMTokUSD: 0.10, CacheWritePerMTokUSD: 0.40},
	"gpt-4.1-nano":  {InputPerMTokUSD: 0.10, OutputPerMTokUSD: 0.40, CacheReadPerMTokUSD: 0.025, CacheWritePerMTokUSD: 0.10},
	"o3":            {InputPerMTokUSD: 2, OutputPerMTokUSD: 8, CacheReadPerMTokUSD: 0.50, CacheWritePerMTokUSD: 2},
	"o4-mini":       {InputPerMTokUSD: 1.10, OutputPerMTokUSD: 4.40, CacheReadPerMTokUSD: 0.275, CacheWritePerMTokUSD: 1.10},
}

// defaultAliases 는 **같은 모델의 다른 이름**만 담는다. 비슷해 보이는 다른 모델을 여기
// 넣으면 그 순간 임의 매핑이 된다 — 모르는 모델은 별칭을 만들지 말고 그대로 두어
// unavailable 로 떨어뜨린다.
var defaultAliases = map[string]string{
	"claude-opus-4-0":   "claude-opus-4",
	"claude-sonnet-4-0": "claude-sonnet-4",
}

// defaultTable 은 패키지 기본 가격표다. NewTable 이 맵을 복사하므로 밖에서 바뀌지 않는다.
var defaultTable = NewTable(DefaultVersion, DefaultEffectiveDate, defaultRates, defaultAliases)

// Default 는 기본 가격표를 돌려준다. 호출자는 자기 표를 NewTable 로 만들어 쓸 수도 있다 —
// 조직이 별도 계약 단가를 쓰는 경우가 그렇다.
func Default() Table { return defaultTable }
