package store

const (
	MetaSchemaVersion  = "local_schema_version"
	MetaInstallationID = "installation_id"
	MetaRetentionDays  = "retention_days"
	MetaLastRollupAt   = "last_rollup_at"
)

const createMetaTable = `CREATE TABLE IF NOT EXISTS meta (
  "key" TEXT NOT NULL PRIMARY KEY,
  value TEXT NOT NULL
)`

// schemaSQL 은 새 DB에 적용하는 최신 전체 DDL의 단일 진실원이다.
var schemaSQL = `
CREATE TABLE vendors (
  vendor TEXT NOT NULL PRIMARY KEY,
  first_seen INTEGER NOT NULL,
  last_seen INTEGER NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('enabled', 'disabled', 'error'))
);

CREATE TABLE sessions (
  id INTEGER PRIMARY KEY,
  vendor_id TEXT NOT NULL REFERENCES vendors (vendor),
  session_key TEXT NOT NULL,
  title TEXT,
  workspace_path TEXT,
  user_email TEXT,
  user_account_id TEXT,
  terminal_type TEXT,
  started_at INTEGER,
  ended_at INTEGER,
  last_activity_at INTEGER,
  active_time_sec INTEGER CHECK (active_time_sec >= 0),
  UNIQUE (vendor_id, session_key),
  CHECK (ended_at >= started_at)
);
CREATE INDEX ix_sessions_started ON sessions (started_at);

-- 유휴 마감 스윕용. 부분 인덱스여야 400일치 마감 세션이 딸려 들어오지 않는다.
CREATE INDEX ix_sessions_open_activity ON sessions (last_activity_at) WHERE ended_at IS NULL;

CREATE TABLE turns (
  id INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
  turn_key TEXT NOT NULL,
  turn_index INTEGER CHECK (turn_index IS NULL OR (typeof(turn_index) = 'integer' AND turn_index >= 0)),
  client_version TEXT,
  started_at INTEGER,
  ended_at INTEGER,
  prompt_text TEXT,
  prompt_message_id TEXT,
  prompt_source_turn_id TEXT,
  prompt_completeness TEXT NOT NULL DEFAULT 'unknown' CHECK (prompt_completeness IN ('complete', 'truncated', 'unknown')),
  prompt_completeness_evidence TEXT,
  prompt_evidence_event_id INTEGER,
  prompt_conflict INTEGER NOT NULL DEFAULT 0 CHECK (prompt_conflict IN (0, 1)),
  content_purged INTEGER NOT NULL DEFAULT 0 CHECK (content_purged IN (0, 1)),
  ttft_ms INTEGER CHECK (ttft_ms >= 0),
  UNIQUE (session_id, turn_key),
  UNIQUE (session_id, turn_index),
  CHECK (ended_at >= started_at)
);
CREATE UNIQUE INDEX ux_turns_virtual ON turns (session_id) WHERE turn_index IS NULL;
CREATE INDEX ix_turns_session ON turns (session_id);

CREATE TABLE codex_turn_provenance (
  turn_id INTEGER PRIMARY KEY REFERENCES turns (id) ON DELETE CASCADE,
  label TEXT NOT NULL CHECK (label IN ('client_submitted', 'internal_task', 'unknown')),
  processing_state TEXT NOT NULL CHECK (processing_state IN ('pending', 'retrying', 'finalized')),
  link_state TEXT NOT NULL CHECK (link_state IN ('unique', 'ambiguous', 'conflict', 'unmatched', 'unavailable', 'not_attempted')),
  link_method TEXT NOT NULL DEFAULT '',
  linked_record_id INTEGER,
  classifier_version TEXT NOT NULL DEFAULT '',
  structure_evidence TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(structure_evidence)),
  reason TEXT NOT NULL DEFAULT '',
  checked_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX ix_codex_provenance_state ON codex_turn_provenance (processing_state, label, checked_at);

CREATE TABLE codex_pending (
  turn_id INTEGER PRIMARY KEY REFERENCES turns (id) ON DELETE CASCADE,
  next_check_at INTEGER NOT NULL DEFAULT 0,
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX ix_codex_pending_due ON codex_pending (next_check_at, turn_id);

CREATE TABLE codex_jsonl_files (
  id INTEGER PRIMARY KEY,
  path TEXT NOT NULL,
  generation INTEGER NOT NULL CHECK (generation >= 1),
  os_file_id TEXT NOT NULL,
  owner_session_key TEXT NOT NULL,
  parent_session_key TEXT NOT NULL DEFAULT '',
  owner_source TEXT NOT NULL DEFAULT '{}',
  committed_offset INTEGER NOT NULL DEFAULT 0 CHECK (committed_offset >= 0),
  committed_prefix_hash TEXT NOT NULL DEFAULT '',
  observed_size INTEGER NOT NULL DEFAULT 0,
  observed_mod_ns INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'rotated', 'superseded', 'quarantined', 'missing')),
  observed_at INTEGER NOT NULL,
  UNIQUE (path, generation)
);
CREATE INDEX ix_codex_jsonl_files_owner ON codex_jsonl_files (owner_session_key, status);

CREATE TABLE codex_jsonl_records (
  id INTEGER PRIMARY KEY,
  file_id INTEGER NOT NULL REFERENCES codex_jsonl_files (id) ON DELETE CASCADE,
  start_offset INTEGER NOT NULL CHECK (start_offset >= 0),
  end_offset INTEGER NOT NULL CHECK (end_offset > start_offset),
  record_type TEXT NOT NULL DEFAULT '',
  event_time INTEGER,
  message_id TEXT NOT NULL DEFAULT '',
  turn_id TEXT NOT NULL DEFAULT '',
  body TEXT,
  body_hash TEXT NOT NULL DEFAULT '',
  completeness TEXT NOT NULL DEFAULT 'unknown' CHECK (completeness IN ('complete', 'truncated', 'unknown')),
  completeness_evidence TEXT NOT NULL DEFAULT '',
  structure_evidence TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(structure_evidence)),
  representation_key TEXT NOT NULL DEFAULT '',
  UNIQUE (file_id, start_offset)
);
CREATE INDEX ix_codex_jsonl_records_time ON codex_jsonl_records (file_id, event_time);
CREATE INDEX ix_codex_jsonl_records_message ON codex_jsonl_records (message_id);
CREATE INDEX ix_codex_jsonl_records_turn ON codex_jsonl_records (turn_id);

CREATE TABLE codex_jsonl_errors (
  file_id INTEGER NOT NULL REFERENCES codex_jsonl_files (id) ON DELETE CASCADE,
  start_offset INTEGER NOT NULL,
  end_offset INTEGER NOT NULL,
  error_kind TEXT NOT NULL,
  diagnostic TEXT NOT NULL DEFAULT '',
  observed_at INTEGER NOT NULL,
  PRIMARY KEY (file_id, start_offset)
);

CREATE TABLE codex_content_tombstones (
  owner_session_key TEXT PRIMARY KEY,
  before_at INTEGER NOT NULL
);

CREATE TABLE codex_worker_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  epoch INTEGER NOT NULL CHECK (epoch >= 0),
  status TEXT NOT NULL CHECK (status IN ('stopped', 'running', 'backoff', 'degraded')),
  heartbeat_at INTEGER NOT NULL DEFAULT 0,
  job_started_at INTEGER NOT NULL DEFAULT 0,
  restart_count INTEGER NOT NULL DEFAULT 0,
  reason TEXT NOT NULL DEFAULT ''
);
INSERT INTO codex_worker_state (id, epoch, status) VALUES (1, 0, 'stopped');

CREATE TABLE events (
  id INTEGER PRIMARY KEY,
  turn_id INTEGER NOT NULL REFERENCES turns (id) ON DELETE CASCADE,
  seq INTEGER NOT NULL CHECK (typeof(seq) = 'integer' AND seq >= 1),
  event_name TEXT NOT NULL,
  occurred_at INTEGER,
  record_hash TEXT NOT NULL UNIQUE,
  diagnostic TEXT NOT NULL DEFAULT '',
  payload BLOB CHECK (payload IS NULL OR json_valid(payload, 8)),
  UNIQUE (turn_id, seq)
);
CREATE INDEX ix_events_name ON events (event_name);

CREATE TABLE llm_calls (
  id INTEGER PRIMARY KEY,
  turn_id INTEGER NOT NULL REFERENCES turns (id) ON DELETE CASCADE,
  source_event_id INTEGER NOT NULL UNIQUE REFERENCES events (id),
  called_at INTEGER,
  model TEXT,
  input_tokens INTEGER CHECK (input_tokens >= 0),
  output_tokens INTEGER CHECK (output_tokens >= 0),
  cache_read_tokens INTEGER CHECK (cache_read_tokens >= 0),
  cache_write_tokens INTEGER CHECK (cache_write_tokens >= 0),
  reasoning_tokens INTEGER CHECK (reasoning_tokens >= 0),
  cost_usd NUMERIC CHECK (cost_usd >= 0),
  duration_ms INTEGER CHECK (duration_ms >= 0),
  request_id TEXT
);
CREATE INDEX ix_llm_turn ON llm_calls (turn_id);

CREATE TABLE tool_calls (
  id INTEGER PRIMARY KEY,
  turn_id INTEGER NOT NULL REFERENCES turns (id) ON DELETE CASCADE,
  call_key TEXT NOT NULL UNIQUE,
  decision_event_id INTEGER UNIQUE REFERENCES events (id),
  result_event_id INTEGER UNIQUE REFERENCES events (id),
  tool_name TEXT,
  target TEXT,
  mcp_server TEXT,
  called_at INTEGER,
  duration_ms INTEGER CHECK (duration_ms >= 0),
  blocked_on_user_ms INTEGER CHECK (blocked_on_user_ms >= 0),
  success INTEGER CHECK (success IN (0, 1)),
  decision TEXT,
  decision_source TEXT,
  input_size_bytes INTEGER CHECK (input_size_bytes >= 0),
  result_size_bytes INTEGER CHECK (result_size_bytes >= 0),
  error_type TEXT,
  error_message TEXT,
  CHECK (decision_event_id IS NOT NULL OR result_event_id IS NOT NULL)
);
CREATE INDEX ix_tool_calls_turn ON tool_calls (turn_id);

CREATE TABLE file_changes (
  id INTEGER PRIMARY KEY,
  tool_call_id INTEGER NOT NULL REFERENCES tool_calls (id) ON DELETE CASCADE,
  file_path TEXT NOT NULL,
  operation TEXT NOT NULL CHECK (operation IN ('create', 'modify', 'delete', 'rename')),
  renamed_from TEXT,
  additions INTEGER CHECK (additions >= 0),
  deletions INTEGER CHECK (deletions >= 0),
  old_hash TEXT,
  new_hash TEXT,
  CHECK (operation <> 'rename' OR renamed_from IS NOT NULL)
);
CREATE INDEX ix_fc_tool ON file_changes (tool_call_id);

CREATE TABLE vendor_limit_snapshots (
  vendor TEXT PRIMARY KEY,
  state TEXT NOT NULL CHECK (state IN ('available', 'unavailable')),
  reason TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  plan TEXT NOT NULL DEFAULT '',
  windows_json TEXT NOT NULL DEFAULT '[]' CHECK (CASE WHEN json_valid(windows_json) THEN json_type(windows_json) = 'array' ELSE 0 END),
  extra_json TEXT NOT NULL DEFAULT '{}' CHECK (CASE WHEN json_valid(extra_json) THEN json_type(extra_json) = 'object' ELSE 0 END),
  observed_at TEXT NOT NULL DEFAULT '',
  checked_at INTEGER NOT NULL
) WITHOUT ROWID;
`
