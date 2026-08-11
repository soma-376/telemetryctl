package dashboard

import (
	"context"
	"database/sql"
	"errors"
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

// SessionQuery 는 세션 목록 조회 조건이다 (계획서 「오늘의 활동 / 세션 리스트」).
type SessionQuery struct {
	// Since·Until 은 started_at 범위(UTC unix 초)다. Since 포함, Until 배타. 0 이면 무제한.
	Since int64 `json:"since"`
	Until int64 `json:"until"`
	// Status 는 running|completed|abandoned|handoff 중 하나 이상이다. 비어 있으면 전체.
	Status []string `json:"status"`
	// Vendor·ProjectHash 는 정확히 일치할 때만 거른다. 빈 문자열은 무시.
	Vendor      string `json:"vendor"`
	ProjectHash string `json:"project_hash"`

	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// SessionRow 는 sessions 한 행이다. 계획서 「세션 상세 지표 5종」이 이 안에 있다.
type SessionRow struct {
	SessionID   string `json:"session_id"`
	Vendor      string `json:"vendor"`
	StartedAt   int64  `json:"started_at"`
	LastEventAt int64  `json:"last_event_at"`
	// EndedAt 은 null 이면 진행 중이다.
	EndedAt *int64 `json:"ended_at"`
	Status  string `json:"status"`

	Title string `json:"title"`
	// TitleSource 는 prompt_head|files|fallback 이다. 제목 품질이 화면 예시에 못 미치는 것이
	// 알려진 한계라 화면이 출처를 표시할 수 있어야 한다 (계획서 리스크 표).
	TitleSource string `json:"title_source"`
	Summary     string `json:"summary"`
	ProjectHash string `json:"project_hash"`
	ProjectName string `json:"project_name"`

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
	APIErrors   int64 `json:"api_errors"`
	Retries     int64 `json:"retries"`

	Prompts   int64 `json:"prompts"`
	Responses int64 `json:"responses"`

	LinesAdded   int64 `json:"lines_added"`
	LinesRemoved int64 `json:"lines_removed"`
}

// FileRow 는 session_files 한 행이다 (계획서 「파일 변경」).
type FileRow struct {
	FileName string `json:"file_name"`
	FileExt  string `json:"file_ext"`
	FileHash string `json:"file_path_hash"`
	// LinesAdded·LinesRemoved 는 **근사** 다. lines_of_code 메트릭에 파일명이 없어 같은
	// 구간의 편집 대상에 나눠 귀속시킨 값이라 세션 합계와 달리 파일별 값은 정확하지 않다
	// (계획서 「파일 변경 · 툴 타임라인」). 화면이 툴팁으로 이를 표기한다.
	LinesAdded   int64 `json:"lines_added"`
	LinesRemoved int64 `json:"lines_removed"`
	Edits        int64 `json:"edits"`
	LastTS       int64 `json:"last_ts"`
}

// ToolRow 는 tool_events 한 행이다 (계획서 「최근 작업 타임라인」).
type ToolRow struct {
	TS         int64  `json:"ts"`
	ToolName   string `json:"tool_name"`
	Action     string `json:"action"`
	TargetName string `json:"target_name"`
	TargetHash string `json:"target_hash"`
	// Success 가 null 이면 성공 여부를 모른다는 뜻이고 실패와 다르다.
	Success    *bool  `json:"success"`
	DurationMS *int64 `json:"duration_ms"`
	ErrorType  string `json:"error_type"`
	Decision   string `json:"decision"`
	MCPServer  string `json:"mcp_server"`
}

// SessionMCPRow 는 한 세션의 mcp_session_usage 한 행이다.
// 여러 세션을 가로지르는 집계는 MCPRow 가 따로 있다 (Insights 카드).
type SessionMCPRow struct {
	ServerName      string `json:"server_name"`
	Connected       bool   `json:"connected"`
	ConnectFailures int64  `json:"connect_failures"`
	ToolCalls       int64  `json:"tool_calls"`
	Tokens          int64  `json:"tokens"`
}

// SessionDetail 은 Activity 세션 상세 화면 한 장이다.
type SessionDetail struct {
	// Found 가 false 면 그 session_id 가 없다는 뜻이다. 에러가 아니다 — 보존 정책이 지운
	// 세션의 id 를 화면이 아직 들고 있는 것은 정상 상황이고, 그때 앱이 에러 토스트를 띄울
	// 이유가 없다.
	Found   bool       `json:"found"`
	Session SessionRow `json:"session"`

	Files []FileRow       `json:"files"`
	Tools []ToolRow       `json:"tools"`
	MCP   []SessionMCPRow `json:"mcp"`

	// ToolsTruncated 는 타임라인이 maxToolEvents 에서 잘렸다는 뜻이다.
	ToolsTruncated bool `json:"tools_truncated"`
}

const sessionColumns = `session_id, vendor, started_at, last_event_at, ended_at, status,
  COALESCE(title,''), COALESCE(title_source,''), COALESCE(summary,''),
  COALESCE(project_hash,''), COALESCE(project_name,''),
  duration_ms, active_seconds,
  input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cost_usd,
  tool_calls, tool_errors, tool_rejects,
  api_requests, api_errors, retries,
  prompts, responses, lines_added, lines_removed`

func scanSession(scan func(...any) error) (SessionRow, error) {
	var (
		s     SessionRow
		ended sql.NullInt64
	)
	err := scan(
		&s.SessionID, &s.Vendor, &s.StartedAt, &s.LastEventAt, &ended, &s.Status,
		&s.Title, &s.TitleSource, &s.Summary, &s.ProjectHash, &s.ProjectName,
		&s.DurationMS, &s.ActiveSeconds,
		&s.InputTokens, &s.OutputTokens, &s.CacheReadTokens, &s.CacheCreationTokens, &s.CostUSD,
		&s.ToolCalls, &s.ToolErrors, &s.ToolRejects,
		&s.APIRequests, &s.APIErrors, &s.Retries,
		&s.Prompts, &s.Responses, &s.LinesAdded, &s.LinesRemoved,
	)
	s.EndedAt = nullInt64(ended)
	return s, err
}

// Sessions 는 세션 목록이다 — `sessions ORDER BY started_at DESC` (계획서).
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
		where = append(where, "started_at >= ?")
		args = append(args, q.Since)
	}
	if q.Until > 0 {
		where = append(where, "started_at < ?")
		args = append(args, q.Until)
	}
	if q.Vendor != "" {
		where = append(where, "vendor = ?")
		args = append(args, q.Vendor)
	}
	if q.ProjectHash != "" {
		where = append(where, "project_hash = ?")
		args = append(args, q.ProjectHash)
	}
	if len(q.Status) > 0 {
		where = append(where, "status IN ("+placeholders(len(q.Status))+")")
		for _, s := range q.Status {
			args = append(args, s)
		}
	}

	// 정렬 2순위가 session_id 인 이유는 페이지네이션이다. started_at 이 같은 세션이 여러 개면
	// 순서가 불안정해져 Offset 으로 넘길 때 어떤 행은 두 번, 어떤 행은 한 번도 안 나온다.
	query := `SELECT ` + sessionColumns + ` FROM sessions`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY started_at DESC, session_id DESC LIMIT ? OFFSET ?"

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

const sessionByIDSQL = `SELECT ` + sessionColumns + ` FROM sessions WHERE session_id = ?`

// Session 은 세션 하나와 파일·툴 타임라인·MCP 사용을 함께 돌려준다.
//
// 없는 id 는 에러가 아니라 Found=false 다.
func (r *Reader) Session(ctx context.Context, id string) (SessionDetail, error) {
	detail := SessionDetail{
		Files: []FileRow{},
		Tools: []ToolRow{},
		MCP:   []SessionMCPRow{},
	}
	db, ok := r.db()
	if !ok || id == "" {
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

// 변경량 내림차순이 계획서가 지정한 순서다. 동률에서 file_name 으로 한 번 더 정렬해
// 새로고침마다 목록 순서가 바뀌지 않게 한다.
const sessionFilesSQL = `SELECT file_name, COALESCE(file_ext,''), file_path_hash,
  lines_added, lines_removed, edits, last_ts
FROM session_files WHERE session_id = ?
ORDER BY (lines_added + lines_removed) DESC, file_name ASC`

func sessionFiles(ctx context.Context, db sqlQuerier, id string) (files []FileRow, err error) {
	const op = "파일 변경 조회"
	rows, err := db.QueryContext(ctx, sessionFilesSQL, id)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	files = []FileRow{}
	for rows.Next() {
		var f FileRow
		if serr := rows.Scan(&f.FileName, &f.FileExt, &f.FileHash,
			&f.LinesAdded, &f.LinesRemoved, &f.Edits, &f.LastTS); serr != nil {
			return nil, queryErr(op, serr)
		}
		files = append(files, f)
	}
	return files, nil
}

// tool_events.ts 는 초 단위라 같은 초에 여러 행이 흔하다. id 를 2순위로 두어야 저장 순서
// (= 도착 순서)가 타임라인에 그대로 남는다.
const sessionToolsSQL = `SELECT ts, tool_name, COALESCE(action,''), COALESCE(target_name,''),
  COALESCE(target_hash,''), success, duration_ms, COALESCE(error_type,''),
  COALESCE(decision,''), COALESCE(mcp_server,'')
FROM tool_events WHERE session_id = ? ORDER BY ts ASC, id ASC LIMIT ?`

func sessionTools(ctx context.Context, db sqlQuerier, id string) (tools []ToolRow, truncated bool, err error) {
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
		if serr := rows.Scan(&t.TS, &t.ToolName, &t.Action, &t.TargetName, &t.TargetHash,
			&success, &duration, &t.ErrorType, &t.Decision, &t.MCPServer); serr != nil {
			return nil, false, queryErr(op, serr)
		}
		t.Success = nullBool(success)
		t.DurationMS = nullInt64(duration)
		if len(tools) == maxToolEvents {
			truncated = true
			break
		}
		tools = append(tools, t)
	}
	return tools, truncated, nil
}

const sessionMCPSQL = `SELECT server_name, connected, connect_failures, tool_calls, tokens
FROM mcp_session_usage WHERE session_id = ? ORDER BY server_name ASC`

func sessionMCP(ctx context.Context, db sqlQuerier, id string) (out []SessionMCPRow, err error) {
	const op = "MCP 사용 조회"
	rows, err := db.QueryContext(ctx, sessionMCPSQL, id)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	out = []SessionMCPRow{}
	for rows.Next() {
		var (
			m         SessionMCPRow
			connected int64
		)
		if serr := rows.Scan(&m.ServerName, &connected, &m.ConnectFailures, &m.ToolCalls, &m.Tokens); serr != nil {
			return nil, queryErr(op, serr)
		}
		m.Connected = connected != 0
		out = append(out, m)
	}
	return out, nil
}
