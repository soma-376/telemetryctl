package store

// meta 테이블의 키. 계획서 스키마 주석이 나열한 네 가지다.
const (
	// MetaSchemaVersion 은 마이그레이션 러너가 소유한다. 다른 곳에서 쓰지 않는다.
	MetaSchemaVersion = "local_schema_version"
	// MetaInstallationID 는 enrollment 의 installation_id 사본이다. v3 도메인 DDL 에는
	// installation_id 컬럼이 없지만 DB 하나가 어느 설치의 것인지 파일만 보고 알 수 있게 남긴다.
	MetaInstallationID = "installation_id"
	// MetaRetentionDays 는 기존 런타임이 마지막으로 기록한 고정 보존일이다.
	MetaRetentionDays = "retention_days"
	// MetaLastRollupAt 는 기존 런타임이 기록한 마지막 롤업 플러시 시각(unix 초)이다.
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

// schemaV2 는 세션 안의 턴과 연속된 턴 범위의 단계 분류 결과를 저장할 자리를 추가한다.
// 실제 턴 조립·분류는 후속 작업의 몫이라 기존 행을 추측해 백필하지 않는다.
var schemaV2 = []string{
	`CREATE TABLE turns (
  session_id          TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  turn_index          INTEGER NOT NULL,
  started_at          INTEGER NOT NULL,
  last_event_at       INTEGER NOT NULL,
  ended_at            INTEGER,
  prompt_length       INTEGER NOT NULL DEFAULT 0,
  work_type           TEXT,
  duration_ms         INTEGER NOT NULL DEFAULT 0,
  active_seconds      REAL NOT NULL DEFAULT 0,
  input_tokens        INTEGER NOT NULL DEFAULT 0,
  output_tokens       INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens   INTEGER NOT NULL DEFAULT 0,
  cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
  cost_usd            REAL NOT NULL DEFAULT 0,
  tool_calls          INTEGER NOT NULL DEFAULT 0,
  tool_errors         INTEGER NOT NULL DEFAULT 0,
  tool_rejects        INTEGER NOT NULL DEFAULT 0,
  api_requests        INTEGER NOT NULL DEFAULT 0,
  api_errors          INTEGER NOT NULL DEFAULT 0,
  retries             INTEGER NOT NULL DEFAULT 0,
  responses           INTEGER NOT NULL DEFAULT 0,
  lines_added         INTEGER NOT NULL DEFAULT 0,
  lines_removed       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (session_id, turn_index)
) WITHOUT ROWID`,

	`CREATE TABLE session_phases (
  session_id          TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
  phase_index         INTEGER NOT NULL,
  phase_type          TEXT NOT NULL,
  start_turn_index    INTEGER NOT NULL,
  end_turn_index      INTEGER NOT NULL,
  started_at          INTEGER NOT NULL,
  last_event_at       INTEGER NOT NULL,
  turn_count          INTEGER NOT NULL DEFAULT 0,
  confidence          REAL,
  classifier          TEXT,
  classifier_version  TEXT,
  PRIMARY KEY (session_id, phase_index)
) WITHOUT ROWID`,

	`ALTER TABLE tool_events ADD COLUMN turn_index INTEGER`,
	`CREATE INDEX idx_tool_events_turn ON tool_events(session_id, turn_index, ts)`,
}

// schemaV3 는 로컬 저장 모델을 새 DDL 로 교체한다. v1/v2 행은 새 모델로 의미를 보존해
// 옮길 수 없으므로 백필하지 않고 기존 도메인 테이블을 모두 제거한 뒤 다시 만든다.
// meta 는 마이그레이션 러너가 소유하므로 삭제하지 않는다.
//
// DROP 은 외래 키 자식부터 부모 순서다. 전체 목록은 applyMigration 의 단일 트랜잭션에서
// 실행되므로 어느 문장에서든 실패하면 기존 스키마와 데이터가 함께 복구된다.
var schemaV3 = []string{
	`DROP TRIGGER event_content_ai`,
	`DROP TRIGGER event_content_ad`,
	`DROP TRIGGER event_content_au`,
	`DROP TABLE content_fts`,
	`DROP TABLE event_content`,
	`DROP TABLE session_phases`,
	`DROP TABLE session_files`,
	`DROP TABLE tool_events`,
	`DROP TABLE mcp_session_usage`,
	`DROP TABLE turns`,
	`DROP TABLE events`,
	`DROP TABLE rollup_hourly`,
	`DROP TABLE sessions`,
	`DROP TABLE vendors`,

	`CREATE TABLE vendors (
  vendor     TEXT PRIMARY KEY,
  first_seen INTEGER NOT NULL,
  last_seen  INTEGER NOT NULL,
  status     TEXT NOT NULL
    CHECK (status IN ('enabled', 'disabled', 'error'))
)`,

	`CREATE TABLE sessions (
  id              INTEGER PRIMARY KEY,
  vendor_id       TEXT NOT NULL REFERENCES vendors (vendor),
  session_key     TEXT NOT NULL,
  title           TEXT,
  workspace_path  TEXT,
  user_email      TEXT,
  user_account_id TEXT,
  terminal_type   TEXT,
  started_at      INTEGER,
  ended_at        INTEGER,
  active_time_sec INTEGER,
  UNIQUE (vendor_id, session_key)
)`,

	`CREATE TABLE turns (
  id             INTEGER PRIMARY KEY,
  session_id     INTEGER NOT NULL REFERENCES sessions (id),
  turn_key       TEXT NOT NULL,
  turn_index     INTEGER,
  client_version TEXT,
  started_at     INTEGER,
  ended_at       INTEGER,
  prompt_text    TEXT,
  ttft_ms        INTEGER,
  UNIQUE (session_id, turn_key),
  UNIQUE (session_id, turn_index)
)`,
	`CREATE UNIQUE INDEX ux_turns_virtual ON turns (session_id) WHERE turn_index IS NULL`,

	`CREATE TABLE events (
  id          INTEGER PRIMARY KEY,
  turn_id     INTEGER NOT NULL REFERENCES turns (id),
  seq         INTEGER NOT NULL,
  event_name  TEXT NOT NULL,
  occurred_at INTEGER,
  record_hash TEXT NOT NULL UNIQUE,
  payload     BLOB
    CHECK (payload IS NULL OR json_valid(payload, 8)),
  UNIQUE (turn_id, seq)
)`,
	`CREATE INDEX ix_events_name ON events (event_name)`,

	`CREATE TABLE llm_calls (
  id                 INTEGER PRIMARY KEY,
  turn_id            INTEGER NOT NULL REFERENCES turns (id),
  source_event_id    INTEGER NOT NULL UNIQUE REFERENCES events (id),
  called_at          INTEGER,
  model              TEXT,
  input_tokens       INTEGER,
  output_tokens      INTEGER,
  cache_read_tokens  INTEGER,
  cache_write_tokens INTEGER,
  reasoning_tokens   INTEGER,
  cost_usd           NUMERIC,
  duration_ms        INTEGER,
  request_id         TEXT
)`,
	`CREATE INDEX ix_llm_turn ON llm_calls (turn_id)`,

	`CREATE TABLE tool_calls (
  id                 INTEGER PRIMARY KEY,
  turn_id            INTEGER NOT NULL REFERENCES turns (id),
  call_key           TEXT NOT NULL UNIQUE,
  decision_event_id  INTEGER UNIQUE REFERENCES events (id),
  result_event_id    INTEGER UNIQUE REFERENCES events (id),
  tool_name          TEXT,
  target             TEXT,
  mcp_server         TEXT,
  called_at          INTEGER,
  duration_ms        INTEGER,
  blocked_on_user_ms INTEGER,
  success            INTEGER,
  decision           TEXT,
  decision_source    TEXT,
  input_size_bytes   INTEGER,
  result_size_bytes  INTEGER,
  error_type         TEXT,
  error_message      TEXT,
  CHECK (decision_event_id IS NOT NULL OR result_event_id IS NOT NULL)
)`,

	`CREATE TABLE file_changes (
  id           INTEGER PRIMARY KEY,
  tool_call_id INTEGER NOT NULL REFERENCES tool_calls (id),
  file_path    TEXT NOT NULL,
  operation    TEXT NOT NULL
    CHECK (operation IN ('create', 'modify', 'delete', 'rename')),
  renamed_from TEXT,
  additions    INTEGER,
  deletions    INTEGER,
  old_hash     TEXT,
  new_hash     TEXT,
  CHECK (operation <> 'rename' OR renamed_from IS NOT NULL)
)`,
	`CREATE INDEX ix_fc_tool ON file_changes (tool_call_id)`,
}
