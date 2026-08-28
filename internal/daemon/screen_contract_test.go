package daemon

// 화면 계약 통합 테스트 (PROJ-97).
//
// 앞선 티켓들의 테스트는 각자 자기 조회 하나를 store.Batch 로 직접 씨앗을 심어 검증했다.
// 그 방식은 "이 질의가 자기 입력에 대해 맞는가" 를 고정하지만 **디코드·조립·승격을 거쳐
// 실제로 저장된 행** 을 읽는지는 확인하지 않는다. 여기서는 반대로 간다.
//
//	OTLP 픽스처 → 수신기 → 디코드 → 조립 → SQLite → internal/dashboard 의 모든 화면
//
// 한 벌의 데이터를 넣고 Home · Activity · Session Detail · SessionMetrics · FileChanges ·
// Classifier · Tray · Search · Status 를 전부 그 위에서 단언한다. 실패하면 **테스트 이름이
// 곧 깨진 화면 계약** 이다 (TestScreenContract_<화면>_...).
//
// # 네트워크와 자격증명
//
// 벤더 한도 조회(internal/vendorlimit)는 사용자의 홈에서 자격증명 파일을 찾는다. 이 파일의
// 테스트는 홈을 빈 임시 디렉터리로 바꾼다 — 자격증명이 없으면 어댑터는 요청을 만들기 전에
// unavailable 로 끝나므로 **실제 벤더 엔드포인트로 나가는 요청이 한 건도 없다.**
// 상위 Collector 도 httptest 대역이다 (helper_test.go 의 upstream).

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/store"
	"github.com/your-org/pulsemetry/internal/vendorlimit"
)

// screenFixture 는 데몬을 한 번 돌려 픽스처를 저장하고, 그 DB 를 조회 계층으로 연 상태다.
type screenFixture struct {
	t   *testing.T
	h   *harness
	db  *sql.DB
	svc *dashboard.Service

	// sessionID 는 저장된 유일한 세션의 sessions.id 다. 화면들이 세션을 가리키는 키다.
	sessionID int64
	// date 는 세션이 시작한 날(UTC)이다. Home·HomeBreakdown 의 인자다 — 벽시계로
	// "오늘" 을 물으면 픽스처가 과거라 빈 화면이 나온다.
	date string
	// startedAt 은 sessions.started_at 이다.
	startedAt int64
}

const screenTZ = "UTC"

// newScreenFixture 는 walkthrough 픽스처 한 벌을 데몬으로 흘려 넣고 조회 서비스를 연다.
func newScreenFixture(t *testing.T) *screenFixture {
	t.Helper()

	// 자격증명 탐색이 진짜 홈으로 가지 않게 막는다. os.UserHomeDir 이 보는 변수다.
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("USERPROFILE", empty)

	h := start(t, harnessOptions{})

	// 메트릭을 먼저 넣는다 — lines_of_code 포인트는 첫 프롬프트보다 앞선 이벤트라
	// 가상 턴으로 가야 한다 (daemon_test.go 의 같은 주석).
	if resp := h.postFixture("metrics_lines_of_code.json"); resp.StatusCode != http.StatusOK {
		t.Fatalf("메트릭 응답 = %d, want 200", resp.StatusCode)
	}
	h.waitDecoded(1)
	if resp := h.postFixture("logs_session_walkthrough.json"); resp.StatusCode != http.StatusOK {
		t.Fatalf("로그 응답 = %d, want 200", resp.StatusCode)
	}
	h.waitDecoded(2)
	h.stop()

	f := &screenFixture{t: t, h: h, db: h.openDB()}

	if err := f.db.QueryRow(`SELECT id, started_at FROM sessions`).
		Scan(&f.sessionID, &f.startedAt); err != nil {
		t.Fatalf("세션 조회: %v\n%s", err, h.logs.String())
	}
	f.date = dashboardDateOf(t, f.startedAt)

	f.svc = dashboard.NewService(store.PathIn(h.dataDir))
	if err := f.svc.Start(); err != nil {
		t.Fatalf("dashboard.Service.Start: %v", err)
	}
	t.Cleanup(func() {
		if err := f.svc.Stop(); err != nil {
			t.Errorf("dashboard.Service.Stop: %v", err)
		}
	})
	if !f.svc.Available() {
		t.Fatalf("데몬이 쓴 DB 를 조회 계층이 열지 못했다 (%s)", store.PathIn(h.dataDir))
	}
	return f
}

// dashboardDateOf 는 UTC unix 초를 Home 이 받는 YYYY-MM-DD 로 옮긴다.
func dashboardDateOf(t *testing.T, sec int64) string {
	t.Helper()
	return time.Unix(sec, 0).UTC().Format("2006-01-02")
}

func (f *screenFixture) count(query string, args ...any) int {
	f.t.Helper()
	return countRows(f.t, f.db, query, args...)
}

// ── 화면별 계약 ─────────────────────────────────────────────────────────────

// TestScreenContract_Home_ShowsTheStoredDay 는 Home 카드가 저장된 승격 행을 그대로
// 반영하는지 본다. 카드가 0 이면 디코드~승격 사이 어딘가가 끊긴 것이다.
func TestScreenContract_Home_ShowsTheStoredDay(t *testing.T) {
	f := newScreenFixture(t)
	ctx := context.Background()

	home, err := f.svc.Home(ctx, dashboard.HomeQuery{TZ: screenTZ, Date: f.date})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if home.TZ != screenTZ || home.Date != f.date {
		t.Fatalf("Home 이 다른 날을 봤다: tz=%q date=%q", home.TZ, home.Date)
	}

	wantCalls := int64(f.count(`SELECT COUNT(*) FROM llm_calls`))
	if home.Totals.APIRequests != wantCalls {
		t.Errorf("Home.Totals.APIRequests = %d, want %d (llm_calls 행 수)",
			home.Totals.APIRequests, wantCalls)
	}
	if home.Totals.Tokens() <= 0 {
		t.Errorf("Home 토큰이 0 이다 — llm_calls 의 토큰이 화면에 닿지 않았다: %+v", home.Totals)
	}
	wantTools := int64(f.count(`SELECT COUNT(*) FROM tool_calls`))
	if home.Totals.ToolCalls != wantTools {
		t.Errorf("Home.Totals.ToolCalls = %d, want %d", home.Totals.ToolCalls, wantTools)
	}

	// 카드 네 장은 DB 유무와 무관하게 늘 있어야 한다 (absent_test.go 와 같은 계약).
	if len(home.Cards) != 4 {
		t.Errorf("카드 = %d장, want 4", len(home.Cards))
	}
	if len(home.Recent) != 1 {
		t.Fatalf("최근 세션 = %d건, want 1", len(home.Recent))
	}
	if home.Recent[0].ID != f.sessionID {
		t.Errorf("최근 세션 id = %d, want %d", home.Recent[0].ID, f.sessionID)
	}
	if home.Recent[0].WorkspacePath != fixturePath {
		t.Errorf("최근 세션 workspace_path = %q, want %q", home.Recent[0].WorkspacePath, fixturePath)
	}
	// 픽스처의 세션은 유휴 임계값 안이라 아직 진행 중이다 (ended_at IS NULL).
	if home.Recent[0].Status != dashboard.StatusRunning {
		t.Errorf("최근 세션 status = %q, want running", home.Recent[0].Status)
	}
}

// TestScreenContract_HomeBreakdown_MatchesHomeTotals 는 분해 화면이 Home 과 같은 날을
// 같은 값으로 본다는 계약이다 (HomeBreakdown.Totals 주석).
func TestScreenContract_HomeBreakdown_MatchesHomeTotals(t *testing.T) {
	f := newScreenFixture(t)
	ctx := context.Background()

	home, err := f.svc.Home(ctx, dashboard.HomeQuery{TZ: screenTZ, Date: f.date})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	bd, err := f.svc.HomeBreakdown(ctx, dashboard.HomeBreakdownQuery{TZ: screenTZ, Date: f.date})
	if err != nil {
		t.Fatalf("HomeBreakdown: %v", err)
	}

	if bd.StartAt != home.StartAt || bd.EndAt != home.EndAt {
		t.Errorf("구간이 다르다: home=[%d,%d) breakdown=[%d,%d)",
			home.StartAt, home.EndAt, bd.StartAt, bd.EndAt)
	}
	if bd.Totals != home.Totals {
		t.Errorf("Totals 가 다르다:\nhome      = %+v\nbreakdown = %+v", home.Totals, bd.Totals)
	}
	if bd.Cost.Total != home.Cost.Total {
		t.Errorf("예상 비용이 다르다: home=%v breakdown=%v", home.Cost.Total, bd.Cost.Total)
	}
	if len(bd.Vendors) != 1 || string(bd.Vendors[0].Vendor) != "claude_code" {
		t.Errorf("벤더 줄 = %+v, want claude_code 하나", bd.Vendors)
	}
	// 창 골격은 언제나 유지된다.
	if len(bd.Windows) != 12 {
		t.Errorf("2시간 창 = %d개, want 12", len(bd.Windows))
	}
}

// TestScreenContract_Activity_ListsTheStoredSession 은 Activity 목록이 저장된 세션을
// 화면 열(제목·프로젝트·수치)과 함께 돌려주는지 본다.
func TestScreenContract_Activity_ListsTheStoredSession(t *testing.T) {
	f := newScreenFixture(t)
	ctx := context.Background()

	page, err := f.svc.Activity(ctx, dashboard.ActivityQuery{})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("Activity 줄 = %d, want 1", len(page.Rows))
	}
	row := page.Rows[0]
	if row.ID != f.sessionID {
		t.Errorf("id = %d, want %d", row.ID, f.sessionID)
	}
	if row.SessionKey != fixtureSession {
		t.Errorf("session_key = %q, want %q", row.SessionKey, fixtureSession)
	}
	if !strings.Contains(row.Title, "temporality") {
		t.Errorf("title = %q — 첫 프롬프트에서 파생된 제목이어야 한다", row.Title)
	}
	if row.ProjectName != filepath.Base(fixturePath) {
		t.Errorf("project_name = %q, want %q", row.ProjectName, filepath.Base(fixturePath))
	}
	wantTools := int64(f.count(`SELECT COUNT(*) FROM tool_calls`))
	if row.ToolCalls != wantTools {
		t.Errorf("tool_calls = %d, want %d", row.ToolCalls, wantTools)
	}
	wantErrs := int64(f.count(`SELECT COUNT(*) FROM tool_calls WHERE success = 0`))
	if row.ToolErrors != wantErrs {
		t.Errorf("tool_errors = %d, want %d", row.ToolErrors, wantErrs)
	}
	if page.HasMore {
		t.Error("HasMore = true — 한 건뿐인데 다음 페이지가 있다고 한다")
	}

	// 검색 필터도 같은 행을 찾아야 한다. 파일 경로는 file_changes.file_path 원경로다.
	hit, err := f.svc.Activity(ctx, dashboard.ActivityQuery{Text: filepath.Base(fixtureFilePath)})
	if err != nil {
		t.Fatalf("Activity(검색): %v", err)
	}
	if len(hit.Rows) != 1 {
		t.Fatalf("파일명 검색 결과 = %d줄, want 1", len(hit.Rows))
	}
	if !containsStr(hit.Rows[0].MatchedSources, dashboard.SourceFile) {
		t.Errorf("matched_sources = %v, want file 포함", hit.Rows[0].MatchedSources)
	}
}

// TestScreenContract_SessionDetail_HasFilesToolsAndTimeline 은 세션 상세 한 장이
// 파일·툴 타임라인을 저장된 승격 행 그대로 담는지 본다.
func TestScreenContract_SessionDetail_HasFilesToolsAndTimeline(t *testing.T) {
	f := newScreenFixture(t)
	ctx := context.Background()

	detail, err := f.svc.Session(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !detail.Found {
		t.Fatalf("Found = false — 방금 저장한 세션 %d 를 못 찾는다", f.sessionID)
	}
	if got, want := int64(len(detail.Tools)), int64(f.count(`SELECT COUNT(*) FROM tool_calls`)); got != want {
		t.Errorf("툴 타임라인 = %d건, want %d", got, want)
	}
	if detail.ToolsTruncated {
		t.Error("ToolsTruncated = true — 세 건짜리 세션이 잘렸다고 한다")
	}
	if len(detail.Files) != 1 {
		t.Fatalf("파일 = %d건, want 1", len(detail.Files))
	}
	if detail.Files[0].FilePath != fixtureFilePath {
		t.Errorf("file_path = %q, want %q (ADR 0010 원경로)", detail.Files[0].FilePath, fixtureFilePath)
	}
	// 대상 경로도 원경로다. 툴 타임라인에서 파일로 건너뛸 수 있어야 한다.
	var withTarget int
	for _, tool := range detail.Tools {
		if tool.Target != "" {
			withTarget++
		}
	}
	if withTarget == 0 {
		t.Error("툴 타임라인의 target 이 전부 비었다 — tool_calls.target 이 화면에 닿지 않는다")
	}
}

// TestScreenContract_SessionMetrics_CoversEveryTurn 은 지표 화면의 상단 합계가
// 세션 전체(가상 턴 포함)를 덮는지 본다.
func TestScreenContract_SessionMetrics_CoversEveryTurn(t *testing.T) {
	f := newScreenFixture(t)
	ctx := context.Background()

	m, err := f.svc.SessionMetrics(ctx, dashboard.SessionMetricsQuery{SessionID: f.sessionID})
	if err != nil {
		t.Fatalf("SessionMetrics: %v", err)
	}
	if !m.Found {
		t.Fatal("Found = false")
	}
	wantTurns := int64(f.count(`SELECT COUNT(*) FROM turns WHERE session_id = ?`, f.sessionID))
	if m.Totals.TurnCount != wantTurns {
		t.Errorf("TurnCount = %d, want %d (가상 턴 포함)", m.Totals.TurnCount, wantTurns)
	}
	if int64(len(m.Turns)) != wantTurns || m.TurnsTruncated {
		t.Errorf("턴 목록 = %d건(잘림=%v), want %d건", len(m.Turns), m.TurnsTruncated, wantTurns)
	}
	wantLLM := int64(f.count(`SELECT COUNT(*) FROM llm_calls`))
	if m.Totals.LLMCalls != wantLLM {
		t.Errorf("LLMCalls = %d, want %d", m.Totals.LLMCalls, wantLLM)
	}
	if m.WorkspacePath != fixturePath {
		t.Errorf("workspace_path = %q, want %q", m.WorkspacePath, fixturePath)
	}

	// 상단 합계는 정확히 턴별 합이다 (SessionTotals 주석).
	var turnLLM, turnTools int64
	for _, tm := range m.Turns {
		turnLLM += tm.LLMCalls
		turnTools += tm.ToolCalls
	}
	if turnLLM != m.Totals.LLMCalls || turnTools != m.Totals.ToolCalls {
		t.Errorf("상단 합계와 턴 합이 다르다: llm %d vs %d, tool %d vs %d",
			m.Totals.LLMCalls, turnLLM, m.Totals.ToolCalls, turnTools)
	}
}

// TestScreenContract_FileChanges_MatchesRawTable 은 파일 변경 화면의 합계가 원본
// file_changes 와 같은지 본다. 화면이 잘려도 합계는 잘리지 않는다는 계약이다.
func TestScreenContract_FileChanges_MatchesRawTable(t *testing.T) {
	f := newScreenFixture(t)
	ctx := context.Background()

	fc, err := f.svc.FileChanges(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("FileChanges: %v", err)
	}
	if !fc.Found {
		t.Fatal("Found = false")
	}
	wantChanges := int64(f.count(`SELECT COUNT(*) FROM file_changes`))
	if fc.Totals.Changes != wantChanges {
		t.Errorf("Totals.Changes = %d, want %d", fc.Totals.Changes, wantChanges)
	}
	if len(fc.Files) != 1 {
		t.Fatalf("파일 = %d건, want 1", len(fc.Files))
	}
	if fc.Files[0].FilePath != fixtureFilePath {
		t.Errorf("file_path = %q, want %q", fc.Files[0].FilePath, fixtureFilePath)
	}
	if fc.Files[0].LastOperation != dashboard.FileOpModify {
		t.Errorf("last_operation = %q, want modify", fc.Files[0].LastOperation)
	}
	if fc.Totals.Operations.Modify != wantChanges {
		t.Errorf("operations.modify = %d, want %d", fc.Totals.Operations.Modify, wantChanges)
	}
}

// TestScreenContract_Classifier_ReadsStoredTurns 는 분류가 **저장된** 도구 호출과
// 이벤트 이름을 읽는지 본다. 픽스처의 턴에는 Edit(성공)·Bash(실패)·Read(실패)가 있다.
func TestScreenContract_Classifier_ReadsStoredTurns(t *testing.T) {
	f := newScreenFixture(t)
	ctx := context.Background()

	c := dashboard.NewClassifier(f.svc.Reader())
	got, err := c.Session(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("Classifier.Session: %v", err)
	}
	if got.SessionID != f.sessionID {
		t.Errorf("session_id = %d, want %d", got.SessionID, f.sessionID)
	}
	if got.TurnCount == 0 {
		t.Fatal("턴이 0 개다 — 분류 입력이 저장된 행에 닿지 않았다")
	}
	if got.WorkType == dashboard.WorkTypeUnknown {
		t.Errorf("work_type = unknown — 도구 호출 %d건이 있는데 근거를 못 찾았다 (%s)",
			f.count(`SELECT COUNT(*) FROM tool_calls`), got.WorkTypeReason)
	}
	// 근거가 비면 화면 툴팁이 빈다. 분류가 무엇을 보고 판정했는지 남아야 한다.
	var evidence int
	for _, turn := range got.Turns {
		evidence += len(turn.Evidence)
	}
	if evidence == 0 {
		t.Error("근거가 하나도 없다 — 분류 결과를 사후에 검증할 수 없다")
	}
	if len(got.Phases) == 0 {
		t.Error("단계가 비었다 — 「세션 흐름」 화면이 그릴 것이 없다")
	}
}

// TestScreenContract_Tray_SummarizesLocalStateWithoutCredentials 는 트레이 한 장이
// 로컬 상태를 요약하고, 자격증명이 없는 기계에서도 모양을 잃지 않는지 본다.
//
// 자격증명이 없으므로 벤더 어댑터는 **요청을 만들기 전에** unavailable 로 끝난다 —
// 실제 벤더 API 로 나가는 트래픽이 없다는 사실이 이 테스트의 전제다.
func TestScreenContract_Tray_SummarizesLocalStateWithoutCredentials(t *testing.T) {
	f := newScreenFixture(t)
	ctx := context.Background()

	snap, err := f.svc.Tray(ctx, dashboard.TrayQuery{TZ: screenTZ})
	if err != nil {
		t.Fatalf("Tray: %v", err)
	}
	// 데몬은 이미 멈췄고 DB 는 있다 — paused 다.
	if snap.Monitoring.State != dashboard.TrayStatePaused {
		t.Errorf("state = %q, want paused (DB 는 있고 데몬은 멈췄다)", snap.Monitoring.State)
	}
	if !snap.Monitoring.DatabaseAvailable {
		t.Error("database_available = false — 데몬이 만든 DB 를 못 본다")
	}
	if snap.Monitoring.LastEventAt == 0 {
		t.Error("last_event_at = 0 — 저장된 이벤트의 신선도가 트레이에 닿지 않는다")
	}
	if snap.Stale {
		t.Errorf("Stale = true (%s) — 로컬 조회는 성공했다", snap.StaleReason)
	}
	// 진행 중인 세션 하나가 있다.
	if snap.ActiveSessions != 1 || len(snap.ActiveAgents) != 1 {
		t.Errorf("활성 세션 = %d / 에이전트 = %v, want 1 / [claude_code]",
			snap.ActiveSessions, snap.ActiveAgents)
	}
	// 벤더는 전부 자리를 지키되 자격증명이 없어 unavailable 이다.
	if len(snap.Limits) != len(vendorlimit.SupportedVendors()) {
		t.Fatalf("한도 결과 = %d건, want %d (실패한 벤더도 자리를 지킨다)",
			len(snap.Limits), len(vendorlimit.SupportedVendors()))
	}
	for _, res := range snap.Limits {
		if res.State != vendorlimit.StateUnavailable {
			t.Errorf("%s state = %q — 자격증명이 없는데 available 이다", res.Vendor, res.State)
		}
		if res.Reason != vendorlimit.ReasonCredentialMissing {
			t.Errorf("%s reason = %q, want credential_missing", res.Vendor, res.Reason)
		}
		if res.Windows == nil {
			t.Errorf("%s windows = nil — JSON 에서 null 이 되어 화면이 분기해야 한다", res.Vendor)
		}
	}
	if snap.Tightest.Found {
		t.Error("가장 빠듯한 한도가 있다고 한다 — available 한 창이 하나도 없다")
	}
}

// TestScreenContract_Status_CountsMatchTheDatabase 는 Settings 화면의 근거가 실제
// 테이블 행 수와 같은지 본다.
func TestScreenContract_Status_CountsMatchTheDatabase(t *testing.T) {
	f := newScreenFixture(t)
	ctx := context.Background()

	st, err := f.svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Available {
		t.Fatal("Available = false")
	}
	counts := []struct {
		name  string
		got   int64
		query string
	}{
		{"events", st.Counts.Events, `SELECT COUNT(*) FROM events`},
		{"turns", st.Counts.Turns, `SELECT COUNT(*) FROM turns`},
		{"sessions", st.Counts.Sessions, `SELECT COUNT(*) FROM sessions`},
		{"llm_calls", st.Counts.LLMCalls, `SELECT COUNT(*) FROM llm_calls`},
		{"tool_calls", st.Counts.ToolCalls, `SELECT COUNT(*) FROM tool_calls`},
		{"file_changes", st.Counts.FileChanges, `SELECT COUNT(*) FROM file_changes`},
		{"vendors", st.Counts.Vendors, `SELECT COUNT(*) FROM vendors`},
	}
	for _, c := range counts {
		if want := int64(f.count(c.query)); c.got != want {
			t.Errorf("Counts.%s = %d, want %d", c.name, c.got, want)
		}
	}
	if st.RunningSessions != 1 {
		t.Errorf("RunningSessions = %d, want 1", st.RunningSessions)
	}
	if st.NewestEventAt == 0 || st.OldestEventAt == 0 {
		t.Errorf("이벤트 시각 범위가 비었다: [%d, %d]", st.OldestEventAt, st.NewestEventAt)
	}
}

// TestScreenContract_Search_FindsStoredPromptAndPath 는 통합 검색이 저장된 원문과
// 원경로를 모두 뒤지는지 본다 (ADR 0009 의 LIKE 결정).
func TestScreenContract_Search_FindsStoredPromptAndPath(t *testing.T) {
	f := newScreenFixture(t)
	ctx := context.Background()

	cases := []struct {
		name   string
		text   string
		source string
	}{
		{name: "원문", text: fixturePrompt, source: dashboard.SourceContent},
		{name: "파일경로", text: filepath.Base(fixtureFilePath), source: dashboard.SourceFile},
		{name: "작업폴더", text: filepath.Base(fixturePath), source: dashboard.SourceWorkspace},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits, err := f.svc.Search(ctx, dashboard.SearchQuery{Text: tc.text})
			if err != nil {
				t.Fatalf("Search(%q): %v", tc.text, err)
			}
			if len(hits) != 1 {
				t.Fatalf("결과 = %d건, want 1", len(hits))
			}
			if hits[0].ID != f.sessionID {
				t.Errorf("id = %d, want %d", hits[0].ID, f.sessionID)
			}
			if !containsStr(hits[0].Sources, tc.source) {
				t.Errorf("sources = %v, want %q 포함", hits[0].Sources, tc.source)
			}
		})
	}
}

// ── 화면 사이의 합의 ────────────────────────────────────────────────────────

// TestScreenContract_SurfacesAgreeOnTheSameSession 은 같은 세션을 보는 네 화면이
// 서로 다른 숫자를 말하지 않는지 본다. 각 화면의 자기 일관성은 앞선 티켓들이 이미
// 고정했고, **화면끼리의 합의** 는 여기서만 검증된다.
func TestScreenContract_SurfacesAgreeOnTheSameSession(t *testing.T) {
	f := newScreenFixture(t)
	ctx := context.Background()

	page, err := f.svc.Activity(ctx, dashboard.ActivityQuery{})
	if err != nil {
		t.Fatalf("Activity: %v", err)
	}
	if len(page.Rows) != 1 {
		t.Fatalf("Activity 줄 = %d, want 1", len(page.Rows))
	}
	row := page.Rows[0]

	detail, err := f.svc.Session(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	metrics, err := f.svc.SessionMetrics(ctx, dashboard.SessionMetricsQuery{SessionID: f.sessionID})
	if err != nil {
		t.Fatalf("SessionMetrics: %v", err)
	}
	changes, err := f.svc.FileChanges(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("FileChanges: %v", err)
	}
	home, err := f.svc.Home(ctx, dashboard.HomeQuery{TZ: screenTZ, Date: f.date})
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if len(home.Recent) != 1 {
		t.Fatalf("최근 세션 = %d건, want 1", len(home.Recent))
	}
	recent := home.Recent[0]

	// Activity 한 줄과 세션 상세는 같은 SessionRow 를 낸다.
	// EndedAt 이 포인터라 == 는 주소를 본다. 값 비교여야 한다.
	if !reflect.DeepEqual(row.SessionRow, detail.Session) {
		t.Errorf("Activity 줄과 세션 상세가 다르다:\nactivity = %+v\ndetail   = %+v",
			row.SessionRow, detail.Session)
	}
	// 지표 화면도 같은 세션을 가리킨다.
	if metrics.SessionKey != row.SessionKey || metrics.Vendor != row.Vendor {
		t.Errorf("SessionMetrics 가 다른 세션을 본다: %q/%q vs %q/%q",
			metrics.SessionKey, metrics.Vendor, row.SessionKey, row.Vendor)
	}
	if metrics.Totals.ToolCalls != row.ToolCalls {
		t.Errorf("툴 호출 수가 다르다: metrics=%d activity=%d", metrics.Totals.ToolCalls, row.ToolCalls)
	}
	// 파일 변경 화면의 파일 수는 세션 상세의 파일 줄 수와 같다.
	if changes.Totals.Files != int64(len(detail.Files)) {
		t.Errorf("파일 수가 다르다: filechanges=%d detail=%d",
			changes.Totals.Files, len(detail.Files))
	}
	// Home 최근 세션은 세션 **생애 전체** 를 담는다. 같은 세션이므로 토큰도 같다.
	// SessionRow 는 입력·출력을 따로 두고 RecentSession 은 합(입력+출력)을 둔다.
	rowTokens := row.InputTokens + row.OutputTokens
	if recent.ID != row.ID || recent.Tokens != rowTokens {
		t.Errorf("Home 최근 세션과 Activity 줄이 다르다: id %d/%d, 토큰 %d/%d",
			recent.ID, row.ID, recent.Tokens, rowTokens)
	}
	// 자정을 넘긴 세션도 잘린 목록도 없으므로 행 합 = 카드다 (home.go 「합계의 정의」).
	if recent.Tokens != home.Totals.Tokens() {
		t.Errorf("최근 세션 토큰 합 %d ≠ 카드 %d — 자정을 넘긴 세션도 잘림도 없는데 어긋났다",
			recent.Tokens, home.Totals.Tokens())
	}
	// 예상 비용은 보고값 합보다 작지 않다 (home.go 「비용은 aggregate 의 SUM 이 아니다」).
	// 보고값이 없는 호출을 가격표로 메우므로 항상 크거나 같다. costEpsilon 은 float 합산과
	// 정수 nano-USD 합산의 마지막 자리 차이만 허용한다.
	if home.Cost.Total.USD < home.Totals.CostUSD-costEpsilon {
		t.Errorf("예상 비용 %v 가 보고값 합 %v 보다 작다", home.Cost.Total.USD, home.Totals.CostUSD)
	}
}

// ── 프라이버시 이음매 ───────────────────────────────────────────────────────

// TestScreenContract_PrivacySeam_LocalHasIdentityUpstreamDoesNot 는 이 에픽에서 가장
// 중요한 불변식이다 (ADR 0010 인수조건).
//
// 같은 픽스처 한 벌에 대해 (a) 원경로·이메일·계정 ID 가 **로컬 저장과 화면 응답에는 있고**
// (b) 상위 Collector 가 실제로 받은 바이트에는 **없음** 을 한 자리에서 단언한다.
// 둘 중 하나만 보면 "로컬에 없다" 와 "상위로 샌다" 중 어느 쪽으로 무너져도 알아채지 못한다.
//
// otlpdecode/privacy_test.go 가 같은 성질을 **디코드 결과** 수준에서 고정한다. 여기는 그
// 결정이 수신기·저장·전송을 전부 통과한 뒤에도 유지되는지를 본다 — 배선 한 줄만 바뀌어도
// 무너질 수 있는 자리라 두 층 모두에 단언이 필요하다.
func TestScreenContract_PrivacySeam_LocalHasIdentityUpstreamDoesNot(t *testing.T) {
	f := newScreenFixture(t)
	ctx := context.Background()

	// (a) 로컬 — 저장된 행에도, 화면 응답에도 있다.
	if got := dumpText(t, f.db, "sessions"); !strings.Contains(got, fixtureEmail) {
		t.Error("sessions 에 user_email 이 없다 — ADR 0010 이 허용한 로컬 저장이 끊겼다")
	}
	detail, err := f.svc.Session(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if detail.Session.WorkspacePath != fixturePath {
		t.Errorf("화면의 workspace_path = %q, want %q", detail.Session.WorkspacePath, fixturePath)
	}
	changes, err := f.svc.FileChanges(ctx, f.sessionID)
	if err != nil {
		t.Fatalf("FileChanges: %v", err)
	}
	if len(changes.Files) != 1 || changes.Files[0].FilePath != fixtureFilePath {
		t.Errorf("화면의 file_path 가 원경로가 아니다: %+v", changes.Files)
	}

	// (b) 상위 전달 — 회사 기본 Privacy(전부 false)에서는 하나도 나가지 않는다.
	bodies := f.h.upstream.received()
	if len(bodies) == 0 {
		t.Fatal("상위가 받은 페이로드가 없다 — 이 단언이 공허해진다")
	}
	forbidden := []struct {
		what  string
		value string
	}{
		{"user.email", fixtureEmail},
		{"파일 원경로", fixtureFilePath},
		{"원문 프롬프트", fixturePrompt},
	}
	for i, body := range bodies {
		for _, fb := range forbidden {
			if strings.Contains(string(body), fb.value) {
				t.Errorf("상위 전달 페이로드[%d]에 %s 가 남았다: %q", i, fb.what, fb.value)
			}
		}
	}

	// 화면 응답 자체에는 토큰 조각이 없어야 한다. 로컬이 관대하다는 것이 비밀까지
	// 담아도 된다는 뜻은 아니다 (ADR 0010 「토큰은 여전히 어디에도 저장하지 않는다」).
	st, err := f.svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if strings.Contains(dumpAll(t, f.db), testIngestToken) {
		t.Error("로컬 DB 어딘가에 ingest 토큰이 저장됐다")
	}
	if strings.Contains(st.DatabasePath+st.DataDir+st.Daemon.Endpoint, testIngestToken) {
		t.Error("Status 응답에 ingest 토큰이 실렸다")
	}
}

// dumpAll 은 v3 도메인 테이블 전부를 한 문자열로 잇는다.
func dumpAll(t *testing.T, db *sql.DB) string {
	t.Helper()
	var sb strings.Builder
	for _, table := range []string{
		"meta", "vendors", "sessions", "turns", "events", "llm_calls", "tool_calls", "file_changes",
	} {
		sb.WriteString(dumpText(t, db, table))
	}
	return sb.String()
}

// containsStr 는 문자열 목록에 값이 있는지 본다.
func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// costEpsilon 은 금액 부등호 단언이 허용하는 오차다. 보고값 합은 float64 SUM 이고 예상
// 비용은 정수 nano-USD 합이라 마지막 자리가 갈릴 수 있다.
const costEpsilon = 1e-9
