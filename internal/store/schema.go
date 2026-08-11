package store

// meta 테이블의 키. 계획서 스키마 주석이 나열한 네 가지다.
const (
	// MetaSchemaVersion 은 마이그레이션 러너가 소유한다. 다른 곳에서 쓰지 않는다.
	MetaSchemaVersion = "local_schema_version"
	// MetaInstallationID 는 enrollment 의 installation_id 사본이다. events.installation_id 와
	// 같은 값이라 DB 하나가 어느 설치의 것인지 파일만 보고 알 수 있다.
	MetaInstallationID = "installation_id"
	// MetaRetentionDays 는 Settings 「데이터 보존 기간」의 마지막 적용값이다.
	MetaRetentionDays = "retention_days"
	// MetaLastRollupAt 는 마지막 롤업 플러시 시각(unix 초)이다.
	MetaLastRollupAt = "last_rollup_at"
)

// createMetaTable 은 마이그레이션 러너가 버전을 읽기 전에 실행한다.
// meta 자체는 어느 마이그레이션에도 속하지 않는다 — 버전을 담는 그릇이 버전 관리 대상이면
// 첫 실행에서 닭과 달걀이 된다.
const createMetaTable = `CREATE TABLE IF NOT EXISTS meta (
  "key"  TEXT PRIMARY KEY,
  value  TEXT NOT NULL
)`

// schemaV1 은 계획서 「스키마」의 DDL 이다. 컬럼·인덱스·WITHOUT ROWID·ON DELETE CASCADE 는
// 계획서 그대로다. 계획서가 한 줄로 줄여 쓴 축약(`input_tokens, output_tokens, ... INTEGER
// NOT NULL DEFAULT 0`)만 실제 DDL 로 풀어 썼다.
//
// 문장을 하나씩 나눠 둔 이유는 마이그레이션 러너가 순서대로 실행하고 실패한 문장을 지목할 수
// 있어야 하기 때문이다.
var schemaV1 = []string{
	// ── 세션: 화면의 중심 ────────────────────────────────────────────────────
	`CREATE TABLE sessions (
  session_id    TEXT PRIMARY KEY,
  vendor        TEXT NOT NULL,
  started_at    INTEGER NOT NULL,
  last_event_at INTEGER NOT NULL,
  ended_at      INTEGER,
  status        TEXT NOT NULL,

  title         TEXT,
  title_source  TEXT,
  summary       TEXT,
  project_hash  TEXT,
  project_name  TEXT,

  duration_ms           INTEGER NOT NULL DEFAULT 0,
  active_seconds        REAL    NOT NULL DEFAULT 0,
  input_tokens          INTEGER NOT NULL DEFAULT 0,
  output_tokens         INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens     INTEGER NOT NULL DEFAULT 0,
  cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
  cost_usd              REAL    NOT NULL DEFAULT 0,
  tool_calls            INTEGER NOT NULL DEFAULT 0,
  tool_errors           INTEGER NOT NULL DEFAULT 0,
  tool_rejects          INTEGER NOT NULL DEFAULT 0,
  api_requests          INTEGER NOT NULL DEFAULT 0,
  api_errors            INTEGER NOT NULL DEFAULT 0,
  retries               INTEGER NOT NULL DEFAULT 0,
  prompts               INTEGER NOT NULL DEFAULT 0,
  responses             INTEGER NOT NULL DEFAULT 0,
  lines_added           INTEGER NOT NULL DEFAULT 0,
  lines_removed         INTEGER NOT NULL DEFAULT 0,

  phase_json    TEXT,
  work_type     TEXT
)`,
	`CREATE INDEX idx_sessions_started ON sessions(started_at)`,
	`CREATE INDEX idx_sessions_status  ON sessions(status, last_event_at)`,
	`CREATE INDEX idx_sessions_vendor  ON sessions(vendor, started_at)`,
	`CREATE INDEX idx_sessions_project ON sessions(project_hash, started_at)`,

	// ── 파일 변경 · 툴 타임라인 ──────────────────────────────────────────────
	`CREATE TABLE session_files (
  session_id     TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  file_path_hash TEXT NOT NULL,
  file_name      TEXT NOT NULL,
  file_ext       TEXT,
  lines_added    INTEGER NOT NULL DEFAULT 0,
  lines_removed  INTEGER NOT NULL DEFAULT 0,
  edits          INTEGER NOT NULL DEFAULT 0,
  last_ts        INTEGER NOT NULL,
  PRIMARY KEY (session_id, file_path_hash)
) WITHOUT ROWID`,

	`CREATE TABLE tool_events (
  id          INTEGER PRIMARY KEY,
  session_id  TEXT    NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  ts          INTEGER NOT NULL,
  tool_name   TEXT    NOT NULL,
  action      TEXT,
  target_name TEXT,
  target_hash TEXT,
  success     INTEGER,
  duration_ms INTEGER,
  error_type  TEXT,
  decision    TEXT,
  mcp_server  TEXT
)`,
	`CREATE INDEX idx_tool_events_session ON tool_events(session_id, ts)`,

	// ── MCP · 벤더 상태 ─────────────────────────────────────────────────────
	`CREATE TABLE mcp_session_usage (
  session_id  TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  server_name TEXT NOT NULL,
  connected        INTEGER NOT NULL DEFAULT 0,
  connect_failures INTEGER NOT NULL DEFAULT 0,
  tool_calls       INTEGER NOT NULL DEFAULT 0,
  tokens           INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (session_id, server_name)
) WITHOUT ROWID`,

	`CREATE TABLE vendors (
  vendor       TEXT PRIMARY KEY,
  first_seen   INTEGER NOT NULL,
  last_seen    INTEGER NOT NULL,
  events_total INTEGER NOT NULL DEFAULT 0
)`,

	// ── 원본 이벤트 ─────────────────────────────────────────────────────────
	// 속성은 allowlist 다. catch-all 컬럼이 없으므로 여기 없는 속성은 저장될 자리가 없다
	// (ADR 0003). event.Attributes 의 필드 순서를 그대로 따른다.
	`CREATE TABLE events (
  id              INTEGER PRIMARY KEY,
  dedup_key       TEXT    NOT NULL UNIQUE,
  ts              INTEGER NOT NULL,
  hour            INTEGER NOT NULL,
  session_id      TEXT,
  vendor          TEXT    NOT NULL,
  signal          TEXT    NOT NULL,
  name            TEXT    NOT NULL,
  installation_id TEXT    NOT NULL,
  event_id        TEXT,
  trace_id        TEXT,
  span_id         TEXT,

  model           TEXT,
  type            TEXT,
  tool_name       TEXT,
  decision        TEXT,
  decision_source TEXT,
  language        TEXT,
  query_source    TEXT,
  speed           TEXT,
  effort          TEXT,
  agent_name      TEXT,
  skill_name      TEXT,
  plugin_name     TEXT,
  mcp_server      TEXT,
  mcp_tool        TEXT,
  start_type      TEXT,
  terminal_type   TEXT,
  app_version     TEXT,
  entrypoint      TEXT,
  environment     TEXT,
  project_hash    TEXT,
  project_name    TEXT,

  value                 REAL,
  unit                  TEXT,
  cost_usd              REAL,
  input_tokens          INTEGER,
  output_tokens         INTEGER,
  cache_read_tokens     INTEGER,
  cache_creation_tokens INTEGER,
  duration_ms           INTEGER,
  status_code           INTEGER,
  attempt               INTEGER,
  success               INTEGER,
  error_type            TEXT,
  prompt_length         INTEGER,
  response_length       INTEGER,
  tool_input_bytes      INTEGER,
  tool_result_bytes     INTEGER
)`,
	`CREATE INDEX idx_events_hour    ON events(hour)`,
	`CREATE INDEX idx_events_session ON events(session_id, ts)`,

	// ── 원문 ────────────────────────────────────────────────────────────────
	// 계획서 DDL 은 `event_id INTEGER PRIMARY KEY` 였다. 이벤트당 원문이 하나라는 가정인데
	// 디코더는 한 이벤트에서 최대 4종(prompt·response·tool_input·tool_result)을 뽑는다 —
	// claude_code.tool_result 로그 한 건이 tool_input 과 tool_result 를 함께 실어 오는 것이
	// 예외가 아니라 기본 경로다. 계획서대로 두면 그중 하나만 남고 나머지는 조용히 사라진다.
	//
	// 그래서 대리 키 id 를 두고 (event_id, kind) 를 UNIQUE 로 잡았다. content_rowid 는
	// FTS5 external content 규약상 content 테이블의 rowid 여야 하므로 id 를 가리킨다.
	`CREATE TABLE event_content (
  id        INTEGER PRIMARY KEY,
  event_id  INTEGER NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  kind      TEXT    NOT NULL,
  body      TEXT    NOT NULL,
  truncated INTEGER NOT NULL DEFAULT 0,
  UNIQUE (event_id, kind)
)`,
	`CREATE VIRTUAL TABLE content_fts USING fts5(
  body, content='event_content', content_rowid='id'
)`,

	// external content FTS5 는 원본 테이블을 자동으로 따라가지 않는다. 트리거를 빠뜨리면
	// 색인이 영원히 비어 있고 검색이 "결과 없음" 으로 조용히 실패한다 (FTS5 문서 4.4.3).
	`CREATE TRIGGER event_content_ai AFTER INSERT ON event_content BEGIN
  INSERT INTO content_fts(rowid, body) VALUES (new.id, new.body);
END`,
	`CREATE TRIGGER event_content_ad AFTER DELETE ON event_content BEGIN
  INSERT INTO content_fts(content_fts, rowid, body) VALUES ('delete', old.id, old.body);
END`,
	`CREATE TRIGGER event_content_au AFTER UPDATE ON event_content BEGIN
  INSERT INTO content_fts(content_fts, rowid, body) VALUES ('delete', old.id, old.body);
  INSERT INTO content_fts(rowid, body) VALUES (new.id, new.body);
END`,

	// ── 시간 버킷 롤업 ──────────────────────────────────────────────────────
	// 컬럼 순서는 rollup.Bucket 의 필드 순서와 같다. 어긋나면 INSERT 인자 나열이 조용히 밀린다.
	`CREATE TABLE rollup_hourly (
  hour INTEGER NOT NULL,
  dim  TEXT    NOT NULL,
  "key" TEXT   NOT NULL,
  cost_usd              REAL    NOT NULL DEFAULT 0,
  input_tokens          INTEGER NOT NULL DEFAULT 0,
  output_tokens         INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens     INTEGER NOT NULL DEFAULT 0,
  cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
  api_requests          INTEGER NOT NULL DEFAULT 0,
  api_errors            INTEGER NOT NULL DEFAULT 0,
  retries               INTEGER NOT NULL DEFAULT 0,
  lines_added           INTEGER NOT NULL DEFAULT 0,
  lines_removed         INTEGER NOT NULL DEFAULT 0,
  commits               INTEGER NOT NULL DEFAULT 0,
  pull_requests         INTEGER NOT NULL DEFAULT 0,
  prompts               INTEGER NOT NULL DEFAULT 0,
  tool_calls            INTEGER NOT NULL DEFAULT 0,
  tool_accepts          INTEGER NOT NULL DEFAULT 0,
  tool_rejects          INTEGER NOT NULL DEFAULT 0,
  active_seconds        REAL    NOT NULL DEFAULT 0,
  sessions_started      INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (hour, dim, "key")
) WITHOUT ROWID`,
}
