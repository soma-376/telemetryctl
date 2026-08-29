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

	f.write(store.Batch{
		Sessions: []session.Session{newSession("s1", testNow.Add(-2*time.Hour), running)},
		Events: []store.EventRecord{
			promptRecord("s1", "turn-1", testNow.Add(-2*time.Hour), 1, "인증 토큰 검증"),
			llmRecord("s1", "turn-1", testNow.Add(-90*time.Minute), 2, llmSpec{Cost: 1}),
			toolRecord("s1", "turn-1", "call-1", testNow.Add(-time.Hour), 3, toolSpec{
				ToolName: "Edit",
				Success:  event.Some(true),
				Target:   workspaceA + "/a.go",
				File:     fileChange(workspaceA+"/a.go", 3, 0),
			}),
		},
	})
	if err := f.db.SetMeta(ctx, store.MetaRetentionDays, "45"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	lastFlush := testNow.Add(-5 * time.Minute).Unix()
	if err := f.db.SetMeta(ctx, store.MetaLastRollupAt, strconv.FormatInt(lastFlush, 10)); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	st, err := f.reader.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Available {
		t.Fatal("Available = false")
	}

	// v3 의 도메인 테이블 여섯을 센다. v1 의 event_content·tool_events·session_files·
	// mcp_session_usage·rollup_hourly 는 테이블 자체가 없다.
	c := st.Counts
	switch {
	case c.Events != 3:
		t.Errorf("events = %d, want 3", c.Events)
	case c.Turns != 1:
		t.Errorf("turns = %d, want 1", c.Turns)
	case c.Sessions != 1:
		t.Errorf("sessions = %d, want 1", c.Sessions)
	case c.LLMCalls != 1:
		t.Errorf("llm_calls = %d, want 1", c.LLMCalls)
	case c.ToolCalls != 1:
		t.Errorf("tool_calls = %d, want 1", c.ToolCalls)
	case c.FileChanges != 1:
		t.Errorf("file_changes = %d, want 1", c.FileChanges)
	case c.Vendors != 1:
		t.Errorf("vendors = %d, want 1", c.Vendors)
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
	if st.LastFlushAt != lastFlush {
		t.Errorf("LastFlushAt = %d, want %d", st.LastFlushAt, lastFlush)
	}
	if st.DatabaseBytes <= 0 {
		t.Errorf("DatabaseBytes = %d, want > 0", st.DatabaseBytes)
	}
	// v3 의 events.occurred_at 은 초다. 나노초 변환이 남아 있으면 여기서 어긋난다.
	if want := testNow.Add(-2 * time.Hour).Unix(); st.OldestEventAt != want {
		t.Errorf("OldestEventAt = %d, want %d (초 단위)", st.OldestEventAt, want)
	}
	if want := testNow.Add(-time.Hour).Unix(); st.NewestEventAt != want {
		t.Errorf("NewestEventAt = %d, want %d", st.NewestEventAt, want)
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

// 공개 응답 타입의 json 태그는 전부 snake_case 여야 한다 (ADR 0004). 태그가 곧 TS 필드명이라
// 하나라도 어긋나면 프런트엔드가 조용히 undefined 를 읽는다.
func TestPublicTypesUseSnakeCaseTags(t *testing.T) {
	values := []any{
		TodaySummary{}, Card{}, Totals{}, Row{}, BreakdownQuery{},
		SessionQuery{}, SessionRow{}, SessionDetail{}, FileRow{}, ToolRow{}, SessionMCPRow{},
		SearchQuery{}, Hit{}, VendorStatus{}, MCPRow{}, Status{}, Counts{}, DaemonStatus{},
	}
	for _, v := range values {
		assertSnakeCaseTags(t, v)
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
	for _, tc := range absentCases() {
		if _, err := tc.call(ctx, f.reader); err != nil {
			msgs = append(msgs, err.Error())
		}
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
