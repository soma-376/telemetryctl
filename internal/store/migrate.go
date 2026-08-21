package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// migration 은 스키마 버전을 하나 올리는 단위다.
//
// # 다음 마이그레이션을 추가하는 방법
//
//  1. migrations 슬라이스 **끝에** 항목을 하나 추가한다. version 은 직전 +1 이다.
//  2. stmts 에 ALTER/CREATE 문을 순서대로 적는다. 문장 하나가 하나의 문자열이다.
//  3. 끝. 러너가 meta.local_schema_version 을 보고 아직 적용되지 않은 것만 실행한다.
//
// 이미 배포된 버전의 stmts 는 절대 고치지 않는다. 고치면 기존 설치(이미 그 버전을 지나감)와
// 새 설치(고친 문장을 실행)의 스키마가 갈리고, 그 차이는 어느 쪽에서도 드러나지 않는다.
// 잘못된 마이그레이션은 다음 번호의 새 마이그레이션으로 바로잡는다.
//
// 예 — 후속 티켓이 분류 결과에 컬럼을 더할 때:
//
//	{version: 3, name: "턴 분류 근거", stmts: []string{
//		`ALTER TABLE turns ADD COLUMN work_type_reason TEXT`,
//	}},
type migration struct {
	version int
	name    string
	stmts   []string
}

// migrations 는 버전 오름차순이다.
var migrations = []migration{
	{version: 1, name: "초기 스키마", stmts: schemaV1},
	{version: 2, name: "턴과 세션 단계", stmts: schemaV2},
}

// LatestSchemaVersion 은 이 바이너리가 아는 최신 스키마 버전이다.
func LatestSchemaVersion() int { return migrations[len(migrations)-1].version }

// migrate 는 아직 적용되지 않은 마이그레이션을 순서대로 실행한다.
// 마이그레이션 하나가 트랜잭션 하나다 — 중간에 실패하면 그 버전은 통째로 롤백되고
// local_schema_version 도 오르지 않아 다음 기동에서 같은 지점부터 다시 시도한다.
func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, createMetaTable); err != nil {
		return fmt.Errorf("store: meta 테이블 생성: %w", err)
	}

	current, err := readSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if latest := LatestSchemaVersion(); current > latest {
		// 더 새로운 telemetryctl 이 만든 DB 다. 여기서 쓰기 시작하면 그쪽이 아는 컬럼을
		// 우리가 모른 채 덮어쓴다.
		return fmt.Errorf("store: DB 스키마 버전 %d 가 이 바이너리가 아는 %d 보다 높음 — telemetryctl 을 갱신해라", current, latest)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: 마이그레이션 %d(%s) 트랜잭션 시작: %w", m.version, m.name, err)
	}
	defer tx.Rollback() //nolint:errcheck // 커밋 후에는 ErrTxDone 이라 무시한다

	for i, stmt := range m.stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: 마이그레이션 %d(%s) 문장 %d: %w", m.version, m.name, i+1, err)
		}
	}
	if err := setMetaTx(ctx, tx, MetaSchemaVersion, strconv.Itoa(m.version)); err != nil {
		return fmt.Errorf("store: 마이그레이션 %d(%s) 버전 기록: %w", m.version, m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: 마이그레이션 %d(%s) 커밋: %w", m.version, m.name, err)
	}
	return nil
}

// readSchemaVersion 은 meta 의 버전을 읽는다. 값이 없으면 0 — 아직 아무 마이그레이션도
// 적용되지 않은 새 DB 다.
func readSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM meta WHERE "key" = ?`, MetaSchemaVersion).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("store: 스키마 버전 조회: %w", err)
	}
	v, convErr := strconv.Atoi(raw)
	if convErr != nil {
		return 0, fmt.Errorf("store: 스키마 버전 값이 정수가 아님 (%q)", raw)
	}
	return v, nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func setMetaTx(ctx context.Context, e execer, key, value string) error {
	_, err := e.ExecContext(ctx,
		`INSERT INTO meta ("key", value) VALUES (?, ?)
		 ON CONFLICT("key") DO UPDATE SET value = excluded.value`, key, value)
	return err
}
