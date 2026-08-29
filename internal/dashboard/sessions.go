package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
)

const (
	defaultSessionLimit = 100
	maxSessionLimit     = 1000
	// maxToolEvents 는 세션 상세가 한 번에 돌려주는 타임라인 길이 상한이다. 긴 세션은 툴
	// 호출이 수천 건이고 그것을 통째로 JSON 으로 넘기면 화면이 멈춘다. 잘렸다는 사실은
	// SessionDetail.ToolsTruncated 로 알린다 — 조용히 자르면 "타임라인이 왜 끊겼나" 를
	// 아무도 설명하지 못한다.
	maxToolEvents = 1000
)

// 세션 상태. v3 에는 sessions.status 컬럼이 없어 조회 시점에 계산한다 (ADR 0009).
//
// abandoned·handoff 는 **어떤 행에도 부여되지 않는다.** 판정에 필요한 입력(마지막 툴
// 이벤트의 성공 여부, project_hash)이 v3 에 없기 때문이다. 필터 어휘에는 남겨 두되
// 결과는 항상 비어 있다.
const (
	StatusRunning   = "running"
	StatusCompleted = "completed"
)

// statusExpr 은 상태 계산식이다. 목록 필터와 상세가 같은 식을 써야 둘이 갈리지 않는다.
const statusExpr = `CASE WHEN s.ended_at IS NULL THEN '` + StatusRunning + `' ELSE '` + StatusCompleted + `' END`

// lastActivityExpr 은 "마지막으로 알려진 활동" 시각이다.
//
// v3 에는 last_event_at 이 없고 started_at·ended_at 은 둘 다 선택이다. 그래서 하나를
// 고르는 것이 아니라 알고 있는 값 중 **가장 늦은 것**을 쓴다 — store/retention.go 의
// 보존 판정과 같은 식이다. 두 곳이 다른 시각을 "마지막 활동" 이라 부르면 화면에 보이는
// 세션이 그 값과 무관하게 사라진다.
const lastActivityExpr = `MAX(
  COALESCE(s.ended_at, 0),
  COALESCE(s.started_at, 0),
  COALESCE((SELECT MAX(COALESCE(e.occurred_at, t.ended_at, t.started_at))
              FROM turns t LEFT JOIN events e ON e.turn_id = t.id
             WHERE t.session_id = s.id), 0))`

// SessionQuery 는 세션 목록 조회 조건이다 (계획서 「오늘의 활동 / 세션 리스트」).
type SessionQuery struct {
	// Since·Until 은 started_at 범위(UTC unix 초)다. Since 포함, Until 배타. 0 이면 무제한.
	Since int64 `json:"since"`
	Until int64 `json:"until"`
	// Status 는 running|completed|abandoned|handoff 중 하나 이상이다. 비어 있으면 전체.
	// 뒤의 둘은 v3 에서 산출되지 않아 항상 빈 결과를 준다 (ADR 0009).
	Status []string `json:"status"`
	// Vendor 는 정확히 일치할 때만 거른다. 빈 문자열은 무시.
	Vendor string `json:"vendor"`
	// WorkspacePath 는 작업 폴더 원경로다 (v1 의 project_hash 를 대체한다 — v3 에는
	// 해시 컬럼이 없고 ADR 0010 이 원경로를 로컬에 저장하기로 했다).
	WorkspacePath string `json:"workspace_path"`

	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// SessionRow 는 세션 한 줄이다. 계획서 「세션 상세 지표 5종」이 이 안에 있다.
//
// # v3 에 출처가 없는 필드
//
// v3 sessions 에는 v1 이 갖고 있던 15개 남짓의 비정규화 지표 컬럼이 없다. 수치는 전부
// 승격 테이블에서 상관 서브쿼리로 다시 센다. 그래도 되살릴 수 없는 것이 남는다.
//
//	TitleSource  — v3 에 title_source 컬럼이 없다. 항상 빈 문자열
//	Summary      — v3 에 summary 컬럼이 없다. 항상 빈 문자열
//	APIErrors    — 오류 응답을 세는 입력이 v3 events 에 없다. 항상 0
//	Retries      — 같은 이유로 항상 0
//	Responses    — 응답 수를 담는 자리가 없다. 항상 0
//
// 필드를 지우지 않는 이유는 Totals 와 같다 — GUI TypeScript 바인딩과 `sessions --json`
// 출력이 깨진다.
type SessionRow struct {
	// ID 는 sessions.id 다. **세션을 가리키는 유일한 키**이고 Session() 의 인자다 —
	// v3 에서 session_key 는 벤더 안에서만 고유하다.
	ID int64 `json:"id"`
	// SessionKey 는 벤더가 준 세션 식별자다 (v1 의 session_id). 표시·디버깅용이다.
	SessionKey string `json:"session_key"`
	Vendor     string `json:"vendor"`
	StartedAt  int64  `json:"started_at"`
	// LastEventAt 은 마지막으로 알려진 활동 시각이다 (lastActivityExpr).
	LastEventAt int64 `json:"last_event_at"`
	// EndedAt 은 null 이면 진행 중이다.
	EndedAt *int64 `json:"ended_at"`
	Status  string `json:"status"`

	Title string `json:"title"`
	// TitleSource·Summary 는 v3 에 출처가 없어 항상 빈 문자열이다 (위 주석).
	TitleSource string `json:"title_source"`
	Summary     string `json:"summary"`
	// WorkspacePath 는 작업 폴더 원경로, ProjectName 은 그 basename 이다 (ADR 0010).
	WorkspacePath string `json:"workspace_path"`
	ProjectName   string `json:"project_name"`

	DurationMS    int64   `json:"duration_ms"`
	ActiveSeconds float64 `json:"active_seconds"`

	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CostUSD             float64 `json:"cost_usd"`

	ToolCalls   int64 `json:"tool_calls"`
	ToolErrors  int64 `json:"tool_errors"`
	ToolRejects int64 `json:"tool_rejects"`

	APIRequests int64 `json:"api_requests"`
	// APIErrors·Retries·Responses 는 v3 에 출처가 없어 항상 0 이다 (위 주석).
	APIErrors int64 `json:"api_errors"`
	Retries   int64 `json:"retries"`

	Prompts   int64 `json:"prompts"`
	Responses int64 `json:"responses"`

	LinesAdded   int64 `json:"lines_added"`
	LinesRemoved int64 `json:"lines_removed"`
}

// FileRow 는 세션 안의 파일 하나에 대한 변경 집계다 (계획서 「파일 변경」).
//
// v3 의 file_changes 는 파일당 한 행이 아니라 **변경 한 건당 한 행**이라 파일 경로로
// 묶어서 준다. v1 의 file_path_hash 는 v3 에 없고 원경로가 그 자리를 대신한다 (ADR 0010).
type FileRow struct {
	FileName string `json:"file_name"`
	FileExt  string `json:"file_ext"`
	// FilePath 는 원경로다. 「작업 폴더 열기」 가 이 값을 쓴다.
	FilePath string `json:"file_path"`

	LinesAdded   int64 `json:"lines_added"`
	LinesRemoved int64 `json:"lines_removed"`
	Edits        int64 `json:"edits"`
	LastTS       int64 `json:"last_ts"`
}

// ToolRow 는 tool_calls 한 행이다 (계획서 「최근 작업 타임라인」).
//
// v1 의 action(read|edit|write|run|search)과 target_hash 는 v3 에 컬럼이 없어 사라졌다.
// 대상은 원경로 하나로 온다.
type ToolRow struct {
	TS       int64  `json:"ts"`
	ToolName string `json:"tool_name"`
	// Target 은 대상의 원경로, TargetName 은 그 basename 이다.
	Target     string `json:"target"`
	TargetName string `json:"target_name"`
	// Success 가 null 이면 성공 여부를 모른다는 뜻이고 실패와 다르다 — 결정만 있고 결과가
	// 없는 호출(거부된 편집)이 그 경우다.
	Success    *bool  `json:"success"`
	DurationMS *int64 `json:"duration_ms"`
	ErrorType  string `json:"error_type"`
	Decision   string `json:"decision"`
	MCPServer  string `json:"mcp_server"`
}

// SessionMCPRow 는 한 세션의 MCP 서버별 사용량이다.
//
// v3 에는 mcp_session_usage 테이블이 없다. 연결 성공/실패와 토큰 수를 담던 자리가
// 사라졌고, 남은 관측은 tool_calls.mcp_server 하나다. 여러 세션을 가로지르는 집계는
// MCPRow 가 따로 있다 (Insights 카드).
type SessionMCPRow struct {
	ServerName string `json:"server_name"`
	ToolCalls  int64  `json:"tool_calls"`
	// Errors 는 success = 0 인 호출 수다.
	Errors int64 `json:"errors"`
}

// SessionDetail 은 Activity 세션 상세 화면 한 장이다.
type SessionDetail struct {
	// Found 가 false 면 그 id 가 없다는 뜻이다. 에러가 아니다 — 보존 정책이 지운 세션의
	// id 를 화면이 아직 들고 있는 것은 정상 상황이고, 그때 앱이 에러 토스트를 띄울
	// 이유가 없다.
	Found   bool       `json:"found"`
	Session SessionRow `json:"session"`

	Files []FileRow       `json:"files"`
	Tools []ToolRow       `json:"tools"`
	MCP   []SessionMCPRow `json:"mcp"`

	// ToolsTruncated 는 타임라인이 maxToolEvents 에서 잘렸다는 뜻이다.
	ToolsTruncated bool `json:"tools_truncated"`
}

// turnScope 는 세션에 속한 턴 집합이다. 아래 상관 서브쿼리가 전부 이것으로 좁힌다.
const turnScope = `SELECT id FROM turns WHERE session_id = s.id`

// llmSum 은 세션의 llm_calls 합계 한 컬럼이다.
func llmSum(expr string) string {
	return `COALESCE((SELECT ` + expr + ` FROM llm_calls c WHERE c.turn_id IN (` + turnScope + `)), 0)`
}

// toolCount 는 세션의 tool_calls 개수 한 컬럼이다. cond 가 비어 있으면 전체다.
func toolCount(cond string) string {
	q := `SELECT COUNT(*) FROM tool_calls c WHERE c.turn_id IN (` + turnScope + `)`
	if cond != "" {
		q += ` AND ` + cond
	}
	return `COALESCE((` + q + `), 0)`
}

// fileSum 은 세션의 file_changes 합계 한 컬럼이다.
func fileSum(expr string) string {
	return `COALESCE((SELECT ` + expr + ` FROM file_changes f
	  WHERE f.tool_call_id IN (SELECT id FROM tool_calls c WHERE c.turn_id IN (` + turnScope + `))), 0)`
}

// sessionColumns 는 세션 한 줄의 SELECT 목록이다.
//
// 수치를 상관 서브쿼리로 다시 세는 것이 v3 의 구조다. v1 처럼 비정규화 컬럼을 읽으면
// 값이 하나뿐이라 빨랐지만, v3 에는 그 컬럼이 없고 승격 테이블이 유일한 진실이다.
// 마이그레이션 v4 의 ix_turns_session · ix_tool_calls_turn 이 이 서브쿼리들을 받친다.
var sessionColumns = `s.id, s.session_key, s.vendor_id,
  COALESCE(s.started_at, 0), ` + lastActivityExpr + `, s.ended_at, ` + statusExpr + `,
  COALESCE(s.title,''), COALESCE(s.workspace_path,''), COALESCE(s.active_time_sec, 0),
  ` + llmSum(`SUM(c.input_tokens)`) + `,
  ` + llmSum(`SUM(c.output_tokens)`) + `,
  ` + llmSum(`SUM(c.cache_read_tokens)`) + `,
  ` + llmSum(`SUM(c.cache_write_tokens)`) + `,
  ` + llmSum(`SUM(c.cost_usd)`) + `,
  ` + llmSum(`COUNT(*)`) + `,
  ` + toolCount(``) + `,
  ` + toolCount(`c.success = 0`) + `,
  ` + toolCount(`c.decision = 'reject'`) + `,
  COALESCE((SELECT COUNT(*) FROM turns t WHERE t.session_id = s.id AND t.turn_index IS NOT NULL), 0),
  ` + fileSum(`SUM(f.additions)`) + `,
  ` + fileSum(`SUM(f.deletions)`)

func scanSession(scan func(...any) error) (SessionRow, error) {
	var (
		s     SessionRow
		ended sql.NullInt64
	)
	err := scan(
		&s.ID, &s.SessionKey, &s.Vendor,
		&s.StartedAt, &s.LastEventAt, &ended, &s.Status,
		&s.Title, &s.WorkspacePath, &s.ActiveSeconds,
		&s.InputTokens, &s.OutputTokens, &s.CacheReadTokens, &s.CacheCreationTokens,
		&s.CostUSD, &s.APIRequests,
		&s.ToolCalls, &s.ToolErrors, &s.ToolRejects,
		&s.Prompts, &s.LinesAdded, &s.LinesRemoved,
	)
	if err != nil {
		return SessionRow{}, err
	}
	s.EndedAt = nullInt64(ended)
	s.ProjectName = baseName(s.WorkspacePath)
	s.DurationMS = durationMS(s)
	return s, nil
}

// durationMS 는 세션 길이다. v3 에는 duration_ms 컬럼이 없어 시각에서 계산한다.
// 진행 중인 세션은 마지막 활동까지를 길이로 본다 — 그래야 화면의 값이 멈추지 않는다.
func durationMS(s SessionRow) int64 {
	if s.StartedAt <= 0 {
		return 0
	}
	end := s.LastEventAt
	if s.EndedAt != nil {
		end = *s.EndedAt
	}
	if end <= s.StartedAt {
		return 0
	}
	return (end - s.StartedAt) * 1000
}

// baseName 은 경로의 마지막 조각이다. 빈 경로는 빈 문자열을 준다.
func baseName(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(path))
}

// Sessions 는 세션 목록이다 — `sessions ORDER BY started_at DESC`.
//
// DB 가 없으면 빈 슬라이스다. nil 이 아니라 빈 슬라이스를 돌려주는 이유는 JSON 에서
// null 과 [] 가 다르고, 프런트엔드가 null 에 .map 을 걸면 그대로 터지기 때문이다.
func (r *Reader) Sessions(ctx context.Context, q SessionQuery) (out []SessionRow, err error) {
	const op = "세션 목록 조회"
	out = []SessionRow{}

	db, ok := r.db()
	if !ok {
		return out, nil
	}

	var (
		where []string
		args  []any
	)
	if q.Since > 0 {
		where = append(where, "s.started_at >= ?")
		args = append(args, q.Since)
	}
	if q.Until > 0 {
		where = append(where, "s.started_at < ?")
		args = append(args, q.Until)
	}
	if q.Vendor != "" {
		where = append(where, "s.vendor_id = ?")
		args = append(args, q.Vendor)
	}
	if q.WorkspacePath != "" {
		where = append(where, "s.workspace_path = ?")
		args = append(args, q.WorkspacePath)
	}
	if len(q.Status) > 0 {
		where = append(where, statusExpr+" IN ("+placeholders(len(q.Status))+")")
		for _, s := range q.Status {
			args = append(args, s)
		}
	}

	// 정렬 2순위가 id 인 이유는 페이지네이션이다. started_at 이 같은 세션이 여러 개면
	// 순서가 불안정해져 Offset 으로 넘길 때 어떤 행은 두 번, 어떤 행은 한 번도 안 나온다.
	query := `SELECT ` + sessionColumns + ` FROM sessions s`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY s.started_at DESC, s.id DESC LIMIT ? OFFSET ?"

	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	args = append(args, clampLimit(q.Limit, defaultSessionLimit, maxSessionLimit), offset)

	sqlRows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(sqlRows, op, &err)

	for sqlRows.Next() {
		s, serr := scanSession(sqlRows.Scan)
		if serr != nil {
			return nil, queryErr(op, serr)
		}
		out = append(out, s)
	}
	return out, nil
}

var sessionByIDSQL = `SELECT ` + sessionColumns + ` FROM sessions s WHERE s.id = ?`

// Session 은 세션 하나와 파일·툴 타임라인·MCP 사용을 함께 돌려준다.
//
// id 는 **sessions.id** 다. v1 의 문자열 session_id 가 아니다 — v3 에서 session_key 는
// (vendor_id, session_key) 로만 고유해서 그것만으로는 세션을 가리킬 수 없다.
//
// 없는 id 는 에러가 아니라 Found=false 다.
func (r *Reader) Session(ctx context.Context, id int64) (SessionDetail, error) {
	detail := SessionDetail{
		Files: []FileRow{},
		Tools: []ToolRow{},
		MCP:   []SessionMCPRow{},
	}
	db, ok := r.db()
	if !ok || id <= 0 {
		return detail, nil
	}

	row := db.QueryRowContext(ctx, sessionByIDSQL, id)
	s, err := scanSession(row.Scan)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return detail, nil
	case err != nil:
		return SessionDetail{}, queryErr("세션 조회", err)
	}
	detail.Found = true
	detail.Session = s

	if detail.Files, err = sessionFiles(ctx, db, id); err != nil {
		return SessionDetail{}, err
	}
	if detail.Tools, detail.ToolsTruncated, err = sessionTools(ctx, db, id); err != nil {
		return SessionDetail{}, err
	}
	if detail.MCP, err = sessionMCP(ctx, db, id); err != nil {
		return SessionDetail{}, err
	}
	return detail, nil
}

// sessionTurns 는 인자 하나(session id)로 좁히는 턴 집합이다. 상세 조회는 상관 서브쿼리가
// 아니라 바인딩 인자를 쓰므로 sessionColumns 의 turnScope 와 식이 다르다.
const sessionTurns = `SELECT id FROM turns WHERE session_id = ?`

// 변경량 내림차순이 계획서가 지정한 순서다. 동률에서 경로로 한 번 더 정렬해
// 새로고침마다 목록 순서가 바뀌지 않게 한다.
const sessionFilesSQL = `SELECT f.file_path,
  COALESCE(SUM(f.additions),0), COALESCE(SUM(f.deletions),0),
  COUNT(*), COALESCE(MAX(c.called_at),0)
FROM file_changes f
JOIN tool_calls c ON c.id = f.tool_call_id
WHERE c.turn_id IN (` + sessionTurns + `)
GROUP BY f.file_path
ORDER BY (COALESCE(SUM(f.additions),0) + COALESCE(SUM(f.deletions),0)) DESC, f.file_path ASC`

func sessionFiles(ctx context.Context, db sqlQuerier, id int64) (files []FileRow, err error) {
	const op = "파일 변경 조회"
	rows, err := db.QueryContext(ctx, sessionFilesSQL, id)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	files = []FileRow{}
	for rows.Next() {
		var f FileRow
		if serr := rows.Scan(&f.FilePath, &f.LinesAdded, &f.LinesRemoved,
			&f.Edits, &f.LastTS); serr != nil {
			return nil, queryErr(op, serr)
		}
		f.FileName = baseName(f.FilePath)
		f.FileExt = strings.TrimPrefix(filepath.Ext(f.FileName), ".")
		files = append(files, f)
	}
	return files, nil
}

// tool_calls.called_at 은 초 단위라 같은 초에 여러 행이 흔하다. id 를 2순위로 두어야
// 저장 순서(= 도착 순서)가 타임라인에 그대로 남는다.
const sessionToolsSQL = `SELECT COALESCE(c.called_at,0), COALESCE(c.tool_name,''),
  COALESCE(c.target,''), c.success, c.duration_ms, COALESCE(c.error_type,''),
  COALESCE(c.decision,''), COALESCE(c.mcp_server,'')
FROM tool_calls c
WHERE c.turn_id IN (` + sessionTurns + `)
ORDER BY c.called_at ASC, c.id ASC LIMIT ?`

func sessionTools(ctx context.Context, db sqlQuerier, id int64) (tools []ToolRow, truncated bool, err error) {
	const op = "툴 타임라인 조회"
	// 상한 +1 을 받아 "더 있다" 를 별도 질의 없이 판정한다.
	rows, err := db.QueryContext(ctx, sessionToolsSQL, id, maxToolEvents+1)
	if err != nil {
		return nil, false, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	tools = []ToolRow{}
	for rows.Next() {
		var (
			t        ToolRow
			success  sql.NullInt64
			duration sql.NullInt64
		)
		if serr := rows.Scan(&t.TS, &t.ToolName, &t.Target,
			&success, &duration, &t.ErrorType, &t.Decision, &t.MCPServer); serr != nil {
			return nil, false, queryErr(op, serr)
		}
		t.Success = nullBool(success)
		t.DurationMS = nullInt64(duration)
		t.TargetName = baseName(t.Target)
		if len(tools) == maxToolEvents {
			truncated = true
			break
		}
		tools = append(tools, t)
	}
	return tools, truncated, nil
}

const sessionMCPSQL = `SELECT c.mcp_server, COUNT(*),
  COALESCE(SUM(CASE WHEN c.success = 0 THEN 1 ELSE 0 END),0)
FROM tool_calls c
WHERE c.turn_id IN (` + sessionTurns + `) AND c.mcp_server IS NOT NULL AND c.mcp_server <> ''
GROUP BY c.mcp_server ORDER BY c.mcp_server ASC`

func sessionMCP(ctx context.Context, db sqlQuerier, id int64) (out []SessionMCPRow, err error) {
	const op = "MCP 사용 조회"
	rows, err := db.QueryContext(ctx, sessionMCPSQL, id)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	out = []SessionMCPRow{}
	for rows.Next() {
		var m SessionMCPRow
		if serr := rows.Scan(&m.ServerName, &m.ToolCalls, &m.Errors); serr != nil {
			return nil, queryErr(op, serr)
		}
		out = append(out, m)
	}
	return out, nil
}
