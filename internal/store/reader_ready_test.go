package store

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
)

// 데몬이 DB 를 만드는 **도중** 에 GUI 가 붙는 창을 고정한다 (PROJ-97).
//
// Open 은 연결을 열어 파일을 만든 뒤 마이그레이션을 실행한다. 그 사이에는 파일이 존재하는데
// 테이블이 없고, 그 순간에 붙은 조회 핸들은 모든 질의가 `no such table` 로 실패한다.
// 더 나쁘게는 dashboard.Reader 가 그 핸들을 "붙었다" 로 보고 다시 붙지 않아, GUI 가 앱을
// 껐다 켤 때까지 회복하지 못한다.
//
// 그래서 OpenReadOnlyIfPresent 는 스키마가 준비되지 않은 파일을 **파일이 없는 것과 같이**
// 다룬다. 호출자의 재시도가 그대로 답이 되는 형태여야 한다 (ADR 0004).

// openWriteRaw 는 마이그레이션 없이 쓰기 연결만 연다. 준비되지 않은 DB 를 만드는 수단이다.
func openWriteRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open(DriverName, writeDSN(path, DefaultBusyTimeout))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck // 테스트 정리
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	return db
}

func TestOpenReadOnlyIfPresentTreatsUnmigratedDatabaseAsAbsent(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		// prepare 는 DB 파일을 원하는 상태로 만든다.
		prepare func(t *testing.T, path string)
	}{
		{
			name: "빈 파일",
			// SQLite 는 길이 0 인 파일을 정상적인 빈 DB 로 연다. 테이블은 하나도 없다.
			prepare: func(t *testing.T, path string) {
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "meta 만 만들어진 상태",
			// migrate 가 createMetaTable 을 실행한 직후, 첫 마이그레이션 전이다.
			prepare: func(t *testing.T, path string) {
				db := openWriteRaw(t, path)
				if _, err := db.ExecContext(ctx, createMetaTable); err != nil {
					t.Fatalf("createMetaTable: %v", err)
				}
			},
		},
		{
			name: "v3 이전에서 멈춘 상태",
			// 마이그레이션 도중 크래시. 버전은 올랐지만 지금의 조회가 읽을 테이블이 없다.
			prepare: func(t *testing.T, path string) {
				db := openWriteRaw(t, path)
				if _, err := db.ExecContext(ctx, createMetaTable); err != nil {
					t.Fatalf("createMetaTable: %v", err)
				}
				if err := setMetaTx(ctx, db, MetaSchemaVersion, strconv.Itoa(minReadableSchemaVersion-1)); err != nil {
					t.Fatalf("버전 기록: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := PathIn(dir)
			tc.prepare(t, path)

			ro, err := OpenReadOnlyIfPresent(path)
			if err != nil {
				t.Fatalf("OpenReadOnlyIfPresent = %v — 준비 중인 DB 는 실패가 아니다", err)
			}
			if ro != nil {
				ro.Close() //nolint:errcheck // 테스트 정리
				t.Fatal("핸들을 돌려줬다 — 조회가 전부 no such table 로 실패하고 다시 붙지도 못한다")
			}
		})
	}
}

// 마이그레이션이 끝난 뒤에는 당연히 붙어야 한다. 위 테스트만 있으면 "항상 nil" 로도 통과한다.
func TestOpenReadOnlyIfPresentAttachesOnceMigrated(t *testing.T) {
	dir := t.TempDir()
	path := PathIn(dir)

	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close() //nolint:errcheck // 테스트 정리

	ro, err := OpenReadOnlyIfPresent(path)
	if err != nil {
		t.Fatalf("OpenReadOnlyIfPresent: %v", err)
	}
	if ro == nil {
		t.Fatal("마이그레이션이 끝났는데 붙지 못했다")
	}
	defer ro.Close() //nolint:errcheck // 테스트 정리

	v, err := ro.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != LatestSchemaVersion() {
		t.Errorf("스키마 버전 = %d, want %d", v, LatestSchemaVersion())
	}
}
