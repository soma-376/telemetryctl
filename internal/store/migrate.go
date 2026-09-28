package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// 제품 배포 전에는 스키마 버전을 v1로 고정하고 최신 DDL 한 벌만 유지한다.
const schemaVersion = 1

func LatestSchemaVersion() int { return schemaVersion }

// migrate 는 빈 DB에 현재 스키마 전체를 한 트랜잭션으로 만든다.
// 다른 세대의 개발 DB는 자동 변환하거나 삭제하지 않고 재생성을 요구한다.
func migrate(ctx context.Context, db *sql.DB) error {
	var objects int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
WHERE type IN ('table', 'view', 'index', 'trigger') AND name NOT LIKE 'sqlite_%'`).Scan(&objects); err != nil {
		return fmt.Errorf("store: DB 구조 확인: %w", err)
	}
	if objects != 0 {
		valid, err := schemaShape(ctx, db)
		if err != nil {
			return err
		}
		if !valid {
			return errRecreateSchema()
		}
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: 스키마 생성 트랜잭션 시작: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // 커밋 뒤 ErrTxDone은 무시한다.
	if _, err := tx.ExecContext(ctx, createMetaTable); err != nil {
		return fmt.Errorf("store: meta 테이블 생성: %w", err)
	}
	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("store: schemaSQL 실행: %w", err)
	}
	if err := setMetaTx(ctx, tx, MetaSchemaVersion, strconv.Itoa(schemaVersion)); err != nil {
		return fmt.Errorf("store: 스키마 버전 기록: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: 스키마 커밋: %w", err)
	}
	return nil
}

func errRecreateSchema() error {
	return errors.New("store: 현재 v1 필수 구조가 없는 개발 DB — pulsemetry.db를 백업한 뒤 삭제해 재생성해야 한다")
}

// schemaShape은 같은 버전 번호를 가진 구형 개발 DB를 실제 필수 구조로 구분한다.
// PRAGMA 이름은 아래 정적 목록에서만 오므로 외부 입력이 SQL에 닿지 않는다.
func schemaShape(ctx context.Context, db *sql.DB) (bool, error) {
	var meta int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&meta); err != nil {
		return false, fmt.Errorf("store: meta 구조 확인: %w", err)
	}
	if meta == 0 {
		return false, nil
	}
	v, err := readSchemaVersion(ctx, db)
	if err != nil {
		return false, err
	}
	if v != schemaVersion {
		return false, nil
	}
	required := map[string][]string{
		"vendors":                  {"vendor", "status"},
		"sessions":                 {"id", "vendor_id", "session_key"},
		"turns":                    {"id", "session_id", "turn_key", "turn_index", "prompt_text", "prompt_message_id", "prompt_source_turn_id", "prompt_completeness", "prompt_completeness_evidence", "prompt_evidence_event_id", "prompt_conflict", "content_purged"},
		"events":                   {"id", "turn_id", "record_hash", "diagnostic", "payload"},
		"llm_calls":                {"id", "turn_id", "input_tokens", "output_tokens"},
		"tool_calls":               {"id", "turn_id"},
		"file_changes":             {"id", "tool_call_id"},
		"vendor_limit_snapshots":   {"vendor", "checked_at"},
		"codex_turn_provenance":    {"turn_id", "label", "processing_state", "link_state", "link_method", "linked_record_id", "classifier_version", "structure_evidence", "reason", "checked_at"},
		"codex_pending":            {"turn_id", "next_check_at", "attempts", "last_error"},
		"codex_jsonl_files":        {"id", "path", "generation", "os_file_id", "owner_session_key", "parent_session_key", "owner_source", "committed_offset", "committed_prefix_hash", "observed_size", "observed_mod_ns", "status", "observed_at"},
		"codex_jsonl_records":      {"id", "file_id", "start_offset", "end_offset", "record_type", "event_time", "message_id", "turn_id", "body", "body_hash", "completeness", "completeness_evidence", "structure_evidence", "representation_key"},
		"codex_jsonl_errors":       {"file_id", "start_offset", "end_offset", "error_kind", "diagnostic", "observed_at"},
		"codex_content_tombstones": {"owner_session_key", "before_at"},
		"codex_worker_state":       {"id", "epoch", "status", "heartbeat_at", "job_started_at", "restart_count", "reason"},
	}
	for table, columns := range required {
		rows, err := db.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
		if err != nil {
			return false, fmt.Errorf("store: %s 구조 확인: %w", table, err)
		}
		found := map[string]bool{}
		for rows.Next() {
			var cid, notNull, pk int
			var name, typ string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
				rows.Close()
				return false, fmt.Errorf("store: %s 컬럼 확인: %w", table, err)
			}
			found[name] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, fmt.Errorf("store: %s 컬럼 읽기: %w", table, err)
		}
		for _, column := range columns {
			if !found[column] {
				return false, nil
			}
		}
	}
	return true, nil
}

func readSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM meta WHERE "key" = ?`, MetaSchemaVersion).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("store: 스키마 버전 조회: %w", err)
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("store: 스키마 버전 값이 정수가 아님 (%q)", raw)
	}
	return v, nil
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func setMetaTx(ctx context.Context, e execer, key, value string) error {
	_, err := e.ExecContext(ctx, `INSERT INTO meta ("key", value) VALUES (?, ?)
ON CONFLICT("key") DO UPDATE SET value = excluded.value`, key, value)
	return err
}
