package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"

	"github.com/your-org/pulsemetry/internal/runtimeinfo"
	"github.com/your-org/pulsemetry/internal/store"
)

// Status 는 `telemetryctl status` 의 로컬 파이프라인 블록이자 GUI Settings 화면의 근거다.
//
// # 비밀은 담지 않는다
//
// 여기 들어가는 것은 경로·행 수·크기·설정값·데몬 좌표뿐이다. loopback ingest 토큰은 키링에
// 있고 runtime.json 에도 없다 (runtimeinfo 패키지 주석). installation_id 도 담지 않는다 —
// 화면이 그것으로 할 일이 없고, 없는 편이 이 구조체가 복사돼 로그에 찍혔을 때 안전하다.
// 필드를 추가할 때는 "이 값이 스크린샷으로 유출돼도 무해한가" 를 먼저 물어야 한다.
type Status struct {
	// Available 이 false 면 DB 파일이 아직 없다 — 미설치이거나 데몬이 한 번도 안 돈 상태다.
	// 에러가 아니다.
	Available    bool   `json:"available"`
	DatabasePath string `json:"database_path"`
	DataDir      string `json:"data_dir"`
	// DatabaseBytes 는 DB 본체와 WAL·SHM 을 합친 크기다. WAL 을 빼면 방금 들어온 수백 MB 가
	// 보이지 않아 "디스크 사용량" 표시가 실제와 어긋난다.
	DatabaseBytes int64 `json:"database_bytes"`

	SchemaVersion int `json:"schema_version"`
	// LatestSchemaVersion 은 이 바이너리가 아는 최신 버전이다. SchemaVersion 보다 크면
	// 데몬이 아직 마이그레이션하지 않았다는 뜻이고, 작으면 GUI 쪽이 낡았다는 뜻이다.
	LatestSchemaVersion int `json:"latest_schema_version"`

	// RetentionDays 는 meta.retention_days 의 마지막 적용값이다 (Settings 「데이터 보존 기간」).
	// 0 이면 데몬이 아직 기록하지 않았다.
	RetentionDays int `json:"retention_days"`
	// LastRollupAt 은 마지막 롤업 플러시 시각(UTC unix 초)이다. 0 이면 아직 없음.
	LastRollupAt int64 `json:"last_rollup_at"`

	OldestEventAt int64 `json:"oldest_event_at"`
	NewestEventAt int64 `json:"newest_event_at"`

	Counts Counts `json:"counts"`

	RunningSessions int64    `json:"running_sessions"`
	ActiveVendors   []string `json:"active_vendors"`

	Daemon DaemonStatus `json:"daemon"`

	// GeneratedAt 은 이 응답을 만든 시각이다. 화면이 "몇 분 전 기준" 을 붙일 수 있어야 한다.
	GeneratedAt int64 `json:"generated_at"`
}

// Counts 는 테이블별 행 수다. 보존 정책이 실제로 도는지, 원문 저장이 켜져 있는지를
// 사람이 눈으로 확인하는 유일한 수단이다.
type Counts struct {
	Events          int64 `json:"events"`
	EventContent    int64 `json:"event_content"`
	ToolEvents      int64 `json:"tool_events"`
	Sessions        int64 `json:"sessions"`
	SessionFiles    int64 `json:"session_files"`
	MCPSessionUsage int64 `json:"mcp_session_usage"`
	RollupHourly    int64 `json:"rollup_hourly"`
	Vendors         int64 `json:"vendors"`
}

// DaemonStatus 는 runtime.json 에서 읽은 데몬 좌표다 (비밀 없음).
type DaemonStatus struct {
	// Found 는 runtime.json 이 있다는 뜻이고, Running 은 그 pid 가 살아 있다는 뜻이다.
	// pid 는 재사용되므로 Stale=true 만 확정이고 Running=true 는 "아마" 다
	// (runtimeinfo.Load 주석). 확정이 필요하면 Endpoint 로 GET /healthz 를 던진다.
	Found   bool `json:"found"`
	Running bool `json:"running"`
	Stale   bool `json:"stale"`

	PID         int      `json:"pid"`
	StartedAt   string   `json:"started_at"`
	Endpoint    string   `json:"endpoint"`
	ListenPort  int      `json:"listen_port"`
	ListenAddrs []string `json:"listen_addrs"`
	Version     string   `json:"version"`
}

// countsSQL 은 한 번의 왕복으로 모든 행 수를 센다. 테이블마다 질의를 나누면 그 사이에 데몬이
// 쓰기를 커밋해 서로 어긋난 숫자가 한 화면에 뜬다.
const countsSQL = `SELECT
  (SELECT COUNT(*) FROM events),
  (SELECT COUNT(*) FROM event_content),
  (SELECT COUNT(*) FROM tool_events),
  (SELECT COUNT(*) FROM sessions),
  (SELECT COUNT(*) FROM session_files),
  (SELECT COUNT(*) FROM mcp_session_usage),
  (SELECT COUNT(*) FROM rollup_hourly),
  (SELECT COUNT(*) FROM vendors),
  (SELECT COUNT(*) FROM sessions WHERE status = 'running'),
  (SELECT COALESCE(MIN(ts),0) FROM events),
  (SELECT COALESCE(MAX(ts),0) FROM events)`

// Status 는 로컬 파이프라인의 현재 상태다.
//
// DB 가 없어도 에러가 아니다 — 그때도 데몬 좌표(runtime.json)는 읽어서 돌려준다.
// 데몬은 떠 있는데 DB 가 아직 없는 순간이 실제로 존재하고, 그 상태를 구분해 보여야
// 사용자가 무엇을 기다리는지 안다.
func (r *Reader) Status(ctx context.Context) (Status, error) {
	st := Status{
		DatabasePath:        r.path,
		DataDir:             r.dataDir,
		LatestSchemaVersion: store.LatestSchemaVersion(),
		ActiveVendors:       []string{},
		GeneratedAt:         r.now().Unix(),
		Daemon:              daemonStatus(r.dataDir),
	}
	st.DatabaseBytes = databaseBytes(r.path)

	db, ok := r.db()
	if !ok {
		return st, nil
	}
	st.Available = true

	// events.ts 는 나노초다. 밖으로 나가는 시각은 전부 초라 여기서 내린다.
	var oldestNano, newestNano int64
	c := &st.Counts
	if err := db.QueryRowContext(ctx, countsSQL).Scan(
		&c.Events, &c.EventContent, &c.ToolEvents, &c.Sessions, &c.SessionFiles,
		&c.MCPSessionUsage, &c.RollupHourly, &c.Vendors,
		&st.RunningSessions, &oldestNano, &newestNano,
	); err != nil {
		return Status{}, queryErr("로컬 상태 조회", err)
	}
	st.OldestEventAt = nanoToSec(oldestNano)
	st.NewestEventAt = nanoToSec(newestNano)

	version, err := r.schemaVersion(ctx)
	if err != nil {
		return Status{}, err
	}
	st.SchemaVersion = version

	if st.RetentionDays, err = r.metaInt(ctx, store.MetaRetentionDays); err != nil {
		return Status{}, err
	}
	lastRollup, err := r.metaInt(ctx, store.MetaLastRollupAt)
	if err != nil {
		return Status{}, err
	}
	st.LastRollupAt = int64(lastRollup)

	vendors, _, err := activeAgents(ctx, db)
	if err != nil {
		return Status{}, err
	}
	st.ActiveVendors = vendors
	return st, nil
}

func nanoToSec(n int64) int64 {
	if n == 0 {
		return 0
	}
	return n / 1e9
}

func (r *Reader) schemaVersion(ctx context.Context) (int, error) {
	r.mu.RLock()
	ro := r.ro
	r.mu.RUnlock()
	if ro == nil {
		return 0, nil
	}
	v, err := ro.SchemaVersion(ctx)
	if err != nil {
		return 0, queryErr("스키마 버전 조회", err)
	}
	return v, nil
}

// metaInt 는 meta 값을 정수로 읽는다. 값이 없거나 정수가 아니면 0 이다 — 상태 표시 하나
// 때문에 조회 전체를 실패시킬 이유가 없다.
func (r *Reader) metaInt(ctx context.Context, key string) (int, error) {
	r.mu.RLock()
	ro := r.ro
	r.mu.RUnlock()
	if ro == nil {
		return 0, nil
	}
	raw, ok, err := ro.Meta(ctx, key)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, queryErr("설정값 조회", err)
	case !ok:
		return 0, nil
	}
	v, convErr := strconv.Atoi(raw)
	if convErr != nil {
		return 0, nil
	}
	return v, nil
}

// databaseBytes 는 DB 파일과 WAL·SHM 의 합이다. 파일이 없으면 0 이다.
func databaseBytes(path string) int64 {
	var total int64
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if fi, err := os.Stat(p); err == nil {
			total += fi.Size()
		}
	}
	return total
}

// daemonStatus 는 runtime.json 을 읽는다. 읽기 실패는 에러로 올리지 않는다 — 데몬 상태를
// 못 읽는 것과 데이터를 못 읽는 것은 심각도가 다르고, 여기서 에러를 내면 DB 는 멀쩡한데도
// Settings 화면 전체가 뜨지 않는다.
func daemonStatus(dataDir string) DaemonStatus {
	st, err := runtimeinfo.Load(runtimeinfo.PathIn(dataDir))
	if err != nil || !st.Found {
		return DaemonStatus{ListenAddrs: []string{}}
	}
	addrs := st.Info.ListenAddrs
	if addrs == nil {
		addrs = []string{}
	}
	return DaemonStatus{
		Found:       true,
		Running:     !st.Stale,
		Stale:       st.Stale,
		PID:         st.Info.PID,
		StartedAt:   st.Info.StartedAt,
		Endpoint:    st.Info.Endpoint,
		ListenPort:  st.Info.ListenPort,
		ListenAddrs: addrs,
		Version:     st.Info.Version,
	}
}
