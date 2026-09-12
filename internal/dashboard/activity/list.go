package activity

import (
	"context"
	"strings"

	"github.com/your-org/pulsemetry/internal/dashboard"
)

const (
	defaultActivityLimit = 50
	// maxActivityLimit 는 한 페이지의 상한이다. Activity 한 줄마다 승격 테이블 상관
	// 서브쿼리가 여러 번 도므로(dashboard.SessionColumns) 한 번에 수천 줄을 요구하면 화면이 멈춘다.
	maxActivityLimit = 200
)

// 정렬과 커서 비교는 같은 식을 쓴다. NULL 시각도 0으로 비교해 페이지에서 누락시키지 않는다.
// 기간 필터는 상태별 정렬 시각과 별개로 기존 시작 시각을 사용한다 (ADR 0027).
const activityStartKey = `COALESCE(s.started_at, 0)`
const activityRunningKey = `CASE WHEN s.ended_at IS NULL THEN 1 ELSE 0 END`
const activityOrderKey = `CASE WHEN s.ended_at IS NULL THEN ` + dashboard.LastActivityExpr + ` ELSE ` + activityStartKey + ` END`

// ── 검색 출처 술어 ──────────────────────────────────────────────────────────
//
// v3 에 FTS 가상 테이블이 없어 전부 LIKE 다 (ADR 0009). 와일드카드 escape 는 dashboard.LikePattern 이
// 하고 여기 ESCAPE '\' 와 짝을 이룬다 — 한쪽만 있으면 아무것도 못 찾거나 `_` 가 새어 나간다.
//
// 파일 경로는 세션에 직접 매달리지 않아 file_changes → tool_calls → turns 를 JOIN 해야
// 세션에 닿는다. **그 JOIN 을 바깥 질의에 풀어 놓지 않고 EXISTS 안에 가두는 것이 핵심이다.**
// 바깥에서 JOIN 하면 한 세션이 자식 행 수만큼 복제돼 dashboard.SessionColumns 의 SUM(토큰·비용)이
// 그 배수만큼 부풀어 오른다. EXISTS 는 행을 늘리지 않고 "있느냐" 만 답한다.
const (
	matchTitleSQL     = `s.title LIKE ? ESCAPE '\'`
	matchWorkspaceSQL = `s.workspace_path LIKE ? ESCAPE '\'`
	matchFileSQL      = `EXISTS (SELECT 1 FROM file_changes f
    JOIN tool_calls c ON c.id = f.tool_call_id
    JOIN turns t ON t.id = c.turn_id
   WHERE t.session_id = s.id AND f.file_path LIKE ? ESCAPE '\')`
	matchContentSQL = `EXISTS (SELECT 1 FROM turns t
   WHERE t.session_id = s.id AND t.prompt_text LIKE ? ESCAPE '\')`
)

// Activity 는 Activity 화면의 세션 목록 한 페이지다 (PROJ-90).
//
// # 정렬과 페이지네이션
//
// 진행 중을 먼저, 진행 중은 최근 활동 순, 종료는 시작 시각 순으로 보여준다.
// 동률은 id 내림차순으로 고정하고 같은 세 값을 커서로 사용한다 (ADR 0027).
//
// # 왜 합계가 부풀지 않는가
//
// 수치는 dashboard.SessionColumns 의 상관 서브쿼리가 세션마다 따로 센다. 검색 술어도 EXISTS 안에
// 갇혀 있어 바깥 행을 늘리지 않는다. llm_calls·tool_calls·file_changes 를 한 질의에서
// JOIN 하면 세 자식의 곱만큼 행이 불어나 SUM 이 그 배수로 틀린다.
//
// DB 가 없으면 빈 페이지다. nil 이 아니라 빈 슬라이스를 주는 이유는 프런트엔드가 null 에
// .map 을 걸면 그대로 터지기 때문이다.
func list(ctx context.Context, db dashboard.Querier, q Query) (page Page, err error) {
	const op = "활동 목록 조회"
	page = Page{Rows: []Row{}}

	if db == nil {
		return page, nil
	}

	limit := dashboard.ClampLimit(q.Limit, defaultActivityLimit, maxActivityLimit)
	text := dashboard.CapRunes(strings.TrimSpace(q.Text), dashboard.MaxSearchRunes)

	// SELECT 의 출처 표시 컬럼이 WHERE 보다 앞이라 인자도 먼저다.
	flags, args := activityFlagColumns(text)
	where, whereArgs := activityWhere(q, text)
	args = append(args, whereArgs...)
	// 상한 +1 을 받아 "더 있다" 를 별도 질의 없이 판정한다.
	args = append(args, limit+1)

	query := `SELECT ` + dashboard.SessionColumns + `,
  ` + flags + `
FROM sessions s`
	if where != "" {
		query += " WHERE " + where
	}
	query += " ORDER BY " + activityRunningKey + " DESC, " + activityOrderKey + " DESC, s.id DESC LIMIT ?"

	sqlRows, qerr := db.QueryContext(ctx, query, args...)
	if qerr != nil {
		return Page{}, dashboard.QueryErr(op, qerr)
	}
	defer dashboard.CloseRows(sqlRows, op, &err)

	rows := []Row{}
	for sqlRows.Next() {
		var flag [4]int64
		s, serr := dashboard.ScanSession(func(dest ...any) error {
			return sqlRows.Scan(append(dest, &flag[0], &flag[1], &flag[2], &flag[3])...)
		})
		if serr != nil {
			return Page{}, dashboard.QueryErr(op, serr)
		}
		rows = append(rows, Row{SessionRow: s, MatchedSources: matchedSources(flag)})
	}

	if len(rows) > limit {
		page.HasMore = true
		rows = rows[:limit]
	}
	page.Rows = rows
	if n := len(rows); n > 0 {
		last := rows[n-1]
		page.NextCursor = Cursor{Running: last.Status == dashboard.StatusRunning, SortAt: last.StartedAt, ID: last.ID}
		if page.NextCursor.Running {
			page.NextCursor.SortAt = last.LastEventAt
		}
	}
	return page, nil
}

// activityFlagColumns 는 출처별 일치 여부를 1/0 으로 뽑는 SELECT 컬럼 넷과 그 인자다.
//
// 검색어가 없으면 리터럴 0 넷이다 — 이때는 술어를 아예 실행하지 않는다.
func activityFlagColumns(text string) (string, []any) {
	if text == "" {
		return "0, 0, 0, 0", nil
	}
	pattern := dashboard.LikePattern(text)
	cols := flagExpr(matchTitleSQL) + ",\n  " + flagExpr(matchWorkspaceSQL) + ",\n  " +
		flagExpr(matchFileSQL) + ",\n  " + flagExpr(matchContentSQL)
	return cols, []any{pattern, pattern, pattern, pattern}
}

func flagExpr(pred string) string { return `CASE WHEN ` + pred + ` THEN 1 ELSE 0 END` }

// matchedSources 는 1/0 네 칸을 출처 이름으로 옮긴다. 순서를 고정해야 화면의 배지 순서가
// 새로고침마다 바뀌지 않는다.
func matchedSources(flag [4]int64) []string {
	names := [4]string{dashboard.SourceTitle, dashboard.SourceWorkspace, dashboard.SourceFile, dashboard.SourceContent}
	out := []string{}
	for i, n := range names {
		if flag[i] != 0 {
			out = append(out, n)
		}
	}
	return out
}

// activityWhere 는 필터·커서·검색 술어를 AND 로 엮는다. 값은 전부 바인딩되므로 질의문에
// 닿는 문자열은 자리표시자뿐이다.
func activityWhere(q Query, text string) (string, []any) {
	var (
		where []string
		args  []any
	)
	// add 는 빈 절을 무시한다 — 필터가 꺼져 있으면 inClause 가 빈 문자열을 준다.
	add := func(clause string, vals []any) {
		if clause == "" {
			return
		}
		where = append(where, clause)
		args = append(args, vals...)
	}

	if q.Since > 0 {
		add(activityStartKey+" >= ?", []any{q.Since})
	}
	if q.Until > 0 {
		add(activityStartKey+" < ?", []any{q.Until})
	}
	add(inClause("s.vendor_id", q.Vendors))
	add(inClause("s.workspace_path", q.Projects))
	add(inClause(dashboard.StatusExpr, q.Status))

	// keyset 조건. 정렬식과 **같은** 식을 써야 한다 (activityOrderKey 주석).
	if q.Cursor.ID > 0 {
		priority := 0
		if q.Cursor.Running {
			priority = 1
		}
		add("("+activityRunningKey+", "+activityOrderKey+", s.id) < (?, ?, ?)",
			[]any{priority, q.Cursor.SortAt, q.Cursor.ID})
	}

	if text != "" {
		pattern := dashboard.LikePattern(text)
		add("("+matchTitleSQL+"\n   OR "+matchWorkspaceSQL+"\n   OR "+matchFileSQL+
			"\n   OR "+matchContentSQL+")", []any{pattern, pattern, pattern, pattern})
	}
	return strings.Join(where, "\n  AND "), args
}

// inClause 는 다중 선택 필터 한 개다. 값이 하나도 없으면 빈 문자열 — "거르지 않음" 이다.
//
// 빈 문자열 값은 버린다. 화면의 "전체" 선택지가 빈 문자열로 내려오는 일이 흔한데 그것을
// 그대로 IN 에 넣으면 아무 세션도 매칭되지 않아 목록이 통째로 사라진다.
func inClause(expr string, values []string) (string, []any) {
	vals := make([]any, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		vals = append(vals, v)
	}
	if len(vals) == 0 {
		return "", nil
	}
	return expr + " IN (" + dashboard.Placeholders(len(vals)) + ")", vals
}
