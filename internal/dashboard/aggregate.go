package dashboard

import (
	"context"
	"sort"
)

// v3 에는 rollup_hourly 가 없다. 시간 버킷 집계는 저장 시점이 아니라 **조회 시점
// GROUP BY** 로 만든다 (ADR 0009). 이 파일이 그 집계기다.
//
// # 왜 승격 테이블마다 질의를 나누는가
//
// 한 지표에는 출처가 하나뿐이다. 비용·토큰은 llm_calls, 도구 수락/거부는 tool_calls,
// 라인 수는 file_changes, 프롬프트 수는 turns, 세션·활동 시간은 sessions 에서 온다.
// 이것들을 한 질의에 JOIN 으로 묶으면 행이 곱해져 **모든 SUM 이 부풀어 오른다** —
// 도구 호출이 5건인 턴의 비용이 정확히 5배가 되는 식이다. store/promote.go 가 경고한
// "비용 2배" 와 같은 종류의 사고이고, 조회 쪽에서는 되짚을 근거조차 남지 않는다.
//
// 그래서 출처마다 따로 집계하고 Go 에서 (키, 시간 버킷) 으로 합친다. 부분 합계가 서로
// 겹치는 필드를 쓰지 않으므로 Totals.add 로 그냥 더하면 된다.
//
// # 시간 버킷이 UTC 정시인 이유
//
// SQL 은 UTC 정시 버킷까지만 묶고 현지 시각·현지 날짜로의 귀속은 Go 가 한다. SQL 로
// 하려면 고정 오프셋을 박아야 하는데 그러면 DST 가 있는 시간대에서 전환일 전후가 한 시간씩
// 밀린다. time.Location 위에서 계산하면 그 문제가 없다 (timezone.go).

// hourSeconds 는 시간 버킷 하나의 길이(초)다.
const hourSeconds = 3600

// aggRow 는 (축 키, UTC 시간 버킷) 한 칸의 합계다.
type aggRow struct {
	Key  string
	Hour int64
	Totals
}

// factSource 는 승격 테이블 하나에서 뽑는 부분 합계의 정의다.
//
// cols 와 dest 의 순서가 서로 대응해야 한다. 어긋나면 스캔이 조용히 다른 필드로 들어간다.
type factSource struct {
	// op 는 오류 메시지에 쓰는 사람이 읽는 동작 이름이다. SQL 은 넣지 않는다 (queryErr).
	op string
	// from 은 FROM 절이다. 별칭은 s(sessions) · t(turns) · c(승격 테이블) 로 고정한다.
	from string
	// at 은 이 사실이 일어난 시각 컬럼 식(unix 초)이다. 시간 버킷과 구간 필터가 이것을 쓴다.
	at string
	// where 는 항상 붙는 추가 조건이다. 없으면 빈 문자열.
	where string
	cols  string
	dest  func(*Totals) []any
	// keys 는 축별 GROUP BY 식이다. 목록에 없는 축에는 이 출처가 기여하지 않는다 —
	// 예를 들어 모델별 집계에 도구 호출 수를 얹을 방법이 없다.
	keys map[Dim]string
}

// 축별 그룹 키 식. 벤더와 프로젝트는 모든 출처가 sessions 로 되짚을 수 있어 공통이다.
const (
	keyTotal     = `''`
	keyVendor    = `s.vendor_id`
	keyWorkspace = `COALESCE(s.workspace_path, '')`
)

// factSources 는 v3 의 다섯 출처다. 순서는 결과에 영향을 주지 않는다.
var factSources = []factSource{
	{
		op:   "LLM 호출 집계 조회",
		from: `llm_calls c JOIN turns t ON t.id = c.turn_id JOIN sessions s ON s.id = t.session_id`,
		at:   `c.called_at`,
		cols: `COALESCE(SUM(c.cost_usd),0), COALESCE(SUM(c.input_tokens),0),
		  COALESCE(SUM(c.output_tokens),0), COALESCE(SUM(c.cache_read_tokens),0),
		  COALESCE(SUM(c.cache_write_tokens),0), COUNT(*)`,
		dest: func(t *Totals) []any {
			return []any{
				&t.CostUSD, &t.InputTokens, &t.OutputTokens,
				&t.CacheReadTokens, &t.CacheCreationTokens, &t.APIRequests,
			}
		},
		keys: map[Dim]string{
			DimTotal: keyTotal, DimVendor: keyVendor,
			DimModel: `COALESCE(c.model, '')`, DimProject: keyWorkspace,
		},
	},
	{
		op:   "도구 호출 집계 조회",
		from: `tool_calls c JOIN turns t ON t.id = c.turn_id JOIN sessions s ON s.id = t.session_id`,
		at:   `c.called_at`,
		cols: `COUNT(*),
		  COALESCE(SUM(CASE WHEN c.decision = 'accept' THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN c.decision = 'reject' THEN 1 ELSE 0 END),0)`,
		dest: func(t *Totals) []any {
			return []any{&t.ToolCalls, &t.ToolAccepts, &t.ToolRejects}
		},
		keys: map[Dim]string{
			DimTotal: keyTotal, DimVendor: keyVendor,
			DimTool: `COALESCE(c.tool_name, '')`, DimProject: keyWorkspace,
		},
	},
	{
		op: "파일 변경 집계 조회",
		// 시각은 파일 변경 자체가 아니라 그것을 만든 도구 호출에서 온다 — file_changes 에는
		// 시각 컬럼이 없다.
		from: `file_changes f JOIN tool_calls c ON c.id = f.tool_call_id
		  JOIN turns t ON t.id = c.turn_id JOIN sessions s ON s.id = t.session_id`,
		at:   `c.called_at`,
		cols: `COALESCE(SUM(f.additions),0), COALESCE(SUM(f.deletions),0)`,
		dest: func(t *Totals) []any { return []any{&t.LinesAdded, &t.LinesRemoved} },
		keys: map[Dim]string{
			DimTotal: keyTotal, DimVendor: keyVendor,
			DimTool: `COALESCE(c.tool_name, '')`, DimProject: keyWorkspace,
		},
	},
	{
		op:   "프롬프트 수 집계 조회",
		from: `turns t JOIN sessions s ON s.id = t.session_id`,
		at:   `t.started_at`,
		// turn_index 가 NULL 인 턴은 세션 수준 이벤트를 담는 가상 턴이라 프롬프트가 아니다
		// (store/resolve.go 의 virtualTurnKey).
		where: `t.turn_index IS NOT NULL`,
		cols:  `COUNT(*)`,
		dest:  func(t *Totals) []any { return []any{&t.Prompts} },
		keys: map[Dim]string{
			DimTotal: keyTotal, DimVendor: keyVendor, DimProject: keyWorkspace,
		},
	},
	{
		op:   "세션 집계 조회",
		from: `sessions s`,
		at:   `s.started_at`,
		// 활동 시간은 세션 전체의 값이라 시간 버킷으로 쪼갤 수 없다. 세션이 시작한 버킷에
		// 통째로 귀속시킨다 — 겹치지도 비지도 않는 규칙이라 구간 합계는 정확하다.
		cols: `COUNT(*), COALESCE(SUM(s.active_time_sec),0)`,
		dest: func(t *Totals) []any { return []any{&t.SessionsStarted, &t.ActiveSeconds} },
		keys: map[Dim]string{
			DimTotal: keyTotal, DimVendor: keyVendor, DimProject: keyWorkspace,
		},
	},
}

// query 는 이 출처의 집계문이다. 값은 전부 바인딩되고 문자열 결합에 닿는 것은
// 우리가 소유한 상수(키 식·컬럼 목록)뿐이다.
func (f factSource) query(keyExpr string, filterKey bool) string {
	q := `SELECT ` + keyExpr + `, (` + f.at + `) / ` + hourSecondsLiteral + ` * ` + hourSecondsLiteral +
		`, ` + f.cols + ` FROM ` + f.from +
		` WHERE (` + f.at + `) IS NOT NULL AND (` + f.at + `) >= ? AND (` + f.at + `) < ?`
	if f.where != "" {
		q += ` AND ` + f.where
	}
	if filterKey {
		q += ` AND ` + keyExpr + ` = ?`
	}
	return q + ` GROUP BY 1, 2`
}

const hourSecondsLiteral = "3600"

// bucketRef 는 Go 쪽 누적 맵의 키다.
type bucketRef struct {
	key  string
	hour int64
}

// aggregate 는 dim 축으로 구간을 집계한다.
//
// keyFilter 가 비어 있지 않으면 그 키 하나로 좁힌다 (dim=vendor, key=claude_code 의
// 시간대별 집중도 같은 질의). DimTotal 에는 키가 하나뿐이라 무시한다.
//
// 결과는 (키, 시간 버킷) 오름차순으로 정렬돼 있다. 정렬을 고정해야 같은 입력이 같은
// 순서를 주고, 호출자가 그 위에 다시 정렬을 얹어도 동률의 순서가 흔들리지 않는다.
func aggregate(ctx context.Context, db sqlQuerier, dim Dim, keyFilter string, tr timeRange) ([]aggRow, error) {
	acc := map[bucketRef]*Totals{}

	for _, src := range factSources {
		keyExpr, ok := src.keys[dim]
		if !ok {
			// 이 축에 기여할 수 없는 출처다. 예: 모델별 집계의 도구 호출 수.
			continue
		}
		filter := keyFilter != "" && dim != DimTotal
		args := []any{tr.StartSec(), tr.EndSec()}
		if filter {
			args = append(args, keyFilter)
		}
		if err := src.collect(ctx, db, src.query(keyExpr, filter), args, acc); err != nil {
			return nil, err
		}
	}

	out := make([]aggRow, 0, len(acc))
	for ref, t := range acc {
		out = append(out, aggRow{Key: ref.key, Hour: ref.hour, Totals: *t})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Hour < out[j].Hour
	})
	return out, nil
}

func (f factSource) collect(ctx context.Context, db sqlQuerier, query string, args []any, acc map[bucketRef]*Totals) (err error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return queryErr(f.op, err)
	}
	defer closeRows(rows, f.op, &err)

	for rows.Next() {
		var (
			ref  bucketRef
			part Totals
		)
		dest := append([]any{&ref.key, &ref.hour}, f.dest(&part)...)
		if serr := rows.Scan(dest...); serr != nil {
			return queryErr(f.op, serr)
		}
		into, ok := acc[ref]
		if !ok {
			into = &Totals{}
			acc[ref] = into
		}
		into.add(part)
	}
	return nil
}

// sumAggregate 는 구간 전체의 단일 합계다. Today 카드가 쓴다.
func sumAggregate(ctx context.Context, db sqlQuerier, dim Dim, keyFilter string, tr timeRange) (Totals, error) {
	rows, err := aggregate(ctx, db, dim, keyFilter, tr)
	if err != nil {
		return Totals{}, err
	}
	var out Totals
	for _, r := range rows {
		out.add(r.Totals)
	}
	return out, nil
}
