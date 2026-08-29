package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
	"github.com/your-org/pulsemetry/internal/session"
	"github.com/your-org/pulsemetry/internal/store"
)

func TestStatus(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.write(testBatch{
		Events: []store.EventRecord{
			prompt("s1", testNow.Add(-2*time.Hour), 1, "인증 토큰 검증"),
			prompt("s1", testNow.Add(-time.Hour), 2, "두 번째 프롬프트"),
		},
		Sessions: []session.Session{
			newSession("s1", testNow.Add(-2*time.Hour), func(s *session.Session) {
				s.Status = session.StatusRunning
				s.EndedAt = event.Opt[event.UnixSec]{}
				s.Files = []session.File{{PathHash: "h", Name: "a.go", Edits: 1, LastTS: event.SecFromTime(testNow)}}
				s.MCP = []session.MCPUsage{{ServerName: "github", Connected: true}}
				s.Tools = []session.ToolEvent{{TS: event.SecFromTime(testNow), ToolName: "Read"}}
			}),
		},
		Rollups: []testRollupRow{
			rollupRow(testNow, testDimTotal, "", testRollupBucket{CostUSD: 1}),
		},
	})
	if err := f.db.SetMeta(ctx, store.MetaRetentionDays, "45"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	lastRollup := testNow.Add(-5 * time.Minute).Unix()
	if err := f.db.SetMeta(ctx, store.MetaLastRollupAt, strconv.FormatInt(lastRollup, 10)); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	st, err := f.reader.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Available {
		t.Fatal("Available = false")
	}
	if st.Counts.Events != 2 || st.Counts.EventContent != 2 {
		t.Errorf("Counts = %+v, want events=2 content=2", st.Counts)
	}
	if st.Counts.Sessions != 1 || st.Counts.SessionFiles != 1 || st.Counts.ToolEvents != 1 || st.Counts.MCPSessionUsage != 1 {
		t.Errorf("Counts = %+v", st.Counts)
	}
	if st.Counts.RollupHourly != 1 || st.Counts.Vendors != 1 {
		t.Errorf("Counts = %+v", st.Counts)
	}
	if st.RunningSessions != 1 || len(st.ActiveVendors) != 1 {
		t.Errorf("running = %d, vendors = %v", st.RunningSessions, st.ActiveVendors)
	}
	if st.SchemaVersion != store.LatestSchemaVersion() || st.LatestSchemaVersion != store.LatestSchemaVersion() {
		t.Errorf("스키마 버전 = %d/%d, want %d", st.SchemaVersion, st.LatestSchemaVersion, store.LatestSchemaVersion())
	}
	if st.RetentionDays != 45 {
		t.Errorf("RetentionDays = %d, want 45", st.RetentionDays)
	}
	if st.LastRollupAt != lastRollup {
		t.Errorf("LastRollupAt = %d, want %d", st.LastRollupAt, lastRollup)
	}
	if st.DatabaseBytes <= 0 {
		t.Errorf("DatabaseBytes = %d, want > 0", st.DatabaseBytes)
	}
	// events.ts 는 나노초지만 밖으로는 초로 나가야 한다.
	wantOldest := testNow.Add(-2 * time.Hour).Unix()
	if st.OldestEventAt != wantOldest {
		t.Errorf("OldestEventAt = %d, want %d (초 단위)", st.OldestEventAt, wantOldest)
	}
	if st.NewestEventAt != testNow.Add(-time.Hour).Unix() {
		t.Errorf("NewestEventAt = %d", st.NewestEventAt)
	}
	if st.GeneratedAt != testNow.Unix() {
		t.Errorf("GeneratedAt = %d, want %d", st.GeneratedAt, testNow.Unix())
	}
	if st.Daemon.Found {
		t.Error("runtime.json 이 없는데 Daemon.Found = true")
	}
}

func TestStatusReadsDaemonRuntimeInfo(t *testing.T) {
	f := newFixture(t)
	info := runtimeinfo.Info{
		PID:          os.Getpid(),
		Endpoint:     "http://localhost:4318",
		ListenPort:   4318,
		ListenAddrs:  []string{"127.0.0.1:4318", "[::1]:4318"},
		DataDir:      f.dir,
		DatabasePath: f.path,
		Version:      "test",
	}
	if err := runtimeinfo.Write(runtimeinfo.PathIn(f.dir), info); err != nil {
		t.Fatalf("runtimeinfo.Write: %v", err)
	}

	st, err := f.reader.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Daemon.Found || !st.Daemon.Running || st.Daemon.Stale {
		t.Fatalf("Daemon = %+v, want found+running (현재 프로세스의 pid 다)", st.Daemon)
	}
	if st.Daemon.Endpoint != "http://localhost:4318" || st.Daemon.ListenPort != 4318 {
		t.Errorf("Daemon = %+v", st.Daemon)
	}
	if len(st.Daemon.ListenAddrs) != 2 {
		t.Errorf("ListenAddrs = %v", st.Daemon.ListenAddrs)
	}
}

// Status 는 스크린샷으로 유출돼도 무해해야 한다. 토큰·자격증명이 담길 자리가 없다는 것을
// 직렬화 결과로 확인한다.
//
// 테스트 이름에 금지어를 넣지 않는다 — t.TempDir 가 테스트 이름을 경로에 넣고 그 경로가
// database_path 로 JSON 에 들어가서 자기 이름 때문에 실패한다.
func TestStatusPayloadIsSafeToShare(t *testing.T) {
	f := newFixture(t)
	st, err := f.reader.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	lower := strings.ToLower(string(b))
	for _, banned := range []string{"token", "secret", "password", "credential", "bearer", "authorization", "installation_id"} {
		if strings.Contains(lower, banned) {
			t.Errorf("Status JSON 에 %q 가 들어 있다: %s", banned, b)
		}
	}
}

// 에러 메시지는 Promise reject 로 사용자 화면에 뜬다. 내부 SQL 이 그대로 노출되면 안 된다.
func TestErrorsDoNotLeakSQL(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.reader.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close 는 핸들을 떼어내므로 이후 조회는 "미설치" 로 취급된다. 진짜 실패 경로를 만들려면
	// 닫힌 핸들을 그대로 들고 있어야 한다.
	ro, err := store.OpenReadOnly(f.path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	if err := ro.Close(); err != nil {
		t.Fatalf("ReadOnly.Close: %v", err)
	}
	f.reader.ro = ro

	var msgs []string
	if _, err := f.reader.Sessions(ctx, SessionQuery{}); err != nil {
		msgs = append(msgs, err.Error())
	}
	if _, err := f.reader.Today(ctx, seoul); err != nil {
		msgs = append(msgs, err.Error())
	}
	if _, err := f.reader.Search(ctx, SearchQuery{Text: "토큰"}); err != nil {
		msgs = append(msgs, err.Error())
	}
	if _, err := f.reader.Vendors(ctx); err != nil {
		msgs = append(msgs, err.Error())
	}
	if _, err := f.reader.Status(ctx); err != nil {
		msgs = append(msgs, err.Error())
	}
	if len(msgs) == 0 {
		t.Fatal("닫힌 핸들로 조회했는데 에러가 하나도 없다 — 테스트가 무의미하다")
	}
	for _, m := range msgs {
		upper := strings.ToUpper(m)
		for _, banned := range []string{"SELECT ", "FROM ", "JOIN ", "WHERE ", "COALESCE"} {
			if strings.Contains(upper, banned) {
				t.Errorf("에러 메시지에 SQL 이 들어 있다: %q", m)
			}
		}
		if !strings.HasPrefix(m, "dashboard: ") {
			t.Errorf("에러 메시지에 출처 표시가 없다: %q", m)
		}
	}
	f.reader.ro = nil
}
