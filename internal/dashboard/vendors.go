package dashboard

import (
	"context"
	"time"
)

// VendorActiveWindow 는 Settings 화면이 "연결됨" 으로 표시하는 신선도 기준이다.
//
// vendors.last_seen 이 이 창 안이면 최근에 실제로 텔레메트리를 보낸 벤더다. 값을 하루로 둔
// 이유는 화면의 의미가 "지금 붙어 있는가" 가 아니라 "이 도구를 쓰고 있는가" 이기 때문이다 —
// 점심시간에 Claude Code 가 "연결 안 됨" 으로 바뀌면 그 표시는 아무 정보도 주지 못한다.
const VendorActiveWindow = 24 * time.Hour

// VendorStatus 는 Settings 「연결 상태」 한 줄이다.
//
// 자동 설정 대상이 아닌 벤더(Gemini CLI·Cursor)는 여기 나오지 않는다 — 이벤트를 한 번도
// 보낸 적이 없어 vendors 테이블에 행이 없다. 화면이 아는 벤더 목록과 대조해 "연결 안 됨" 을
// 그리는 것은 GUI 몫이다. 이 패키지는 관측된 사실만 돌려준다.
type VendorStatus struct {
	Vendor      string `json:"vendor"`
	FirstSeen   int64  `json:"first_seen"`
	LastSeen    int64  `json:"last_seen"`
	EventsTotal int64  `json:"events_total"`

	// Connected 는 LastSeen 이 VendorActiveWindow 안이라는 뜻이다.
	Connected bool `json:"connected"`
	// Sessions·RunningSessions 는 이 벤더의 세션 수다. 상단 "agents active" 와 같은 근거다.
	Sessions        int64 `json:"sessions"`
	RunningSessions int64 `json:"running_sessions"`
}

const vendorsSQL = `SELECT v.vendor, v.first_seen, v.last_seen, v.events_total,
  (SELECT COUNT(*) FROM sessions s WHERE s.vendor = v.vendor),
  (SELECT COUNT(*) FROM sessions s WHERE s.vendor = v.vendor AND s.status = 'running')
FROM vendors v
ORDER BY v.last_seen DESC, v.vendor ASC`

// Vendors 는 벤더별 연결 상태다 — `vendors.last_seen` (계획서 「Settings 연결 상태」).
func (r *Reader) Vendors(ctx context.Context) (out []VendorStatus, err error) {
	const op = "벤더 상태 조회"
	out = []VendorStatus{}

	db, ok := r.db()
	if !ok {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, vendorsSQL)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	cutoff := r.now().Add(-VendorActiveWindow).Unix()
	for rows.Next() {
		var v VendorStatus
		if serr := rows.Scan(&v.Vendor, &v.FirstSeen, &v.LastSeen, &v.EventsTotal,
			&v.Sessions, &v.RunningSessions); serr != nil {
			return nil, queryErr(op, serr)
		}
		v.Connected = v.LastSeen >= cutoff
		out = append(out, v)
	}
	return out, nil
}

// ── MCP ─────────────────────────────────────────────────────────────────────

const (
	// defaultMCPSessions 는 계획서 Insights 예시의 "최근 14개 세션" 이다.
	defaultMCPSessions = 14
	maxMCPSessions     = 1000
)

// MCPRow 는 Insights 의 MCP 카드 한 줄이다 (계획서 「Insights MCP 카드」).
//
// 계획서가 두 문장을 지목했고 각각 아래 필드로 그대로 나온다.
//
//	"github MCP가 최근 14개 세션에서 한 번도 사용되지 않았어요"
//	  → NeverUsed (ConnectedSessions>0 이고 ToolCalls==0), ScopeSessions=14
//	"postgres MCP 18번 연결 실패"
//	  → ConnectFailures
type MCPRow struct {
	ServerName string `json:"server_name"`
	// ScopeSessions 는 이번 조회가 들여다본 세션 수다 (요청한 N 과 실제 세션 수 중 작은 값).
	// 문장의 "최근 14개 세션에서" 가 이 값이라 행마다 같은 값이 들어간다.
	ScopeSessions int64 `json:"scope_sessions"`
	// Sessions 는 그중 이 서버가 등장한 세션 수다.
	Sessions int64 `json:"sessions"`
	// ConnectedSessions 는 연결에 성공한 세션 수, UnusedSessions 는 연결됐는데 툴 호출이
	// 0 이었던 세션 수다.
	ConnectedSessions int64 `json:"connected_sessions"`
	UnusedSessions    int64 `json:"unused_sessions"`

	ConnectFailures int64 `json:"connect_failures"`
	ToolCalls       int64 `json:"tool_calls"`
	Tokens          int64 `json:"tokens"`

	// NeverUsed 는 "붙기는 했는데 한 번도 안 썼다" 는 판정이다.
	NeverUsed bool `json:"never_used"`
}

// scope 를 서브쿼리로 두는 이유는 "최근 N개 세션" 이 시간 구간이 아니라 개수이기 때문이다.
// 이번 주에 세션이 3개뿐이면 창이 3개이고, 하루에 50개를 돌렸으면 오늘 안에서 끝난다.
const mcpUsageSQL = `WITH scope AS (
  SELECT session_id FROM sessions ORDER BY started_at DESC, session_id DESC LIMIT ?
)
SELECT m.server_name,
  COUNT(*),
  COALESCE(SUM(m.connected),0),
  COALESCE(SUM(CASE WHEN m.connected = 1 AND m.tool_calls = 0 THEN 1 ELSE 0 END),0),
  COALESCE(SUM(m.connect_failures),0),
  COALESCE(SUM(m.tool_calls),0),
  COALESCE(SUM(m.tokens),0)
FROM mcp_session_usage m
JOIN scope s ON s.session_id = m.session_id
GROUP BY m.server_name
ORDER BY SUM(m.tool_calls) DESC, m.server_name ASC`

const scopeSessionCountSQL = `SELECT COUNT(*) FROM (
  SELECT session_id FROM sessions ORDER BY started_at DESC, session_id DESC LIMIT ?
)`

// MCPUsage 는 최근 lastNSessions 개 세션의 MCP 사용 집계다.
// lastNSessions 가 0 이하면 14 를 쓴다 (계획서 예시 값).
func (r *Reader) MCPUsage(ctx context.Context, lastNSessions int) (out []MCPRow, err error) {
	const op = "MCP 사용 집계 조회"
	out = []MCPRow{}

	db, ok := r.db()
	if !ok {
		return out, nil
	}
	n := clampLimit(lastNSessions, defaultMCPSessions, maxMCPSessions)

	var scope int64
	if serr := db.QueryRowContext(ctx, scopeSessionCountSQL, n).Scan(&scope); serr != nil {
		return nil, queryErr(op, serr)
	}

	rows, err := db.QueryContext(ctx, mcpUsageSQL, n)
	if err != nil {
		return nil, queryErr(op, err)
	}
	defer closeRows(rows, op, &err)

	for rows.Next() {
		var m MCPRow
		if serr := rows.Scan(&m.ServerName, &m.Sessions, &m.ConnectedSessions, &m.UnusedSessions,
			&m.ConnectFailures, &m.ToolCalls, &m.Tokens); serr != nil {
			return nil, queryErr(op, serr)
		}
		m.ScopeSessions = scope
		// 연결된 적이 없으면 "안 썼다" 가 아니라 "못 붙었다" 다. 둘을 합치면 연결 실패를
		// 미사용으로 보고하게 되고, 사용자는 지우지 말아야 할 서버를 지운다.
		m.NeverUsed = m.ConnectedSessions > 0 && m.ToolCalls == 0
		out = append(out, m)
	}
	return out, nil
}
