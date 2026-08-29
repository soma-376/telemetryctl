package dashboard

import (
	"context"
	"strings"
)

const (
	defaultActivityLimit = 50
	// maxActivityLimit 는 한 페이지의 상한이다. Activity 한 줄마다 승격 테이블 상관
	// 서브쿼리가 여러 번 도므로(sessionColumns) 한 번에 수천 줄을 요구하면 화면이 멈춘다.
	maxActivityLimit = 200
)

// activityOrderKey 는 목록 정렬과 커서 비교에 **함께** 쓰는 시작 시각 식이다.
//
// v3 의 sessions.started_at 은 NULL 일 수 있다 (store/resolve.go 의 nullSec). 정렬을 raw
// 컬럼으로 하고 커서 비교만 COALESCE 로 하면 — 또는 그 반대로 하면 — started_at 이 NULL 인
// 세션이 첫 페이지에는 보이고 다음 페이지부터는 조건에서 탈락해 **조용히 사라진다.**
// 두 자리가 같은 식을 쓰는 것이 그 누락을 막는 유일한 방법이다.
//
// SessionRow.StartedAt 도 COALESCE(s.started_at, 0) 이라 커서에 담기는 값과 정확히 같다.
//
// 대가는 ix_sessions_started 를 못 쓰는 것이다. 식 인덱스가 없으므로 세션 스캔이 된다 —
// 단일 개발자 로컬 규모(400일 보존)에서 sessions 행 수는 수천 단위라 수용 가능하고,
// 어차피 한 줄마다 도는 상관 서브쿼리가 비용의 대부분이다.
const activityOrderKey = `COALESCE(s.started_at, 0)`

// ── 검색 출처 술어 ──────────────────────────────────────────────────────────
//
// v3 에 FTS 가상 테이블이 없어 전부 LIKE 다 (ADR 0009). 와일드카드 escape 는 likePattern 이
// 하고 여기 ESCAPE '\' 와 짝을 이룬다 — 한쪽만 있으면 아무것도 못 찾거나 `_` 가 새어 나간다.
//
// 파일 경로는 세션에 직접 매달리지 않아 file_changes → tool_calls → turns 를 JOIN 해야
// 세션에 닿는다. **그 JOIN 을 바깥 질의에 풀어 놓지 않고 EXISTS 안에 가두는 것이 핵심이다.**
// 바깥에서 JOIN 하면 한 세션이 자식 행 수만큼 복제돼 sessionColumns 의 SUM(토큰·비용)이
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

// ActivityCursor 는 keyset 페이지네이션의 위치다 — 마지막으로 받은 줄의 (시작 시각, id).
//
// OFFSET 을 쓰지 않는 이유는 데몬이 계속 쓰고 있기 때문이다. 페이지 사이에 세션 하나가
// 새로 들어오면 뒤 페이지가 통째로 한 칸 밀려 어떤 줄은 두 번 나오고 어떤 줄은 건너뛴다.
// 정렬 키를 그대로 커서로 쓰면 그 사이 무엇이 들어오든 위치가 흔들리지 않는다.
//
// ID 가 0 이면 "첫 페이지" 다. sessions.id 는 1부터라 유효한 커서와 겹치지 않는다.
type ActivityCursor struct {
	StartedAt int64 `json:"started_at"`
	ID        int64 `json:"id"`
}

func (c ActivityCursor) valid() bool { return c.ID > 0 }

// ActivityQuery 는 Activity 목록 한 페이지의 조회 조건이다 (PROJ-90).
//
// 필터는 서로 AND 이고, 같은 필터 안의 여러 값은 OR 이다 — 화면의 다중 선택이 그 모양이다.
// 빈 목록·빈 문자열은 "거르지 않음" 이고 에러가 아니다.
type ActivityQuery struct {
	// Since·Until 은 started_at 범위(UTC unix 초)다. Since 포함, Until 배타. 0 이면 무제한.
	Since int64 `json:"since"`
	Until int64 `json:"until"`
	// Vendors 는 vendor_id 다중 선택이다.
	Vendors []string `json:"vendors"`
	// Projects 는 workspace_path **원경로** 다중 선택이다 (ADR 0010). basename 이 아니다 —
	// 서로 다른 폴더의 같은 이름 프로젝트가 한 필터로 뭉개지면 안 된다.
	Projects []string `json:"projects"`
	// Status 는 running|completed|abandoned|handoff 다중 선택이다. 뒤의 둘은 v3 에서
	// 산출되지 않아 항상 빈 결과를 준다 (ADR 0009).
	Status []string `json:"status"`
	// Text 는 통합 검색어다. 사용자가 입력한 그대로 넣는다 — 와일드카드 escape 는 여기가 한다.
	Text string `json:"text"`

	Limit int `json:"limit"`
	// Cursor 는 이전 페이지의 NextCursor 다. 첫 페이지는 비워 둔다.
	Cursor ActivityCursor `json:"cursor"`
}

// ActivityRow 는 Activity 목록 한 줄이다.
//
// 시작·경로·소요·토큰·비용·상태는 SessionRow 가 이미 갖고 있어 그대로 묻어 온다. JSON 에서
// 임베드는 평평하게 펼쳐지므로 화면은 한 겹 구조로 읽는다.
type ActivityRow struct {
	SessionRow

	// WorkType 은 목록의 "작업" 열이다 — 이 세션이 무엇을 한 세션인지(구현·디버깅·리뷰 …).
	//
	// TODO(PROJ-92): 턴 분류가 이 값의 **유일한** 출처다. 그 작업이 붙기 전까지 항상 빈
	// 문자열이고, 여기서 휴리스틱으로 추측해 채우지 않는다 — 근거 없는 값이 목록에 뜨면
	// 사용자는 그것을 분류 결과로 읽는다. 화면은 빈 문자열을 "미분류" 로 그리면 된다.
	// v3 turns 에는 work_type 컬럼이 없으므로(v2 에 있었고 v3 가 지웠다) PROJ-92 는
	// 저장할 자리부터 만들어야 한다. 이 필드가 그 결과를 받을 자리다.
	WorkType string `json:"work_type"`

	// MatchedSources 는 검색어가 걸린 출처다 (title|workspace|file|content). 검색어가
	// 없으면 빈 슬라이스다. 화면이 "파일명에서 발견" 같은 배지를 붙일 수 있어야 한다.
	MatchedSources []string `json:"matched_sources"`
}

// ActivityPage 는 목록 한 페이지와 다음 페이지 정보다.
type ActivityPage struct {
	Rows []ActivityRow `json:"rows"`

	// HasMore 가 "더 불러오기" 버튼의 유일한 근거다. Rows 가 Limit 만큼 찼다는 사실로는
	// 마지막 페이지를 구분할 수 없다 — 딱 맞아떨어진 경우와 더 있는 경우가 같아 보인다.
	// 그래서 Limit+1 개를 받아 한 개가 남는지로 판정한다 (별도 COUNT 질의를 돌지 않는다).
	HasMore bool `json:"has_more"`

	// NextCursor 는 **항상** 마지막 줄의 위치다. HasMore 가 false 여도 0 으로 비우지 않는다 —
	// 비워 두면 HasMore 를 안 보는 호출자가 그것을 "첫 페이지" 로 읽어 처음부터 무한히 다시
	// 받는다. 마지막 줄을 그대로 두면 한 번 더 불러도 빈 페이지가 와서 그 자리에서 멈춘다.
	// Rows 가 비어 있으면 커서도 비어 있다.
	NextCursor ActivityCursor `json:"next_cursor"`
}

// Activity 는 Activity 화면의 세션 목록 한 페이지다 (PROJ-90).
//
// # 정렬과 페이지네이션
//
// 정렬은 `started_at DESC, sessions.id DESC` 다. 2순위 id 가 없으면 같은 초에 시작한 세션들의
// 순서가 매 질의마다 달라져 페이지 경계에서 어떤 줄은 두 번 나오고 어떤 줄은 사라진다.
// 그 두 값을 그대로 커서로 삼아 다음 페이지를 연다 (ActivityCursor).
//
// # 왜 합계가 부풀지 않는가
//
// 수치는 sessionColumns 의 상관 서브쿼리가 세션마다 따로 센다. 검색 술어도 EXISTS 안에
// 갇혀 있어 바깥 행을 늘리지 않는다. llm_calls·tool_calls·file_changes 를 한 질의에서
// JOIN 하면 세 자식의 곱만큼 행이 불어나 SUM 이 그 배수로 틀린다.
//
// DB 가 없으면 빈 페이지다. nil 이 아니라 빈 슬라이스를 주는 이유는 프런트엔드가 null 에
// .map 을 걸면 그대로 터지기 때문이다.
func (r *Reader) Activity(ctx context.Context, q ActivityQuery) (page ActivityPage, err error) {
	const op = "활동 목록 조회"
	page = ActivityPage{Rows: []ActivityRow{}}

	db, ok := r.db()
	if !ok {
		return page, nil
	}

	limit := clampLimit(q.Limit, defaultActivityLimit, maxActivityLimit)
	text := capRunes(strings.TrimSpace(q.Text), maxSearchRunes)

	// SELECT 의 출처 표시 컬럼이 WHERE 보다 앞이라 인자도 먼저다.
	flags, args := activityFlagColumns(text)
	where, whereArgs := activityWhere(q, text)
	args = append(args, whereArgs...)
	// 상한 +1 을 받아 "더 있다" 를 별도 질의 없이 판정한다.
	args = append(args, limit+1)

	query := `SELECT ` + sessionColumns + `,
  ` + flags + `
FROM sessions s`
	if where != "" {
		query += " WHERE " + where
	}
	query += " ORDER BY " + activityOrderKey + " DESC, s.id DESC LIMIT ?"

	sqlRows, qerr := db.QueryContext(ctx, query, args...)
	if qerr != nil {
		return ActivityPage{}, queryErr(op, qerr)
	}
	defer closeRows(sqlRows, op, &err)

	rows := []ActivityRow{}
	for sqlRows.Next() {
		var flag [4]int64
		s, serr := scanSession(func(dest ...any) error {
			return sqlRows.Scan(append(dest, &flag[0], &flag[1], &flag[2], &flag[3])...)
		})
		if serr != nil {
			return ActivityPage{}, queryErr(op, serr)
		}
		rows = append(rows, ActivityRow{SessionRow: s, MatchedSources: matchedSources(flag)})
	}

	if len(rows) > limit {
		page.HasMore = true
		rows = rows[:limit]
	}
	page.Rows = rows
	if n := len(rows); n > 0 {
		page.NextCursor = ActivityCursor{StartedAt: rows[n-1].StartedAt, ID: rows[n-1].ID}
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
	pattern := likePattern(text)
	cols := flagExpr(matchTitleSQL) + ",\n  " + flagExpr(matchWorkspaceSQL) + ",\n  " +
		flagExpr(matchFileSQL) + ",\n  " + flagExpr(matchContentSQL)
	return cols, []any{pattern, pattern, pattern, pattern}
}

func flagExpr(pred string) string { return `CASE WHEN ` + pred + ` THEN 1 ELSE 0 END` }

// matchedSources 는 1/0 네 칸을 출처 이름으로 옮긴다. 순서를 고정해야 화면의 배지 순서가
// 새로고침마다 바뀌지 않는다.
func matchedSources(flag [4]int64) []string {
	names := [4]string{SourceTitle, SourceWorkspace, SourceFile, SourceContent}
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
func activityWhere(q ActivityQuery, text string) (string, []any) {
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
		add(activityOrderKey+" >= ?", []any{q.Since})
	}
	if q.Until > 0 {
		add(activityOrderKey+" < ?", []any{q.Until})
	}
	add(inClause("s.vendor_id", q.Vendors))
	add(inClause("s.workspace_path", q.Projects))
	add(inClause(statusExpr, q.Status))

	// keyset 조건. 정렬식과 **같은** 식을 써야 한다 (activityOrderKey 주석).
	if q.Cursor.valid() {
		add("("+activityOrderKey+" < ? OR ("+activityOrderKey+" = ? AND s.id < ?))",
			[]any{q.Cursor.StartedAt, q.Cursor.StartedAt, q.Cursor.ID})
	}

	if text != "" {
		pattern := likePattern(text)
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
	return expr + " IN (" + placeholders(len(vals)) + ")", vals
}

// ── GUI 서비스 배선 ─────────────────────────────────────────────────────────

// Activity 는 Activity 화면의 세션 목록 한 페이지다.
//
// service.go 가 아니라 여기 있는 이유는 이 메서드가 Activity 조회의 일부이기 때문이다 —
// 질의와 그 배선이 한 파일에 있으면 조회 하나를 고칠 때 두 파일을 오가지 않아도 된다.
func (s *Service) Activity(ctx context.Context, q ActivityQuery) (ActivityPage, error) {
	s.reconnect()
	return s.reader.Activity(ctx, q)
}
