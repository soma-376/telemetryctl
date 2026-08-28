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
	Vendor    string `json:"vendor"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	// Status 는 vendors.status 다 (enabled|disabled|error). v3 가 새로 둔 컬럼이고,
	// Settings 의 벤더 토글이 쓰는 값이다 — 우리가 관측한 사실이 아니라 사용자의 설정이라
	// 쓰기 경로가 이벤트마다 덮어쓰지 않는다 (store/resolve.go 의 upsertVendorSQL).
	Status string `json:"status"`
	// EventsTotal 은 이 벤더 세션에 매달린 events 행 수다. v1 에는 vendors 에 같은 이름의
	// 비정규화 컬럼이 있었지만 v3 에는 없어 세어서 만든다.
	EventsTotal int64 `json:"events_total"`

	// Connected 는 LastSeen 이 VendorActiveWindow 안이라는 뜻이다.
	Connected bool `json:"connected"`
	// Sessions·RunningSessions 는 이 벤더의 세션 수다. 상단 "agents active" 와 같은 근거다.
	Sessions        int64 `json:"sessions"`
	RunningSessions int64 `json:"running_sessions"`
}

const vendorsSQL = `SELECT v.vendor, v.first_seen, v.last_seen, v.status,
  (SELECT COUNT(*) FROM events e
     WHERE e.turn_id IN (SELECT t.id FROM turns t
       JOIN sessions s ON s.id = t.session_id WHERE s.vendor_id = v.vendor)),
  (SELECT COUNT(*) FROM sessions s WHERE s.vendor_id = v.vendor),
  (SELECT COUNT(*) FROM sessions s WHERE s.vendor_id = v.vendor AND s.ended_at IS NULL)
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
		if serr := rows.Scan(&v.Vendor, &v.FirstSeen, &v.LastSeen, &v.Status,
			&v.EventsTotal, &v.Sessions, &v.RunningSessions); serr != nil {
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
// # v3 에서 사라진 것
//
// 계획서가 지목한 두 문장 중 하나만 v3 로 살아남았다.
//
//	"github MCP가 최근 14개 세션에서 한 번도 사용되지 않았어요"
//	  → 여전히 만들 수 있다. Sessions 가 0 이면 그 창에서 안 쓴 것이다.
//	"postgres MCP 18번 연결 실패"
//	  → **만들 수 없다.** v3 에는 mcp_session_usage 가 없고 남은 관측은
//	    tool_calls.mcp_server 뿐이라 연결 성공·실패·토큰 수를 담을 자리가 없다.
//
// 그래서 connected · connect_failures · tokens · never_used 필드를 두지 않는다. 항상 0 인
// 필드를 남기면 화면이 "연결 실패 0건" 이라는 잘못된 사실을 그린다 — 값이 없는 것과
// 0 인 것은 다르다.
type MCPRow struct {
	ServerName string `json:"server_name"`
	// ScopeSessions 는 이번 조회가 들여다본 세션 수다 (요청한 N 과 실제 세션 수 중 작은 값).
	// 문장의 "최근 14개 세션에서" 가 이 값이라 행마다 같은 값이 들어간다.
	ScopeSessions int64 `json:"scope_sessions"`
	// Sessions 는 그중 이 서버의 도구를 실제로 부른 세션 수다.
	Sessions  int64 `json:"sessions"`
	ToolCalls int64 `json:"tool_calls"`
	// Errors 는 success = 0 으로 끝난 호출 수다.
	Errors int64 `json:"errors"`
}

// scope 를 서브쿼리로 두는 이유는 "최근 N개 세션" 이 시간 구간이 아니라 개수이기 때문이다.
// 이번 주에 세션이 3개뿐이면 창이 3개이고, 하루에 50개를 돌렸으면 오늘 안에서 끝난다.
const mcpScopeSQL = `SELECT id FROM sessions ORDER BY started_at DESC, id DESC LIMIT ?`

const mcpUsageSQL = `WITH scope AS (` + mcpScopeSQL + `)
SELECT c.mcp_server, COUNT(DISTINCT t.session_id), COUNT(*),
  COALESCE(SUM(CASE WHEN c.success = 0 THEN 1 ELSE 0 END),0)
FROM tool_calls c
JOIN turns t ON t.id = c.turn_id
JOIN scope s ON s.id = t.session_id
WHERE c.mcp_server IS NOT NULL AND c.mcp_server <> ''
GROUP BY c.mcp_server
ORDER BY COUNT(*) DESC, c.mcp_server ASC`

const scopeSessionCountSQL = `SELECT COUNT(*) FROM (` + mcpScopeSQL + `)`

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
		if serr := rows.Scan(&m.ServerName, &m.Sessions, &m.ToolCalls, &m.Errors); serr != nil {
			return nil, queryErr(op, serr)
		}
		m.ScopeSessions = scope
		out = append(out, m)
	}
	return out, nil
}
