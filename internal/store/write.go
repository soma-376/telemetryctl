package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/rollup"
	"github.com/your-org/pulsemetry/internal/session"
)

// EventRecord 는 events 한 행과 거기 매달린 event_content 행들이다.
//
// 디코더(otlpdecode.Result)는 Events·Contents 를 EventIndex 로 연결된 별도 슬라이스로 준다.
// events.id 는 이 패키지가 INSERT 하며 정하므로 그 인덱스를 여기서 다시 풀 수는 없다 —
// 배선 단계(9단계 daemon)가 EventIndex 로 조인해 이 타입으로 묶어 넘긴다.
//
// 묶어서 받는 이유는 고아 방지다. 이벤트와 원문이 따로 들어오면 "이벤트는 중복이라 무시했는데
// 원문은 저장" 하는 경로가 생긴다. 한 레코드로 받으면 그 상태가 표현 불가능하다.
type EventRecord struct {
	Event event.Event
	// Contents 는 이 이벤트에서 뽑힌 원문이다. 없으면 nil.
	// 한 이벤트가 여러 종류를 가질 수 있다 — tool_result 로그는 tool_input 과 tool_result 를
	// 함께 실어 온다. kind 가 겹치면 뒤엣것이 이긴다.
	Contents []event.Content
}

// Batch 는 한 트랜잭션으로 적용되는 쓰기 단위다.
//
// 세 종류를 한 트랜잭션에 묶는 이유는 부분 적용이 곧 화면의 모순이기 때문이다 — 이벤트만
// 들어가고 세션이 안 들어가면 세션 수치가 이벤트보다 뒤처지고, 롤업만 들어가면 Today 카드와
// 세션 목록의 합이 어긋난다.
//
// Sessions 는 조립기가 주는 **전체 스냅샷**이다. 부분 세션을 넣으면 안 된다 —
// 종속 테이블 쓰기가 스냅샷을 정본으로 보고 대체하므로 빠진 항목이 삭제로 해석된다.
type Batch struct {
	Events   []EventRecord
	Sessions []session.Session
	Rollups  []rollup.Row
}

func (b Batch) isEmpty() bool {
	return len(b.Events) == 0 && len(b.Sessions) == 0 && len(b.Rollups) == 0
}

// WriteResult 는 배치 하나가 실제로 무엇을 했는지 알려준다.
//
// 특히 EventsDuplicate 가 중요하다. 중복 삽입은 에러가 아니라 정상 동작이지만(재전송은
// exporter 의 기본 동작이다) 조용히 넘어가면 "왜 이벤트 수가 보낸 것보다 적은가" 를
// 아무도 설명하지 못한다.
type WriteResult struct {
	EventsInserted   int
	EventsDuplicate  int
	ContentsInserted int
	// ContentsDropped 는 --no-store-content 로 버린 원문 수다.
	ContentsDropped int

	SessionsUpserted int
	FilesUpserted    int
	// ToolEventsWritten 은 다시 쓴 타임라인 행 수다. 같은 세션을 두 번 저장해도
	// 두 번째는 기존 행을 지우고 같은 수를 다시 넣으므로 테이블은 자라지 않는다.
	ToolEventsWritten int
	ToolEventsDeleted int
	MCPUpserted       int

	RollupRows     int
	VendorsTouched int
}

// Write 는 배치를 하나의 트랜잭션으로 적용한다.
func (d *DB) Write(ctx context.Context, b Batch) (WriteResult, error) {
	var res WriteResult
	if b.isEmpty() {
		return res, nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("store: 쓰기 트랜잭션 시작: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // 커밋 후에는 ErrTxDone 이라 무시한다

	if err := d.writeEvents(ctx, tx, b.Events, &res); err != nil {
		return WriteResult{}, err
	}
	if err := writeSessions(ctx, tx, b.Sessions, &res); err != nil {
		return WriteResult{}, err
	}
	if err := writeRollups(ctx, tx, b.Rollups, &res); err != nil {
		return WriteResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return WriteResult{}, fmt.Errorf("store: 쓰기 커밋: %w", err)
	}
	return res, nil
}

// ── events · event_content ──────────────────────────────────────────────────

const insertEventSQL = `INSERT INTO events (
  dedup_key, ts, hour, session_id, vendor, signal, name, installation_id,
  event_id, trace_id, span_id,
  model, type, tool_name, decision, decision_source, language, query_source,
  speed, effort, agent_name, skill_name, plugin_name, mcp_server, mcp_tool,
  start_type, terminal_type, app_version, entrypoint, environment,
  project_hash, project_name,
  value, unit, cost_usd, input_tokens, output_tokens, cache_read_tokens,
  cache_creation_tokens, duration_ms, status_code, attempt, success, error_type,
  prompt_length, response_length, tool_input_bytes, tool_result_bytes
) VALUES (?,?,?,?,?,?,?,?, ?,?,?, ?,?,?,?,?,?,?, ?,?,?,?,?,?,?, ?,?,?,?,?, ?,?,
          ?,?,?,?,?,?, ?,?,?,?,?,?, ?,?,?,?)
ON CONFLICT(dedup_key) DO NOTHING`

const insertContentSQL = `INSERT INTO event_content (event_id, kind, body, truncated)
VALUES (?,?,?,?)
ON CONFLICT(event_id, kind) DO UPDATE SET body = excluded.body, truncated = excluded.truncated`

func (d *DB) writeEvents(ctx context.Context, tx *sql.Tx, recs []EventRecord, res *WriteResult) error {
	if len(recs) == 0 {
		return nil
	}
	evStmt, err := tx.PrepareContext(ctx, insertEventSQL)
	if err != nil {
		return fmt.Errorf("store: events INSERT 준비: %w", err)
	}
	defer evStmt.Close()

	ctStmt, err := tx.PrepareContext(ctx, insertContentSQL)
	if err != nil {
		return fmt.Errorf("store: event_content INSERT 준비: %w", err)
	}
	defer ctStmt.Close()

	vendors := newVendorTally()

	for _, rec := range recs {
		e := rec.Event
		if err := e.Validate(); err != nil {
			// 경계에서 막는다. NOT NULL 위반이 SQL 에러로 뒤늦게 터지면 배치 전체가 날아간다.
			return fmt.Errorf("store: 이벤트 검증: %w", err)
		}
		out, err := evStmt.ExecContext(ctx, eventArgs(e)...)
		if err != nil {
			return fmt.Errorf("store: events INSERT (name=%q): %w", e.Name, err)
		}
		affected, err := out.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: events INSERT 결과 확인: %w", err)
		}
		if affected == 0 {
			// dedup_key 충돌 — 재전송이다. 에러가 아니라 조용한 무시이되 건수는 센다.
			// 원문도 함께 건너뛴다. 여기서 content 만 쓰면 존재하지 않는 events.id 를
			// 가리키는 고아가 되거나(FK 위반) 엉뚱한 이벤트에 붙는다.
			res.EventsDuplicate++
			continue
		}
		res.EventsInserted++
		vendors.observe(e.Vendor, e.TS.Sec())

		id, err := out.LastInsertId()
		if err != nil {
			return fmt.Errorf("store: events id 확인: %w", err)
		}
		if !d.cfg.storeContent {
			res.ContentsDropped += len(rec.Contents)
			continue
		}
		for _, c := range rec.Contents {
			if c.Kind == "" {
				continue // 제로값 = 원문 없음
			}
			if _, err := ctStmt.ExecContext(ctx, id, string(c.Kind), c.Body, boolInt(c.Truncated)); err != nil {
				return fmt.Errorf("store: event_content INSERT (kind=%s): %w", c.Kind, err)
			}
			res.ContentsInserted++
		}
	}
	return vendors.flush(ctx, tx, res)
}

func eventArgs(e event.Event) []any {
	a := e.Attr
	m := e.Measure
	return []any{
		e.DedupKey(), int64(e.TS), int64(e.Hour()), nullStr(e.SessionID), e.Vendor,
		string(e.Signal), e.Name, e.InstallationID,
		nullStr(e.EventID), nullStr(e.TraceID), nullStr(e.SpanID),

		nullStr(a.Model), nullStr(a.Type), nullStr(a.ToolName), nullStr(a.Decision),
		nullStr(a.DecisionSource), nullStr(a.Language), nullStr(a.QuerySource),
		nullStr(a.Speed), nullStr(a.Effort), nullStr(a.AgentName), nullStr(a.SkillName),
		nullStr(a.PluginName), nullStr(a.MCPServer), nullStr(a.MCPTool),
		nullStr(a.StartType), nullStr(a.TerminalType), nullStr(a.AppVersion),
		nullStr(a.Entrypoint), nullStr(a.Environment),
		nullStr(a.ProjectHash), nullStr(a.ProjectName),

		optFloat(m.Value), nullStr(m.Unit), optFloat(m.CostUSD),
		optInt(m.InputTokens), optInt(m.OutputTokens),
		optInt(m.CacheReadTokens), optInt(m.CacheCreationTokens),
		optInt(m.DurationMS), optInt(m.StatusCode), optInt(m.Attempt),
		optBool(m.Success), nullStr(m.ErrorType),
		optInt(m.PromptLength), optInt(m.ResponseLength),
		optInt(m.ToolInputBytes), optInt(m.ToolResultBytes),
	}
}

// ── vendors ─────────────────────────────────────────────────────────────────

type vendorSpan struct {
	first, last event.UnixSec
	count       int64
}

type vendorTally struct {
	order []string
	byKey map[string]*vendorSpan
}

func newVendorTally() *vendorTally {
	return &vendorTally{byKey: make(map[string]*vendorSpan)}
}

// observe 는 실제로 저장된 이벤트만 센다. 중복까지 세면 events_total 이 events 테이블의
// 행 수와 어긋나 Settings 화면의 숫자를 아무도 설명하지 못한다.
func (t *vendorTally) observe(vendor string, ts event.UnixSec) {
	s, ok := t.byKey[vendor]
	if !ok {
		t.byKey[vendor] = &vendorSpan{first: ts, last: ts, count: 1}
		t.order = append(t.order, vendor)
		return
	}
	if ts < s.first {
		s.first = ts
	}
	if ts > s.last {
		s.last = ts
	}
	s.count++
}

const upsertVendorSQL = `INSERT INTO vendors (vendor, first_seen, last_seen, events_total)
VALUES (?,?,?,?)
ON CONFLICT(vendor) DO UPDATE SET
  first_seen   = MIN(first_seen, excluded.first_seen),
  last_seen    = MAX(last_seen, excluded.last_seen),
  events_total = events_total + excluded.events_total`

func (t *vendorTally) flush(ctx context.Context, tx *sql.Tx, res *WriteResult) error {
	for _, v := range t.order {
		s := t.byKey[v]
		if _, err := tx.ExecContext(ctx, upsertVendorSQL, v, int64(s.first), int64(s.last), s.count); err != nil {
			return fmt.Errorf("store: vendors UPSERT (%s): %w", v, err)
		}
		res.VendorsTouched++
	}
	return nil
}

// ── sessions 와 종속 테이블 ─────────────────────────────────────────────────

const upsertSessionSQL = `INSERT INTO sessions (
  session_id, vendor, started_at, last_event_at, ended_at, status,
  title, title_source, summary, project_hash, project_name,
  duration_ms, active_seconds,
  input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cost_usd,
  tool_calls, tool_errors, tool_rejects,
  api_requests, api_errors, retries,
  prompts, responses, lines_added, lines_removed
) VALUES (?,?,?,?,?,?, ?,?,?,?,?, ?,?, ?,?,?,?,?, ?,?,?, ?,?,?, ?,?,?,?)
ON CONFLICT(session_id) DO UPDATE SET
  vendor = excluded.vendor,
  started_at = excluded.started_at,
  last_event_at = excluded.last_event_at,
  ended_at = excluded.ended_at,
  status = excluded.status,
  title = excluded.title,
  title_source = excluded.title_source,
  summary = excluded.summary,
  project_hash = excluded.project_hash,
  project_name = excluded.project_name,
  duration_ms = excluded.duration_ms,
  active_seconds = excluded.active_seconds,
  input_tokens = excluded.input_tokens,
  output_tokens = excluded.output_tokens,
  cache_read_tokens = excluded.cache_read_tokens,
  cache_creation_tokens = excluded.cache_creation_tokens,
  cost_usd = excluded.cost_usd,
  tool_calls = excluded.tool_calls,
  tool_errors = excluded.tool_errors,
  tool_rejects = excluded.tool_rejects,
  api_requests = excluded.api_requests,
  api_errors = excluded.api_errors,
  retries = excluded.retries,
  prompts = excluded.prompts,
  responses = excluded.responses,
  lines_added = excluded.lines_added,
  lines_removed = excluded.lines_removed`

// phase_json 과 work_type 은 UPSERT 목록에 없다. 후속 티켓이 채울 컬럼이라 여기서 건드리면
// 다음 세션 스냅샷이 그 티켓의 결과를 NULL 로 덮는다.

const upsertFileSQL = `INSERT INTO session_files (
  session_id, file_path_hash, file_name, file_ext,
  lines_added, lines_removed, edits, last_ts
) VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(session_id, file_path_hash) DO UPDATE SET
  file_name = excluded.file_name,
  file_ext = excluded.file_ext,
  lines_added = excluded.lines_added,
  lines_removed = excluded.lines_removed,
  edits = excluded.edits,
  last_ts = excluded.last_ts`

const upsertMCPSQL = `INSERT INTO mcp_session_usage (
  session_id, server_name, connected, connect_failures, tool_calls, tokens
) VALUES (?,?,?,?,?,?)
ON CONFLICT(session_id, server_name) DO UPDATE SET
  connected = excluded.connected,
  connect_failures = excluded.connect_failures,
  tool_calls = excluded.tool_calls,
  tokens = excluded.tokens`

const insertToolEventSQL = `INSERT INTO tool_events (
  session_id, ts, tool_name, action, target_name, target_hash,
  success, duration_ms, error_type, decision, mcp_server
) VALUES (?,?,?,?,?,?,?,?,?,?,?)`

// writeSessions 는 세션 스냅샷을 멱등하게 쓴다.
//
// # 왜 대입이고 누적이 아닌가
//
// 조립기는 세션 하나의 **현재 전체 상태**를 돌려준다. 같은 세션이 매 틱마다 다시 나오고
// 그때마다 수치는 처음부터 다시 센 값이다. 그러므로 여기서 더하면 정확히 틱 수만큼 부풀고,
// 대입해야 맞는다. rollup_hourly 가 정반대인 이유도 같다 — 집계기는 Flush 로 버킷을 비우고
// 증분만 주므로 그쪽은 더해야 맞는다.
//
// # tool_events 멱등성
//
// tool_events 에는 자연키가 없다. session.ToolEvent 에는 이벤트를 되짚을 dedup_key 가 없고
// (session 패키지는 수정 대상이 아니다) (session_id, ts, tool_name, target_hash) 를 유니크로
// 삼으면 같은 초에 같은 파일을 두 번 읽은 진짜 두 건이 한 건으로 접힌다 — tool_events.ts 는
// 초 단위라 이 충돌이 드물지 않다. 타임라인을 조용히 줄이는 쪽이 두 배로 만드는 쪽보다
// 낫지도 않다.
//
// 그래서 스냅샷을 정본으로 보고 **세션 단위로 지우고 다시 쓴다**. 조립기가 항상 전체
// 타임라인을 주므로 결과가 정확히 멱등하고, 같은 초의 서로 다른 두 호출도 보존된다.
// 비용은 세션당 타임라인 길이만큼의 재삽입이고, 세션 하나의 타임라인은 수십~수백 행이다.
func writeSessions(ctx context.Context, tx *sql.Tx, sessions []session.Session, res *WriteResult) error {
	if len(sessions) == 0 {
		return nil
	}
	sessStmt, err := tx.PrepareContext(ctx, upsertSessionSQL)
	if err != nil {
		return fmt.Errorf("store: sessions UPSERT 준비: %w", err)
	}
	defer sessStmt.Close()

	fileStmt, err := tx.PrepareContext(ctx, upsertFileSQL)
	if err != nil {
		return fmt.Errorf("store: session_files UPSERT 준비: %w", err)
	}
	defer fileStmt.Close()

	mcpStmt, err := tx.PrepareContext(ctx, upsertMCPSQL)
	if err != nil {
		return fmt.Errorf("store: mcp_session_usage UPSERT 준비: %w", err)
	}
	defer mcpStmt.Close()

	toolStmt, err := tx.PrepareContext(ctx, insertToolEventSQL)
	if err != nil {
		return fmt.Errorf("store: tool_events INSERT 준비: %w", err)
	}
	defer toolStmt.Close()

	for _, s := range sessions {
		if s.SessionID == "" {
			return fmt.Errorf("store: session_id 가 빈 세션")
		}
		if _, err := sessStmt.ExecContext(ctx, sessionArgs(s)...); err != nil {
			return fmt.Errorf("store: sessions UPSERT (%s): %w", s.SessionID, err)
		}
		res.SessionsUpserted++

		for _, f := range s.Files {
			if _, err := fileStmt.ExecContext(ctx,
				s.SessionID, f.PathHash, f.Name, nullStr(f.Ext),
				f.LinesAdded, f.LinesRemoved, f.Edits, int64(f.LastTS),
			); err != nil {
				return fmt.Errorf("store: session_files UPSERT (%s): %w", s.SessionID, err)
			}
			res.FilesUpserted++
		}

		for _, m := range s.MCP {
			if _, err := mcpStmt.ExecContext(ctx,
				s.SessionID, m.ServerName, boolInt(m.Connected),
				m.ConnectFailures, m.ToolCalls, m.Tokens,
			); err != nil {
				return fmt.Errorf("store: mcp_session_usage UPSERT (%s): %w", s.SessionID, err)
			}
			res.MCPUpserted++
		}

		out, err := tx.ExecContext(ctx, `DELETE FROM tool_events WHERE session_id = ?`, s.SessionID)
		if err != nil {
			return fmt.Errorf("store: tool_events 초기화 (%s): %w", s.SessionID, err)
		}
		if n, err := out.RowsAffected(); err == nil {
			res.ToolEventsDeleted += int(n)
		}
		for _, t := range s.Tools {
			if _, err := toolStmt.ExecContext(ctx,
				s.SessionID, int64(t.TS), t.ToolName, nullStr(string(t.Action)),
				nullStr(t.TargetName), nullStr(t.TargetHash),
				optBool(t.Success), optInt(t.DurationMS),
				nullStr(t.ErrorType), nullStr(t.Decision), nullStr(t.MCPServer),
			); err != nil {
				return fmt.Errorf("store: tool_events INSERT (%s): %w", s.SessionID, err)
			}
			res.ToolEventsWritten++
		}
	}
	return nil
}

func sessionArgs(s session.Session) []any {
	var endedAt any
	if v, ok := s.EndedAt.Get(); ok {
		endedAt = int64(v)
	}
	return []any{
		s.SessionID, s.Vendor, int64(s.StartedAt), int64(s.LastEventAt), endedAt, string(s.Status),
		nullStr(s.Title), nullStr(string(s.TitleSource)), nullStr(s.Summary),
		nullStr(s.ProjectHash), nullStr(s.ProjectName),
		s.DurationMS, s.ActiveSeconds,
		s.InputTokens, s.OutputTokens, s.CacheReadTokens, s.CacheCreationTokens, s.CostUSD,
		s.ToolCalls, s.ToolErrors, s.ToolRejects,
		s.APIRequests, s.APIErrors, s.Retries,
		s.Prompts, s.Responses, s.LinesAdded, s.LinesRemoved,
	}
}

// ── rollup_hourly ───────────────────────────────────────────────────────────

// 컬럼 순서는 rollup.Bucket 의 필드 순서를 따른다.
const upsertRollupSQL = `INSERT INTO rollup_hourly (
  hour, dim, "key",
  cost_usd, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens,
  api_requests, api_errors, retries, lines_added, lines_removed,
  commits, pull_requests, prompts, tool_calls, tool_accepts, tool_rejects,
  active_seconds, sessions_started
) VALUES (?,?,?, ?,?,?,?,?, ?,?,?,?,?, ?,?,?,?,?,?, ?,?)
ON CONFLICT(hour, dim, "key") DO UPDATE SET
  cost_usd              = cost_usd + excluded.cost_usd,
  input_tokens          = input_tokens + excluded.input_tokens,
  output_tokens         = output_tokens + excluded.output_tokens,
  cache_read_tokens     = cache_read_tokens + excluded.cache_read_tokens,
  cache_creation_tokens = cache_creation_tokens + excluded.cache_creation_tokens,
  api_requests          = api_requests + excluded.api_requests,
  api_errors            = api_errors + excluded.api_errors,
  retries               = retries + excluded.retries,
  lines_added           = lines_added + excluded.lines_added,
  lines_removed         = lines_removed + excluded.lines_removed,
  commits               = commits + excluded.commits,
  pull_requests         = pull_requests + excluded.pull_requests,
  prompts               = prompts + excluded.prompts,
  tool_calls            = tool_calls + excluded.tool_calls,
  tool_accepts          = tool_accepts + excluded.tool_accepts,
  tool_rejects          = tool_rejects + excluded.tool_rejects,
  active_seconds        = active_seconds + excluded.active_seconds,
  sessions_started      = sessions_started + excluded.sessions_started`

// writeRollups 는 시간 버킷을 **누적** UPSERT 한다.
//
// 집계기는 Flush 로 버킷을 비우고 그 이후의 증분만 다시 준다. 덮어쓰면 플러시 사이의 값만
// 남아 하루치 비용이 마지막 몇 분치로 줄어든다.
func writeRollups(ctx context.Context, tx *sql.Tx, rows []rollup.Row, res *WriteResult) error {
	if len(rows) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, upsertRollupSQL)
	if err != nil {
		return fmt.Errorf("store: rollup_hourly UPSERT 준비: %w", err)
	}
	defer stmt.Close()

	for _, r := range rows {
		if r.Dim == "" {
			return fmt.Errorf("store: rollup dim 이 비어 있음 (hour=%d)", int64(r.Hour))
		}
		if r.Bucket.IsZero() {
			// 기여분이 0 인 행은 건너뛴다. 넣어도 값이 바뀌지 않고 (hour, dim, key) 만 늘어난다.
			continue
		}
		b := r.Bucket
		if _, err := stmt.ExecContext(ctx,
			int64(r.Hour), string(r.Dim), r.Key,
			b.CostUSD, b.InputTokens, b.OutputTokens, b.CacheReadTokens, b.CacheCreationTokens,
			b.APIRequests, b.APIErrors, b.Retries, b.LinesAdded, b.LinesRemoved,
			b.Commits, b.PullRequests, b.Prompts, b.ToolCalls, b.ToolAccepts, b.ToolRejects,
			b.ActiveSeconds, b.SessionsStarted,
		); err != nil {
			return fmt.Errorf("store: rollup_hourly UPSERT (hour=%d dim=%s): %w", int64(r.Hour), r.Dim, err)
		}
		res.RollupRows++
	}
	return nil
}

// ── NULL 변환 ───────────────────────────────────────────────────────────────

// nullStr 은 빈 문자열을 NULL 로 옮긴다. 문자열 컬럼에서는 ""와 NULL 을 구분할 이유가 없고,
// NULL 로 통일해야 조회 쪽에서 IS NULL 하나만 보면 된다.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// optInt·optFloat·optBool 은 event.Opt 의 미설정을 NULL 로 옮긴다.
// 0 으로 눕히면 "비용 0" 과 "비용 개념 없음" 이 같아져 rollup 의 분모가 조용히 부푼다.
func optInt(o event.Opt[int64]) any {
	if v, ok := o.Get(); ok {
		return v
	}
	return nil
}

func optFloat(o event.Opt[float64]) any {
	if v, ok := o.Get(); ok {
		return v
	}
	return nil
}

func optBool(o event.Opt[bool]) any {
	if v, ok := o.Get(); ok {
		return boolInt(v)
	}
	return nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
