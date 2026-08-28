package dashboard

// 세션 상세 지표와 턴 집계 (PROJ-91).
//
// # 왜 sessions.go 와 따로 있는가
//
// SessionDetail(sessions.go) 은 "무엇이 일어났는가" 를 나열한다 — 파일 목록, 툴 타임라인,
// MCP 사용. 이 파일은 "얼마나 들었는가" 를 센다 — 소요 시간·토큰·툴 호출·비용·캐시·턴 수.
// 두 질문의 상한이 서로 다르다. 타임라인은 툴 호출 1000건에서 자르지만 지표는 턴 단위로
// 자르고, 잘려도 **상단 합계는 세션 전체를 덮어야** 한다.
//
// # 출처마다 따로 집계하고 Go 에서 합친다
//
// llm_calls · tool_calls · events 를 한 질의에 JOIN 으로 묶으면 행이 곱해져 모든 SUM 이
// 부풀어 오른다 — 도구 호출이 5건인 턴의 비용이 정확히 5배가 된다 (aggregate.go 의 같은
// 경고, store/promote.go 의 "비용 2배"). 그래서 출처마다 따로 묻고 turn_id 로 합친다.
// TestSessionMetricsDoesNotMultiplyAcrossSources 가 이 성질을 붙들고 있다.
//
// # 상단 값과 턴별 합계의 정합성
//
// 세션 상단의 셀 수 있는 값(SessionTotals.TurnTotals)은 **턴별 값을 접어 만든다.**
// 따로 SUM 질의를 돌려 두 숫자를 각각 만들지 않는다 — 그러면 언젠가 한쪽만 고쳐져
// 화면의 합계와 목록이 어긋나고, 사용자는 둘 다 믿지 않게 된다.
//
// 시각 값은 이 불변식의 대상이 **아니다.** 세션 소요 시간은 벽시계 길이라 턴 사이의
// 빈 시간을 포함하고, 턴 길이의 합과 같을 이유가 없다. 그래서 SessionTotals 안에 두지
// 않고 SessionMetrics 머리에 따로 둔다.
//
// # 비용은 internal/pricing 이 정한다
//
// llm_calls.cost_usd 를 그냥 SUM 하지 않는다. 보고 비용이 없는 호출은 그 합계에서 조용히
// 0 이 되고, 화면의 비용이 이유 없이 작아진다. pricing 이 "보고값 우선, 없으면 토큰 단가,
// 그것도 없으면 unavailable" 을 판정하고, 여기서는 그 결과를 정수 nano-USD 로 더한다
// (float 로 더하면 더하는 순서에 따라 합이 달라진다 — pricing/money.go).

import (
	"context"
	"database/sql"
	"errors"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/pricing"
)

const (
	// defaultSessionTurns 는 SessionMetrics 가 기본으로 돌려주는 턴 수다.
	defaultSessionTurns = 200
	// maxSessionTurns 는 응답 상한이다. 긴 세션은 턴이 수천 개이고 그것을 통째로 JSON 으로
	// 넘기면 화면이 멈춘다 — sessionTools 의 maxToolEvents 와 같은 이유다. 잘렸다는 사실은
	// TurnsTruncated 로 알리고, 상한 자체도 TurnLimit 으로 응답에 실어 보낸다. 조용히
	// 자르면 "턴이 왜 여기서 끊겼나" 를 아무도 설명하지 못한다.
	maxSessionTurns = 1000
)

// SessionMetricsQuery 는 세션 상세 지표 조회 조건이다.
type SessionMetricsQuery struct {
	// SessionID 는 sessions.id 다. Session() 과 같은 키이고, 벤더가 준 session_key 가
	// 아니다 — v3 에서 session_key 는 (vendor_id, session_key) 로만 고유하다.
	SessionID int64 `json:"session_id"`
	// TurnLimit 은 돌려줄 턴 수 상한이다. 0 이하면 기본값(200), maxSessionTurns 초과는
	// 상한으로 자른다. **상단 합계는 이 값과 무관하게 세션 전체를 덮는다.**
	TurnLimit int `json:"turn_limit"`
}

// TokenTotals 는 토큰 합계다. 값이 NULL 인 컬럼은 더하지 않는다 — 0 으로 눕히면
// "0 토큰을 쓴 호출" 과 "토큰을 보고하지 않은 호출" 이 같아진다.
type TokenTotals struct {
	Input     int64 `json:"input_tokens"`
	Output    int64 `json:"output_tokens"`
	CacheRead int64 `json:"cache_read_tokens"`
	// CacheWrite 는 llm_calls.cache_write_tokens 다. SessionRow 는 같은 값을
	// cache_creation_tokens 라는 v1 이름으로 내보낸다.
	CacheWrite int64 `json:"cache_write_tokens"`
	// Reasoning 은 **출력 토큰의 부분집합**이라 Billable 에 다시 더하지 않는다
	// (llm_calls 스키마 문서). 현재 쓰기 경로는 이 컬럼을 채우지 않는다.
	Reasoning int64 `json:"reasoning_tokens"`
}

// Billable 은 입력+출력이다. 캐시 토큰은 자기 단가로 따로 매겨지므로 더하지 않는다
// (Totals.Tokens 와 같은 규칙).
func (t TokenTotals) Billable() int64 { return t.Input + t.Output }

func (t *TokenTotals) add(o TokenTotals) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.Reasoning += o.Reasoning
}

// CostTotals 는 여러 호출의 비용 합계다.
//
// 금액 필드가 Total 하나뿐인 것은 pricing.Cost 와 같은 이유다 — 보고값과 추정값을 서로
// 다른 필드에 담아 두면 언젠가 누군가 둘 다 더한다. 어느 쪽이 몇 건이었는지는 아래 세 카운터가
// 말한다.
type CostTotals struct {
	Total pricing.Money `json:"total"`
	// ReportedCalls·EstimatedCalls·UnavailableCalls 는 pricing.Source 별 호출 수다.
	ReportedCalls    int64 `json:"reported_calls"`
	EstimatedCalls   int64 `json:"estimated_calls"`
	UnavailableCalls int64 `json:"unavailable_calls"`
	// Complete 은 비용을 정하지 못한 호출이 하나도 없었다는 뜻이다. false 면 Total 은
	// **하한**이고, 화면은 그 사실을 함께 그려야 한다. 호출이 아예 없으면 true 다.
	Complete bool `json:"complete"`
}

func (c *CostTotals) add(o CostTotals) {
	c.Total.NanoUSD += o.Total.NanoUSD
	c.ReportedCalls += o.ReportedCalls
	c.EstimatedCalls += o.EstimatedCalls
	c.UnavailableCalls += o.UnavailableCalls
}

func (c *CostTotals) finalize() {
	c.Total = nanoMoney(c.Total.NanoUSD)
	c.Complete = c.UnavailableCalls == 0
}

// SavingsTotals 는 캐시 절감액 합계다. **비용이 아니다** — 어떤 비용 합계에도 더하지
// 않는다 (pricing/savings.go).
type SavingsTotals struct {
	// Read 는 캐시 읽기로 아낀 금액, Write 는 캐시 쓰기의 차액이다. 쓰기 단가가 입력보다
	// 비싼 벤더에서는 Write 가 **음수**이고 그것은 오류가 아니다.
	Read  pricing.Money `json:"read"`
	Write pricing.Money `json:"write"`
	Total pricing.Money `json:"total"`

	AvailableCalls   int64 `json:"available_calls"`
	UnavailableCalls int64 `json:"unavailable_calls"`
	// Complete 은 절감액을 계산하지 못한 호출이 하나도 없었다는 뜻이다 (모르는 모델 등).
	Complete bool `json:"complete"`
}

func (s *SavingsTotals) add(o SavingsTotals) {
	s.Read.NanoUSD += o.Read.NanoUSD
	s.Write.NanoUSD += o.Write.NanoUSD
	s.Total.NanoUSD += o.Total.NanoUSD
	s.AvailableCalls += o.AvailableCalls
	s.UnavailableCalls += o.UnavailableCalls
}

func (s *SavingsTotals) finalize() {
	s.Read = nanoMoney(s.Read.NanoUSD)
	s.Write = nanoMoney(s.Write.NanoUSD)
	s.Total = nanoMoney(s.Total.NanoUSD)
	s.Complete = s.UnavailableCalls == 0
}

// nanoMoney 는 정수 nano-USD 에서 표시용 USD 를 파생한다. 합산은 항상 NanoUSD 로 하고
// USD 는 마지막에 한 번만 만든다 (pricing/money.go).
func nanoMoney(n pricing.NanoUSD) pricing.Money {
	return pricing.Money{NanoUSD: n, USD: n.USD()}
}

// TurnTotals 는 턴 하나의 **셀 수 있는** 지표다.
//
// 세션 상단의 같은 값은 이 구조체를 접어서 만든다. 그래서 필드를 추가할 때 add 와
// finalize 만 고치면 상단 값과 턴별 합계가 자동으로 함께 움직인다 — 두 곳에서 따로
// 세면 언젠가 갈린다.
type TurnTotals struct {
	// LLMCalls·ToolCalls 는 티켓이 요구하는 「턴별 LLM 호출과 툴 호출 합계」다.
	LLMCalls  int64 `json:"llm_calls"`
	ToolCalls int64 `json:"tool_calls"`
	// ToolErrors 는 success = 0 인 호출, ToolRejects 는 decision = 'reject' 인 호출이다.
	// success 가 NULL 인 호출(결정만 있고 결과가 없는 것)은 실패가 아니다.
	ToolErrors  int64 `json:"tool_errors"`
	ToolRejects int64 `json:"tool_rejects"`
	// Retries 는 **이벤트 payload 가 명시한** 재시도 횟수다. 같은 도구가 반복됐다는
	// 이유로 추측하지 않는다 (session_retry.go).
	Retries int64 `json:"retries"`

	// LLMDurationMS·ToolDurationMS 는 NULL 이 아닌 duration_ms 만 더한 값이다.
	// 관측되지 않은 호출은 0 을 보태지 않고 빠진다.
	LLMDurationMS  int64 `json:"llm_duration_ms"`
	ToolDurationMS int64 `json:"tool_duration_ms"`

	Tokens       TokenTotals   `json:"tokens"`
	Cost         CostTotals    `json:"cost"`
	CacheSavings SavingsTotals `json:"cache_savings"`
}

func (t *TurnTotals) add(o TurnTotals) {
	t.LLMCalls += o.LLMCalls
	t.ToolCalls += o.ToolCalls
	t.ToolErrors += o.ToolErrors
	t.ToolRejects += o.ToolRejects
	t.Retries += o.Retries
	t.LLMDurationMS += o.LLMDurationMS
	t.ToolDurationMS += o.ToolDurationMS
	t.Tokens.add(o.Tokens)
	t.Cost.add(o.Cost)
	t.CacheSavings.add(o.CacheSavings)
}

// finalize 는 누적이 끝난 뒤 파생값(표시용 USD, Complete)을 한 번 만든다.
// 누적 중에 만들면 중간 합계마다 다시 계산하게 되고, 무엇보다 Complete 이 마지막
// 호출까지 반영됐는지 보장할 수 없다.
func (t *TurnTotals) finalize() {
	t.Cost.finalize()
	t.CacheSavings.finalize()
}

// SessionTotals 는 세션 상단 값이다.
//
// TurnTotals 를 임베드해 JSON 에서 평평하게 펼쳐지고, 그 부분은 **정확히 턴별 값의 합**이다
// (TestSessionMetricsTopLineEqualsTurnSum).
type SessionTotals struct {
	// TurnCount 는 세션의 모든 턴 수다. 가상 턴(turn_index IS NULL)을 포함한다 —
	// 세션 수준 이벤트가 귀속되는 자리라 그 안의 호출도 세션 비용에 든다.
	TurnCount int64 `json:"turn_count"`
	// PromptTurns 는 실제 턴 수다(turn_index IS NOT NULL). 사용자 프롬프트 수와 같다.
	PromptTurns int64 `json:"prompt_turns"`
	TurnTotals
}

// TurnMetrics 는 턴 하나의 지표다.
type TurnMetrics struct {
	// TurnID 는 turns.id 다.
	TurnID int64 `json:"turn_id"`
	// TurnKey 는 벤더가 준 턴 식별자다 (Claude Code prompt.id, Codex 합성 키).
	TurnKey string `json:"turn_key"`
	// TurnIndex 는 실제 턴 순서다. null 이면 가상 턴이다.
	TurnIndex *int64 `json:"turn_index"`
	// Virtual 은 TurnIndex 가 null 이라는 뜻이다. 화면이 포인터를 풀지 않아도 되게 둔다.
	Virtual bool `json:"virtual"`

	StartedAt *int64 `json:"started_at"`
	EndedAt   *int64 `json:"ended_at"`
	// DurationSeconds 는 두 시각을 다 아는 턴에서만 값이 있다. 모르는 것을 0 으로 눕히면
	// "즉시 끝난 턴" 과 구분되지 않는다.
	DurationSeconds *int64 `json:"duration_seconds"`
	// TTFTMS 는 Codex 의 time-to-first-token 이다. 관측되지 않으면 null 이다.
	TTFTMS *int64 `json:"ttft_ms"`

	TurnTotals
}

// SessionMetrics 는 세션 상세 화면의 지표 한 장이다.
type SessionMetrics struct {
	// Found 가 false 면 그 id 가 없다는 뜻이다. **에러가 아니다** — 보존 정책(400일)이
	// 지운 세션의 id 를 화면이 아직 들고 있는 것은 정상이고, 그때 앱이 에러 토스트를
	// 띄울 이유가 없다 (Session() 과 같은 계약).
	Found bool `json:"found"`

	SessionID     int64  `json:"session_id"`
	SessionKey    string `json:"session_key"`
	Vendor        string `json:"vendor"`
	Title         string `json:"title"`
	WorkspacePath string `json:"workspace_path"`
	ProjectName   string `json:"project_name"`
	// Status 는 running 또는 completed 다. v3 에는 status 컬럼이 없어 조회 시점에
	// 계산한다 (ADR 0009, statusExpr).
	Status string `json:"status"`

	// StartedAt·EndedAt 은 관측되지 않았으면 null 이다. EndedAt 이 null 이면 진행 중이다.
	StartedAt *int64 `json:"started_at"`
	EndedAt   *int64 `json:"ended_at"`
	// LastActivityAt 은 마지막으로 알려진 활동 시각이다 (lastActivityExpr).
	LastActivityAt int64 `json:"last_activity_at"`
	// DurationSeconds 는 세션 소요 시간이다. 진행 중이면 마지막 활동까지를 길이로 본다 —
	// 그래야 화면의 값이 멈추지 않는다 (durationMS 와 같은 규칙). 시작 시각을 모르면 null.
	//
	// **턴 길이의 합이 아니다.** 턴 사이의 빈 시간을 포함하는 벽시계 길이다.
	DurationSeconds *int64 `json:"duration_seconds"`
	// ActiveSeconds 는 sessions.active_time_sec 다. 한 번도 관측되지 않으면 null 이며
	// 0초와 다르다 (sessions 스키마 문서).
	ActiveSeconds *int64 `json:"active_seconds"`

	// Totals 의 TurnTotals 부분은 **Turns 가 잘려도 세션 전체**를 덮는다.
	Totals SessionTotals `json:"totals"`
	Turns  []TurnMetrics `json:"turns"`

	// TurnLimit 은 이 응답에 적용한 상한이다. 상한을 응답에 실어 보내야 화면이
	// "1000개 중 200개" 를 말할 수 있다.
	TurnLimit int `json:"turn_limit"`
	// TurnsTruncated 는 턴 목록이 TurnLimit 에서 잘렸다는 뜻이다. 전체 턴 수는
	// Totals.TurnCount 에 있다.
	TurnsTruncated bool `json:"turns_truncated"`

	// PricingTableVersion·PricingEffectiveDate 는 비용·절감액을 계산한 가격표의 판이다.
	// 화면에 뜬 금액이 어느 판에서 나왔는지 되짚을 수 있어야 한다 (pricing.Applied).
	PricingTableVersion  string `json:"pricing_table_version"`
	PricingEffectiveDate string `json:"pricing_effective_date"`
}

// SessionMetrics 는 세션 하나의 지표와 턴별 집계다.
//
// 없는 id 는 에러가 아니라 Found=false 다. DB 가 없어도(미설치) 마찬가지다 (ADR 0004).
func (r *Reader) SessionMetrics(ctx context.Context, q SessionMetricsQuery) (SessionMetrics, error) {
	table := pricing.Default()
	out := SessionMetrics{
		Turns:                []TurnMetrics{},
		TurnLimit:            clampLimit(q.TurnLimit, defaultSessionTurns, maxSessionTurns),
		PricingTableVersion:  table.Version,
		PricingEffectiveDate: table.EffectiveDate,
	}

	db, ok := r.db()
	if !ok || q.SessionID <= 0 {
		return out, nil
	}

	head, found, err := sessionMetricsHead(ctx, db, q.SessionID)
	if err != nil {
		return SessionMetrics{}, err
	}
	if !found {
		return out, nil
	}
	head.apply(&out)

	index, err := sessionTurnMetrics(ctx, db, q.SessionID)
	if err != nil {
		return SessionMetrics{}, err
	}
	// 출처마다 따로 묻고 turn_id 로 합친다. JOIN 하나로 묶으면 행이 곱해진다 (머리말).
	for _, collect := range []func(context.Context, sqlQuerier, int64, pricing.Table, *turnIndex) error{
		collectLLMCalls, collectToolCalls, collectRetries,
	} {
		if err := collect(ctx, db, q.SessionID, table, index); err != nil {
			return SessionMetrics{}, err
		}
	}

	out.Totals = foldTurns(index.turns)
	out.Turns, out.TurnsTruncated = capTurns(index.turns, out.TurnLimit)
	return out, nil
}

// foldTurns 는 턴별 값을 접어 세션 상단 값을 만든다. **상단 값의 유일한 출처다** —
// 따로 SUM 질의를 두지 않는 이유는 머리말에 있다.
func foldTurns(turns []*TurnMetrics) SessionTotals {
	var out SessionTotals
	for _, t := range turns {
		out.TurnCount++
		if !t.Virtual {
			out.PromptTurns++
		}
		t.finalize()
		out.TurnTotals.add(t.TurnTotals)
	}
	out.TurnTotals.finalize()
	return out
}

// capTurns 는 응답 상한을 적용한다. 상단 값은 이미 전체 턴에서 만들어졌으므로 여기서
// 잘리는 것은 목록뿐이다.
func capTurns(turns []*TurnMetrics, limit int) ([]TurnMetrics, bool) {
	truncated := len(turns) > limit
	if truncated {
		turns = turns[:limit]
	}
	out := make([]TurnMetrics, 0, len(turns))
	for _, t := range turns {
		out = append(out, *t)
	}
	return out, truncated
}

// ── 세션 머리 ────────────────────────────────────────────────────────────────

// sessionHeadSQL 은 지표가 아닌 세션 자체의 값이다. 상태·마지막 활동 계산식은
// 목록·상세와 같은 것을 쓴다 (sessions.go) — 두 화면이 다른 식을 쓰면 같은 세션이
// 화면마다 다른 상태로 보인다.
var sessionHeadSQL = `SELECT s.session_key, s.vendor_id, COALESCE(s.title,''),
  COALESCE(s.workspace_path,''), ` + statusExpr + `,
  s.started_at, s.ended_at, ` + lastActivityExpr + `, s.active_time_sec
FROM sessions s WHERE s.id = ?`

// sessionHead 는 nullable 시각 컬럼을 포인터로 옮기기 전의 원본이다.
type sessionHead struct {
	id            int64
	sessionKey    string
	vendor        string
	title         string
	workspacePath string
	status        string

	started      sql.NullInt64
	ended        sql.NullInt64
	lastActivity int64
	activeSec    sql.NullInt64
}

func sessionMetricsHead(ctx context.Context, db sqlQuerier, id int64) (sessionHead, bool, error) {
	const op = "세션 지표 조회"
	h := sessionHead{id: id}
	err := db.QueryRowContext(ctx, sessionHeadSQL, id).Scan(
		&h.sessionKey, &h.vendor, &h.title, &h.workspacePath, &h.status,
		&h.started, &h.ended, &h.lastActivity, &h.activeSec)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// 없거나 보존이 지운 세션이다. 에러가 아니다.
		return sessionHead{}, false, nil
	case err != nil:
		return sessionHead{}, false, queryErr(op, err)
	}
	return h, true, nil
}

func (h sessionHead) apply(m *SessionMetrics) {
	m.Found = true
	m.SessionID = h.id
	m.SessionKey = h.sessionKey
	m.Vendor = h.vendor
	m.Title = h.title
	m.WorkspacePath = h.workspacePath
	m.ProjectName = baseName(h.workspacePath)
	m.Status = h.status

	m.StartedAt = nullInt64(h.started)
	m.EndedAt = nullInt64(h.ended)
	m.LastActivityAt = h.lastActivity
	m.ActiveSeconds = nullInt64(h.activeSec)
	m.DurationSeconds = h.durationSeconds()
}

// durationSeconds 는 세션 벽시계 길이다. 진행 중인 세션은 마지막 활동까지를 길이로 본다.
func (h sessionHead) durationSeconds() *int64 {
	if !h.started.Valid {
		return nil
	}
	end := h.lastActivity
	if h.ended.Valid {
		end = h.ended.Int64
	}
	if end <= h.started.Int64 {
		return nil
	}
	d := end - h.started.Int64
	return &d
}

// ── 턴 ──────────────────────────────────────────────────────────────────────

// turnIndex 는 turn_id 로 턴 칸을 찾는 색인이다.
//
// 색인에 없는 turn_id 를 만나면 **버리지 않고 새 칸을 만든다.** 외래 키가 있으니 일어날
// 수 없는 일이지만, 만약 일어난다면 조용히 버리는 쪽이 훨씬 나쁘다 — 상단 합계가 그만큼
// 줄어드는데 화면에는 아무 흔적도 남지 않는다.
type turnIndex struct {
	byID map[int64]*TurnMetrics
	// turns 는 화면 순서를 지키는 목록이다. 색인은 이 목록의 원소를 가리킨다.
	turns []*TurnMetrics
}

func (x *turnIndex) at(turnID int64) *TurnMetrics {
	if t, ok := x.byID[turnID]; ok {
		return t
	}
	t := &TurnMetrics{TurnID: turnID, Virtual: true}
	x.byID[turnID] = t
	x.turns = append(x.turns, t)
	return t
}

// sessionTurnsSQL 은 세션의 턴을 화면 순서로 읽는다.
//
// 실제 턴이 turn_index 오름차순으로 먼저 오고 가상 턴이 마지막이다 — 가상 턴은 순서가
// 없는 세션 수준 이벤트의 자리라 타임라인 중간에 끼워 넣을 지점이 없다.
const sessionTurnsSQL = `SELECT t.id, t.turn_key, t.turn_index, t.started_at, t.ended_at, t.ttft_ms
FROM turns t WHERE t.session_id = ?
ORDER BY (t.turn_index IS NULL) ASC, t.turn_index ASC, t.id ASC`

func sessionTurnMetrics(ctx context.Context, db sqlQuerier, id int64) (index *turnIndex, err error) {
	const op = "턴 목록 조회"
	rows, err := db.QueryContext(ctx, sessionTurnsSQL, id)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	index = &turnIndex{byID: map[int64]*TurnMetrics{}, turns: []*TurnMetrics{}}
	for rows.Next() {
		var (
			t                         TurnMetrics
			idx, started, ended, ttft sql.NullInt64
		)
		if serr := rows.Scan(&t.TurnID, &t.TurnKey, &idx, &started, &ended, &ttft); serr != nil {
			return nil, queryErr(op, serr)
		}
		t.TurnIndex = nullInt64(idx)
		t.Virtual = !idx.Valid
		t.StartedAt = nullInt64(started)
		t.EndedAt = nullInt64(ended)
		t.TTFTMS = nullInt64(ttft)
		t.DurationSeconds = spanSeconds(started, ended)

		turn := t
		index.byID[turn.TurnID] = &turn
		index.turns = append(index.turns, &turn)
	}
	return index, nil
}

// spanSeconds 는 두 nullable 시각의 길이다. 하나라도 모르면 null 이고, 끝이 시작보다
// 앞서면(시계가 뒤로 간 경우) 역시 null 이다 — 음수 길이를 화면에 올릴 이유가 없다.
func spanSeconds(start, end sql.NullInt64) *int64 {
	if !start.Valid || !end.Valid || end.Int64 < start.Int64 {
		return nil
	}
	d := end.Int64 - start.Int64
	return &d
}

// ── LLM 호출 ────────────────────────────────────────────────────────────────

// metricsTurnScope 는 인자 하나(session id)로 좁히는 턴 집합이다.
const metricsTurnScope = `SELECT id FROM turns WHERE session_id = ?`

// llmCallsSQL 은 **행 단위로** 읽는다. SUM 하지 않는 이유는 비용을 pricing 이 호출마다
// 판정하기 때문이다 — 보고 비용이 있는 호출과 토큰 단가로 추정하는 호출이 한 세션에
// 섞이고, 그 판정은 행을 봐야 할 수 있다.
const llmCallsSQL = `SELECT c.turn_id, COALESCE(c.model,''),
  c.input_tokens, c.output_tokens, c.cache_read_tokens, c.cache_write_tokens,
  c.reasoning_tokens, c.cost_usd, c.duration_ms
FROM llm_calls c WHERE c.turn_id IN (` + metricsTurnScope + `)`

func collectLLMCalls(ctx context.Context, db sqlQuerier, id int64, table pricing.Table, index *turnIndex) (err error) {
	const op = "LLM 호출 지표 조회"
	rows, err := db.QueryContext(ctx, llmCallsSQL, id)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var (
			turnID                                    int64
			model                                     string
			in, out, cacheRead, cacheWrite, reasoning sql.NullInt64
			duration                                  sql.NullInt64
			cost                                      sql.NullFloat64
		)
		if serr := rows.Scan(&turnID, &model, &in, &out, &cacheRead, &cacheWrite,
			&reasoning, &cost, &duration); serr != nil {
			return queryErr(op, serr)
		}

		t := index.at(turnID)
		t.LLMCalls++
		addOptTokens(&t.Tokens, in, out, cacheRead, cacheWrite, reasoning)
		if duration.Valid && duration.Int64 > 0 {
			t.LLMDurationMS += duration.Int64
		}

		res := table.Estimate(pricing.Usage{
			Model:            model,
			InputTokens:      optInt64(in),
			OutputTokens:     optInt64(out),
			CacheReadTokens:  optInt64(cacheRead),
			CacheWriteTokens: optInt64(cacheWrite),
			ReasoningTokens:  optInt64(reasoning),
			ReportedCostUSD:  optFloat64(cost),
		})
		applyCost(&t.Cost, res.Cost)
		applySavings(&t.CacheSavings, res.CacheSavings)
	}
	return nil
}

// addOptTokens 는 NULL 이 아닌 토큰만 더한다. 음수는 세지 않는다 — 벤더가 음수를 보고할
// 이유는 없지만, 그대로 더하면 다른 호출의 정상 토큰까지 깎인다 (pricing 의 tokens 와 같은 규칙).
func addOptTokens(t *TokenTotals, in, out, cacheRead, cacheWrite, reasoning sql.NullInt64) {
	t.Input += positive(in)
	t.Output += positive(out)
	t.CacheRead += positive(cacheRead)
	t.CacheWrite += positive(cacheWrite)
	t.Reasoning += positive(reasoning)
}

func positive(n sql.NullInt64) int64 {
	if !n.Valid || n.Int64 < 0 {
		return 0
	}
	return n.Int64
}

// optInt64 는 SQL 의 NULL 을 event.Opt 의 "없음" 으로 옮긴다. 0 으로 눕히면 pricing 이
// "0 토큰을 보고한 호출" 로 읽어 unavailable 판정이 뒤집힌다.
func optInt64(n sql.NullInt64) event.Opt[int64] {
	if !n.Valid {
		return event.Opt[int64]{}
	}
	return event.Some(n.Int64)
}

func optFloat64(n sql.NullFloat64) event.Opt[float64] {
	if !n.Valid {
		return event.Opt[float64]{}
	}
	return event.Some(n.Float64)
}

// applyCost 는 호출 한 건의 비용을 누적한다. 금액은 정수 nano 로만 더한다.
func applyCost(into *CostTotals, c pricing.Cost) {
	switch c.Source {
	case pricing.SourceReported:
		into.ReportedCalls++
	case pricing.SourceEstimated:
		into.EstimatedCalls++
	default:
		// 비용을 정할 수 없었다. 0 을 더하는 대신 세어 두고 Complete 을 내린다 —
		// 조용히 0 으로 두면 합계가 왜 작은지 화면이 설명하지 못한다.
		into.UnavailableCalls++
		return
	}
	into.Total.NanoUSD += c.Total.NanoUSD
}

func applySavings(into *SavingsTotals, s pricing.Savings) {
	if !s.Available {
		into.UnavailableCalls++
		return
	}
	into.AvailableCalls++
	into.Read.NanoUSD += s.Read.NanoUSD
	into.Write.NanoUSD += s.Write.NanoUSD
	into.Total.NanoUSD += s.Total.NanoUSD
}

// ── 도구 호출 ────────────────────────────────────────────────────────────────

// toolCallsSQL 은 턴별로 미리 접어서 읽는다. LLM 과 달리 행마다 판정할 것이 없다.
//
// success 가 NULL 인 호출은 실패가 아니다 — 결정만 있고 결과가 없는 호출(거부된 편집)이
// 그 경우다. SUM(duration_ms) 는 NULL 인 행을 건너뛰므로 모르는 시간이 0 으로 섞이지 않고,
// 전부 NULL 이면 결과 자체가 NULL 이라 NullInt64 로 받는다.
const toolCallsSQL = `SELECT c.turn_id, COUNT(*),
  COALESCE(SUM(CASE WHEN c.success = 0 THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN c.decision = 'reject' THEN 1 ELSE 0 END),0),
  SUM(c.duration_ms)
FROM tool_calls c WHERE c.turn_id IN (` + metricsTurnScope + `)
GROUP BY c.turn_id`

func collectToolCalls(ctx context.Context, db sqlQuerier, id int64, _ pricing.Table, index *turnIndex) (err error) {
	const op = "도구 호출 지표 조회"
	rows, err := db.QueryContext(ctx, toolCallsSQL, id)
	if err != nil {
		return queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var (
			turnID                   int64
			calls, failures, rejects int64
			duration                 sql.NullInt64
		)
		if serr := rows.Scan(&turnID, &calls, &failures, &rejects, &duration); serr != nil {
			return queryErr(op, serr)
		}
		t := index.at(turnID)
		t.ToolCalls += calls
		t.ToolErrors += failures
		t.ToolRejects += rejects
		t.ToolDurationMS += positive(duration)
	}
	return nil
}

// ── GUI 서비스 ──────────────────────────────────────────────────────────────

// SessionMetrics 는 세션 하나의 지표와 턴별 집계다 (Reader.SessionMetrics 를 그대로 감싼다).
//
// service.go 가 아니라 여기 있는 이유는 이 메서드의 계약이 위 타입들과 함께 움직이기
// 때문이다. Wails 서비스가 감싸는 표면이라는 점은 다른 조회 메서드와 같다 (ADR 0004).
func (s *Service) SessionMetrics(ctx context.Context, q SessionMetricsQuery) (SessionMetrics, error) {
	s.reconnect()
	return s.reader.SessionMetrics(ctx, q)
}
