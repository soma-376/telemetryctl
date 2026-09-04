package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// 제품 배포 전에는 과거 개발 DB와의 호환성을 제공하지 않고 최신 DDL 한 벌만 유지한다.
//
// **배포 전까지 이 값은 1로 고정한다.** 스키마를 고쳐도 올리지 않는다 — 마이그레이션을
// 쓰지 않기로 한 이상 번호를 올려도 하는 일이 같고(옛 DB 거부), 번호만 늘어나면 나중에
// 진짜 마이그레이션을 시작할 때 기준점이 흐려진다. 스키마가 바뀌면 개발 DB를 지우고
// 다시 만든다.
const schemaVersion = 1

func LatestSchemaVersion() int { return schemaVersion }

// migrate 는 빈 DB에 현재 스키마 전체를 한 트랜잭션으로 만든다.
// 다른 세대의 개발 DB는 자동 변환하거나 삭제하지 않고 재생성을 요구한다.
func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, createMetaTable); err != nil {
		return fmt.Errorf("store: meta 테이블 생성: %w", err)
	}
	current, err := readSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if current == schemaVersion {
		return nil
	}
	if current != 0 {
		return fmt.Errorf("store: 지원하지 않는 개발 DB 스키마 v%d — pulsemetry.db를 삭제한 뒤 다시 실행해라", current)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: 스키마 생성 트랜잭션 시작: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // 커밋 뒤 ErrTxDone은 무시한다.
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
