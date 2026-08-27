package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/autostart"
	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/event"
	"github.com/your-org/pulsemetry/internal/forward"
	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
	"github.com/your-org/pulsemetry/internal/store"
)

// 이 파일의 테스트는 전부 통합 테스트다. 9단계에서 값이 있는 것은 "패키지 하나가
// 맞게 동작하는가" 가 아니라 "여덟 개를 이었을 때 실제로 화면에 필요한 행이 생기는가"다.

// TestEndToEndWalkthroughProducesScreenRows 는 이 단계의 핵심 회귀 방어선이다.
//
// 계획서 「검증」의 엔드투엔드 절차를 그대로 코드로 옮겼다. 픽스처를 수신기에 POST 하면
// Activity 화면이 읽는 네 테이블(sessions·session_files·tool_events·vendors)과
// Today 카드가 읽는 rollup_hourly 에 실제 행이 생겨야 한다.
func TestEndToEndWalkthroughProducesScreenRows(t *testing.T) {
	h := start(t, harnessOptions{})

	if resp := h.postFixture("logs_session_walkthrough.json"); resp.StatusCode != http.StatusOK {
		t.Fatalf("수신기 응답 = %d, want 200", resp.StatusCode)
	}
	if resp := h.postFixture("metrics_lines_of_code.json"); resp.StatusCode != http.StatusOK {
		t.Fatalf("메트릭 응답 = %d, want 200", resp.StatusCode)
	}
	h.stop()

	db := h.openDB()

	var (
		id          string
		vendor      string
		title       string
		titleSource string
		status      string
		toolCalls   int64
		toolErrors  int64
		apiErrors   int64
		apiRequests int64
		prompts     int64
		projectName sql.NullString
	)
	err := db.QueryRow(`SELECT session_id, vendor, COALESCE(title,''), COALESCE(title_source,''),
	         status, tool_calls, tool_errors, api_errors, api_requests, prompts, project_name
	    FROM sessions`).Scan(&id, &vendor, &title, &titleSource, &status,
		&toolCalls, &toolErrors, &apiErrors, &apiRequests, &prompts, &projectName)
	if err != nil {
		t.Fatalf("sessions 조회: %v\n%s", err, h.logs.String())
	}

	if id != fixtureSession {
		t.Errorf("session_id = %q, want %q", id, fixtureSession)
	}
	if vendor != "claude_code" {
		t.Errorf("vendor = %q, want claude_code", vendor)
	}
	if titleSource != "prompt_head" {
		t.Errorf("title_source = %q, want prompt_head (원문에서 제목이 나와야 한다)", titleSource)
	}
	if !strings.Contains(title, "temporality") {
		t.Errorf("title = %q, want 첫 프롬프트에서 파생된 제목", title)
	}
	// 픽스처의 툴 이벤트는 Edit(성공)·Bash(실패)·Read(실패) 세 건이다.
	if toolCalls != 3 {
		t.Errorf("tool_calls = %d, want 3", toolCalls)
	}
	if toolErrors != 2 {
		t.Errorf("tool_errors = %d, want 2", toolErrors)
	}
	if apiErrors != 1 {
		t.Errorf("api_errors = %d, want 1", apiErrors)
	}
	if prompts != 1 {
		t.Errorf("prompts = %d, want 1", prompts)
	}
	// 총량(비용·토큰)은 메트릭에서만 세고 건수는 로그에서만 센다 — 같은 사실을 양쪽에서
	// 세면 비용이 두 배가 되기 때문이다 (session/state.go apply). 이 픽스처에는 cost
	// 메트릭이 없으므로 cost_usd 는 0 이고 api_requests 가 1 이어야 한다.
	if apiRequests != 1 {
		t.Errorf("api_requests = %d, want 1", apiRequests)
	}
	// 유휴 임계값(10분) 안쪽 시각을 주입했으므로 아직 진행 중이어야 한다.
	if status != "running" {
		t.Errorf("status = %q, want running", status)
	}
	// project_name 은 basename 만 — 전체 경로가 아니다 (ADR 0003).
	if !projectName.Valid || projectName.String != "telemetryctl" {
		t.Errorf("project_name = %v, want telemetryctl (basename 만)", projectName)
	}

	if n := countRows(t, db, `SELECT COUNT(*) FROM tool_events WHERE session_id = ?`, id); n != 3 {
		t.Errorf("tool_events = %d행, want 3", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM session_files WHERE session_id = ?`, id); n == 0 {
		t.Error("session_files 가 비었다 — tool_input 에서 파일이 나와야 한다")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM vendors WHERE vendor = 'claude_code'`); n != 1 {
		t.Errorf("vendors 행 = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM events`); n == 0 {
		t.Error("events 가 비었다")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM event_content WHERE kind = 'prompt'`); n != 1 {
		t.Errorf("event_content(prompt) = %d행, want 1 (원문 보관 기본 ON)", n)
	}
	// 한 이벤트에 tool_input 과 tool_result 가 함께 실려 온다. 조립기에는 한 건만
	// 넘기지만 저장은 둘 다 되어야 한다.
	if n := countRows(t, db, `SELECT COUNT(*) FROM event_content WHERE kind = 'tool_input'`); n == 0 {
		t.Error("event_content(tool_input) 이 비었다 — 이벤트당 여러 원문이 전부 저장되어야 한다")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM rollup_hourly WHERE dim = 'total'`); n == 0 {
		t.Error("rollup_hourly(total) 이 비었다 — Today 카드의 출처다")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM rollup_hourly WHERE dim = 'vendor'`); n == 0 {
		t.Error("rollup_hourly(vendor) 가 비었다 — Agent 사용 비율의 출처다")
	}

	var lastRollup string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, store.MetaLastRollupAt).Scan(&lastRollup); err != nil {
		t.Errorf("meta.last_rollup_at 이 없다: %v", err)
	}
	var installID string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, store.MetaInstallationID).Scan(&installID); err != nil || installID != "inst-e2e" {
		t.Errorf("meta.installation_id = %q (%v), want inst-e2e", installID, err)
	}
}

// TestNoFullPathsOutsideEventContent 는 ADR 0003 의 로컬 저장 규칙을 고정한다.
//
// 전체 경로가 허용된 자리는 event_content.body(tool_input 원문) 딱 하나다. 그 밖의
// 테이블에는 해시와 basename 만 있어야 하고, user.email·organization.id 는 어디에도
// 있으면 안 된다 — 그 둘은 allowlist 에 없으므로 담길 자리 자체가 없어야 한다.
func TestNoFullPathsOutsideEventContent(t *testing.T) {
	h := start(t, harnessOptions{})
	h.postFixture("logs_session_walkthrough.json")
	h.stop()

	db := h.openDB()

	for _, table := range []string{"sessions", "session_files", "tool_events", "events", "vendors", "rollup_hourly"} {
		dump := dumpText(t, db, table)
		if strings.Contains(dump, fixturePath) {
			t.Errorf("%s 에 전체 경로가 들어갔다 (%s)", table, fixturePath)
		}
		if strings.Contains(dump, fixtureEmail) {
			t.Errorf("%s 에 user.email 이 들어갔다", table)
		}
		if strings.Contains(dump, fixtureOrgID) {
			t.Errorf("%s 에 organization.id 가 들어갔다", table)
		}
	}

	// 원문 쪽은 반대다 — 여기에는 있어야 정상이고, 없으면 파일별 변경을 만들 수 없다.
	content := dumpText(t, db, "event_content")
	if !strings.Contains(content, fixturePath) {
		t.Error("event_content 에 tool_input 원문이 없다 — 파일 변경 목록의 원천이다")
	}
	if strings.Contains(content, fixtureEmail) {
		t.Error("event_content 에 user.email 이 들어갔다")
	}
}

// TestNoStoreContentDropsBodies 는 --no-store-content 가 저장소 단계에서 집행되는지 본다.
// 그때는 전체 경로가 DB 어디에도 남지 않는다.
func TestNoStoreContentDropsBodies(t *testing.T) {
	h := start(t, harnessOptions{
		daemon: func(o *Options) { o.NoStoreContent = true },
	})
	h.postFixture("logs_session_walkthrough.json")
	h.stop()

	db := h.openDB()
	if n := countRows(t, db, `SELECT COUNT(*) FROM event_content`); n != 0 {
		t.Errorf("event_content = %d행, want 0 (--no-store-content)", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM events`); n == 0 {
		t.Error("이벤트까지 사라졌다 — 원문만 버려야 한다")
	}
	for _, table := range []string{"sessions", "session_files", "tool_events", "events", "event_content"} {
		if dump := dumpText(t, db, table); strings.Contains(dump, fixturePath) {
			t.Errorf("--no-store-content 인데 %s 에 전체 경로가 남았다", table)
		}
	}
}

// TestForwardedBodyIsScrubbed 는 ADR 0003 의 종단 증명이다.
//
// 로컬은 원문과 tool details 를 보관하지만, 회사 Collector 대역이 받은 본문에는 그 둘이
// 없어야 한다. 회사 manifest 의 Privacy 가 전부 false 이기 때문이다.
func TestForwardedBodyIsScrubbed(t *testing.T) {
	h := start(t, harnessOptions{})
	h.postFixture("logs_session_walkthrough.json")

	waitFor(t, "상위 Collector 수신", func() bool { return len(h.upstream.received()) > 0 })
	h.stop()

	bodies := h.upstream.received()
	if len(bodies) == 0 {
		t.Fatalf("상위로 아무것도 전달되지 않았다\n%s", h.logs.String())
	}
	removed := []struct{ what, marker string }{
		{"프롬프트 원문(속성)", fixturePrompt},
		{"tool_input(파일 경로)", `"file_path"`},
		{"tool_result 본문", "signal: killed after 120s"},
		{"user.email", fixtureEmail},
	}
	// 반대로 남아야 하는 것. 포워더는 allowlist 가 아니라 denylist 다 (ADR 0003) —
	// 회사 manifest 의 Privacy 가 금지한 것만 지우고 나머지는 직결 시절과 똑같이
	// 흘려보낸다. cwd·organization.id 는 Privacy 에 대응 필드가 없으므로 그대로 간다.
	// 이 단언이 없으면 "다 지워 버려서 통과하는" 구현이 테스트를 통과한다.
	kept := []struct{ what, marker string }{
		{"service.name", "claude-code"},
		{"cost_usd", "cost_usd"},
		{"model", "claude-opus-4-6"},
		{"cwd (Privacy 대상이 아님)", fixturePath},
	}
	for i, b := range bodies {
		for _, r := range removed {
			if bytes.Contains(b, []byte(r.marker)) {
				t.Errorf("상위 전달 본문[%d]에 %s 가 남아 있다 (%q)", i, r.what, r.marker)
			}
		}
		for _, k := range kept {
			if !bytes.Contains(b, []byte(k.marker)) {
				t.Errorf("상위 전달 본문[%d]에서 %s 까지 사라졌다 — denylist 여야 한다", i, k.what)
			}
		}
	}
	// 받은 인코딩(JSON)을 그대로 미러링해야 한다.
	for i, ct := range h.upstream.contentTypes() {
		if !strings.Contains(ct, "json") {
			t.Errorf("상위 전달 Content-Type[%d] = %q, want json (받은 인코딩 보존)", i, ct)
		}
	}
	// 상위 요청에는 telemetry token 이 붙어야 한다. 안 붙으면 회사 Collector 가
	// 401 로 끊는데, 그 실패는 로그에만 남고 화면에는 보이지 않는다.
	for i, a := range h.upstream.authHeaders() {
		if !strings.HasPrefix(a, "Bearer ") {
			t.Errorf("상위 요청[%d] Authorization = %q, want Bearer", i, a)
		}
	}
	// 로그에는 어떤 토큰도 남지 않는다.
	if logs := h.logs.String(); strings.Contains(logs, "telemetry-token-for-test") ||
		strings.Contains(logs, testIngestToken) {
		t.Error("로그에 토큰이 새어 나왔다")
	}
}

// TestForward는회사가끈시그널을전달하지않는다 는 PROJ-45 의 완료 판정 기준 중 하나다.
//
// 로컬 배선은 시그널 셋을 전부 켜 놓고 받는다 (installer.localProfile). 회사가 끈 시그널을
// 상위 전달에서 막지 않으면 재배선만으로 회사가 받는 데이터가 늘어난다 —
// installer/local.go 의 불변식 1 위반이다.
//
// 하네스의 회사 manifest 는 traces 가 꺼져 있다 (Signals{Logs, Metrics}). 그래서 트레이스
// 페이로드는 수신기를 통과하되 상위로는 한 건도 나가면 안 되고, 로그·메트릭은 그대로 나가야
// 한다. 후자를 함께 보는 이유는 "다 막아 버려서 통과하는" 구현을 배제하기 위해서다.
func TestForward는회사가끈시그널을전달하지않는다(t *testing.T) {
	h := start(t, harnessOptions{})

	// 트레이스는 회사가 껐다. 수신기는 받아 주지만 상위로 가면 안 된다.
	if resp := h.post("/v1/traces", fixture(t, "logs_session_walkthrough.json")); resp.StatusCode != http.StatusOK {
		t.Fatalf("수신기가 트레이스를 거부했다: HTTP %d — 로컬은 받아야 한다", resp.StatusCode)
	}
	// 로그는 회사가 켰다. 이것이 도착하는 것으로 파이프라인이 살아 있음을 확인한다.
	h.postFixture("logs_session_walkthrough.json")

	waitFor(t, "상위 Collector 수신", func() bool { return len(h.upstream.received()) > 0 })
	h.stop()

	paths := h.upstream.receivedPaths()
	if len(paths) == 0 {
		t.Fatalf("상위로 아무것도 전달되지 않았다 — 게이팅이 로그까지 막았다\n%s", h.logs.String())
	}
	sawLogs := false
	for i, p := range paths {
		if p == "/v1/traces" {
			t.Errorf("상위 요청[%d] 경로 = %s — 회사가 끈 시그널이 전달됐다", i, p)
		}
		if p == "/v1/logs" {
			sawLogs = true
		}
	}
	if !sawLogs {
		t.Errorf("상위가 받은 경로 = %v — 회사가 켠 logs 가 없다", paths)
	}

	// 종료 요약에 시그널 차단 카운트가 남아야 한다. 이것이 "왜 트레이스가 안 보이는가" 를
	// 운영자가 확인하는 유일한 지점이다.
	if logs := h.logs.String(); !strings.Contains(logs, "시그널차단=") {
		t.Errorf("종료 요약에 시그널차단 카운트가 없다:\n%s", logs)
	}
}

// TestConcurrentPostsAreSerialized 는 직렬화 지점이 실제로 동작하는지 본다.
//
// -race 로 돌면 두 집계기를 두 수신기 워커가 동시에 만졌을 때 잡힌다. 동시에 총량도
// 확인한다 — 직렬화가 깨지면 카운터가 어긋나거나 데이터가 사라진다.
func TestConcurrentPostsAreSerialized(t *testing.T) {
	h := start(t, harnessOptions{})

	const posts = 12
	var wg sync.WaitGroup
	names := []string{
		"logs_session_walkthrough.json",
		"metrics_lines_of_code.json",
		"metrics_token_usage.json",
		"metrics_api_request.json",
	}
	payloads := make([][]byte, len(names))
	kinds := make([]string, len(names))
	for i, n := range names {
		payloads[i] = fixture(t, n)
		kinds[i] = "/v1/logs"
		if strings.HasPrefix(n, "metrics") {
			kinds[i] = "/v1/metrics"
		}
	}

	for i := range posts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			j := i % len(payloads)
			h.post(kinds[j], payloads[j])
		}(i)
	}
	wg.Wait()
	h.stop()

	db := h.openDB()
	// dedup_key 유니크 제약이 재전송을 접으므로 이벤트 수는 한 번 보낸 것과 같다.
	if n := countRows(t, db, `SELECT COUNT(*) FROM events`); n == 0 {
		t.Fatal("events 가 비었다")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Errorf("sessions = %d행, want 1 (같은 session.id 는 하나로 접혀야 한다)", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM tool_events`); n != 3 {
		t.Errorf("tool_events = %d행, want 3 (타임라인이 중복 삽입되면 안 된다)", n)
	}
}

// TestGracefulShutdownFlushesPendingWork 는 종료가 제한 시간 안에 끝나면서
// 미저장 데이터를 잃지 않는지 본다.
//
// flush 틱을 아주 길게 잡아 두면 POST 직후 시점에는 아무것도 저장돼 있지 않다.
// 그 상태에서 종료했을 때 행이 생기면 최종 flush 가 실제로 동작한 것이다.
func TestGracefulShutdownFlushesPendingWork(t *testing.T) {
	h := start(t, harnessOptions{
		daemon: func(o *Options) {
			// 어떤 주기 틱도 종료 전에 돌지 않게 한다.
			o.FlushInterval = time.Hour
			o.Interval = time.Hour
			o.BatchEvents = 100_000
			o.ShutdownTimeout = 10 * time.Second
		},
	})
	h.postFixture("logs_session_walkthrough.json")
	// 수신기 워커가 배치를 파이프라인에 넘길 때까지 기다린다.
	waitFor(t, "수신 배치 디코드", func() bool { return h.info.Endpoint != "" })
	time.Sleep(200 * time.Millisecond)

	took := h.stop()
	if took > 10*time.Second {
		t.Errorf("종료에 %s 걸렸다 — 제한 시간(10s)을 넘었다", took)
	}

	db := h.openDB()
	if n := countRows(t, db, `SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Errorf("종료 flush 후 sessions = %d행, want 1\n%s", n, h.logs.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM events`); n == 0 {
		t.Errorf("종료 flush 후 events 가 비었다\n%s", h.logs.String())
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM rollup_hourly`); n == 0 {
		t.Errorf("종료 flush 후 rollup_hourly 가 비었다\n%s", h.logs.String())
	}
}

// TestShutdownRemovesRuntimeJSON 는 runtime.json 의 수명이 데몬과 같은지 본다.
func TestShutdownRemovesRuntimeJSON(t *testing.T) {
	h := start(t, harnessOptions{})

	path := runtimeinfo.PathIn(h.dataDir)
	info, ok, err := runtimeinfo.Read(path)
	if err != nil || !ok {
		t.Fatalf("기동 중 runtime.json 이 없다: ok=%t err=%v", ok, err)
	}
	if info.Endpoint != h.info.Endpoint || info.ListenPort != h.info.ListenPort {
		t.Errorf("runtime.json = %+v, want endpoint=%s port=%d", info, h.info.Endpoint, h.info.ListenPort)
	}
	if info.PID != os.Getpid() {
		t.Errorf("runtime.json pid = %d, want %d", info.PID, os.Getpid())
	}
	if info.DatabasePath != store.PathIn(h.dataDir) {
		t.Errorf("runtime.json database_path = %q, want %q", info.DatabasePath, store.PathIn(h.dataDir))
	}
	// 토큰이 새지 않는지도 여기서 한 번 더 본다.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(testIngestToken)) {
		t.Error("runtime.json 에 ingest 토큰이 들어갔다")
	}

	h.stop()
	if _, ok, err := runtimeinfo.Read(path); err != nil || ok {
		t.Errorf("종료 후에도 runtime.json 이 남아 있다: ok=%t err=%v", ok, err)
	}
}

// TestNoReceiverSkipsListener 는 --no-receiver 조합이다.
func TestNoReceiverSkipsListener(t *testing.T) {
	h := start(t, harnessOptions{
		daemon: func(o *Options) { o.DisableReceiver = true },
	})
	if h.info.Endpoint != "" || h.info.ListenPort != 0 {
		t.Errorf("--no-receiver 인데 endpoint=%q port=%d 가 잡혔다", h.info.Endpoint, h.info.ListenPort)
	}
	if h.info.DatabasePath == "" {
		t.Error("--no-receiver 라도 저장소는 열려 있어야 한다")
	}
	// runtime.json 은 "어디서 듣고 있는가" 를 답하는 파일이라 듣는 곳이 없으면 쓰지
	// 않는다. 억지로 쓰면 runtimeinfo 의 listen_port 검증에 걸려 매 기동마다 경고가 뜬다.
	if _, ok, err := runtimeinfo.Read(runtimeinfo.PathIn(h.dataDir)); err != nil || ok {
		t.Errorf("--no-receiver 인데 runtime.json 이 있다: ok=%t err=%v", ok, err)
	}
	if logs := h.logs.String(); strings.Contains(logs, "runtime.json 기록 실패") {
		t.Errorf("runtime.json 기록 실패 경고가 떴다:\n%s", logs)
	}
	h.stop()

	// DB 는 만들어졌지만 아무 이벤트도 없다.
	db := h.openDB()
	if n := countRows(t, db, `SELECT COUNT(*) FROM events`); n != 0 {
		t.Errorf("events = %d행, want 0", n)
	}
}

// TestNoForwardKeepsLocalPipeline 는 --no-forward 조합이다.
// 로컬 수집은 그대로 돌고 상위로는 아무것도 나가지 않아야 한다.
func TestNoForwardKeepsLocalPipeline(t *testing.T) {
	h := start(t, harnessOptions{
		daemon: func(o *Options) {
			o.DisableForward = true
			// 전달을 끄면 토큰 공급자도 필요 없다. 비워도 기동해야 한다.
			o.ForwardTokens = nil
		},
	})
	h.postFixture("logs_session_walkthrough.json")
	h.stop()

	if n := len(h.upstream.received()); n != 0 {
		t.Errorf("--no-forward 인데 상위로 %d건이 나갔다", n)
	}
	db := h.openDB()
	if n := countRows(t, db, `SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Errorf("sessions = %d행, want 1 — 전달을 꺼도 로컬 집계는 돌아야 한다", n)
	}
}

// TestGRPCManifestRefusesUnlessNoForward 는 계획서의 "gRPC 상위 전달 defer" 를 고정한다.
//
// 조용히 수신만 하고 넘어가면 회사 Collector 로 가던 스트림이 아무도 모르게 끊긴다.
// 대신 --no-forward 로 명시하면 수신 전용으로 뜰 수 있어야 한다.
func TestGRPCManifestRefusesUnlessNoForward(t *testing.T) {
	grpcManifest := func(st *installer.State) { st.Manifest.OTLP.Protocol = "grpc" }

	h := prepare(t, harnessOptions{state: grpcManifest})
	logs := &syncBuffer{}
	err := Run(context.Background(), Options{
		StatePath:     h.statePath,
		DataDir:       h.dataDir,
		Logger:        log.New(logs, "", 0),
		IngestToken:   testIngestToken,
		ForwardTokens: forward.StaticToken("t"),
		ListenPort:    freePort(t),
	})
	if err == nil {
		t.Fatal("grpc manifest 인데 데몬이 그냥 떴다")
	}
	if !errors.Is(err, forward.ErrGRPCUnsupported) {
		t.Errorf("오류 = %v, want ErrGRPCUnsupported 로 감싼 것", err)
	}
	if !strings.Contains(err.Error(), "--no-forward") {
		t.Errorf("오류 메시지에 탈출구(--no-forward)가 없다: %v", err)
	}

	// --no-forward 면 뜬다.
	h2 := start(t, harnessOptions{
		state:  grpcManifest,
		daemon: func(o *Options) { o.DisableForward = true; o.ForwardTokens = nil },
	})
	if h2.info.Endpoint == "" {
		t.Error("--no-forward + grpc manifest 인데 수신기가 뜨지 않았다")
	}
	h2.stop()
}

// TestMissingServerURLRefusesUnlessNoForward 는 토큰 공급자를 만들 수 없을 때
// 조용히 전달을 포기하지 않는지 본다.
func TestMissingServerURLRefusesUnlessNoForward(t *testing.T) {
	h := prepare(t, harnessOptions{state: func(st *installer.State) { st.ServerURL = "" }})
	logs := &syncBuffer{}
	err := Run(context.Background(), Options{
		StatePath:   h.statePath,
		DataDir:     h.dataDir,
		Logger:      log.New(logs, "", 0),
		IngestToken: testIngestToken,
		ListenPort:  freePort(t),
	})
	if err == nil || !strings.Contains(err.Error(), "--no-forward") {
		t.Fatalf("오류 = %v, want server_url 없음 + 탈출구 안내", err)
	}
}

// TestStateSchemaMigrationV3ToV5 는 이미 설치된 사용자가 바이너리만 갈았을 때
// 재enroll 없이 동작하는지 본다.
//
// 특히 Local.StoreContent 가 중요하다. 제로값(false)을 그대로 쓰면 업그레이드만으로
// 원문 보관이 꺼진다 — 사용자가 끈 적 없는 설정이 조용히 바뀌는 것이다.
func TestStateSchemaMigrationV3ToV5(t *testing.T) {
	h := prepare(t, harnessOptions{noStart: true})

	// 버전 3 파일에는 local 키 자체가 없다.
	v3 := map[string]any{
		"state_schema_version": 3,
		"installation_id":      "inst-legacy",
		"server_url":           h.upstream.srv.URL,
		"config_revision":      5,
		"installer_version":    "0.0.9",
		"installed_at":         "2026-01-02T03:04:05Z",
		"manifest": contract.Manifest{
			SchemaVersion: 1,
			OTLP:          contract.OTLP{Endpoint: h.upstream.srv.URL, Protocol: "http/protobuf"},
			Signals:       contract.Signals{Logs: true},
		},
		"targets": []installer.Target{{Tool: "claude", Path: "/x", ManagedKeys: []string{"k"}}},
	}
	raw := mustJSON(t, v3)
	if err := os.WriteFile(h.statePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"local"`)) {
		t.Fatal("전제가 깨졌다: 버전 3 픽스처에 local 키가 있다")
	}

	h.run(harnessOptions{})
	h.postFixture("logs_session_walkthrough.json")
	h.stop()

	// 1. 올림이 디스크에 굳었다.
	got, migrated, err := installer.LoadStateMigrated(h.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Error("데몬이 저장한 파일을 다시 읽었는데 또 마이그레이션이 필요하다고 나온다")
	}
	if got.StateSchemaVersion != installer.StateSchemaVersion {
		t.Errorf("state_schema_version = %d, want %d", got.StateSchemaVersion, installer.StateSchemaVersion)
	}
	// 2. 기존 필드가 살아 있다.
	if got.InstallationID != "inst-legacy" || got.ConfigRevision != 5 || len(got.Targets) != 1 {
		t.Errorf("마이그레이션이 기존 필드를 잃었다: %+v", got)
	}
	// 3. 기본값이 채워졌다.
	if !got.Local.StoreContent {
		t.Error("Local.StoreContent 가 false 다 — 업그레이드만으로 원문 보관이 꺼졌다")
	}
	if got.Local.Enabled {
		t.Error("Local.Enabled 가 true 다 — 재배선은 opt-in 이어야 한다")
	}
	// 4. 그 기본값이 실제 동작에 반영됐다.
	db := h.openDB()
	if n := countRows(t, db, `SELECT COUNT(*) FROM event_content`); n == 0 {
		t.Error("마이그레이션 후 원문이 저장되지 않았다")
	}
	var retention string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, store.MetaRetentionDays).Scan(&retention); err != nil {
		t.Fatal(err)
	}
	if retention != "400" {
		t.Errorf("meta.retention_days = %q, want 400", retention)
	}
}

// TestFutureSchemaIsNotDowngraded 는 신버전이 쓴 파일을 구버전이 열었을 때
// 필드를 지우고 되쓰지 않는지 본다.
func TestFutureSchemaIsNotDowngraded(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"
	future := map[string]any{
		"state_schema_version": installer.StateSchemaVersion + 5,
		"installation_id":      "inst-future",
	}
	if err := os.WriteFile(path, mustJSON(t, future), 0o600); err != nil {
		t.Fatal(err)
	}
	got, migrated, err := installer.LoadStateMigrated(path)
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Error("미래 버전을 마이그레이션했다")
	}
	if got.StateSchemaVersion != installer.StateSchemaVersion+5 {
		t.Errorf("state_schema_version = %d, want 그대로", got.StateSchemaVersion)
	}
}

// TestRetentionIsFixedAt400Days 는 사용자 설정 없이 단일 정책이 메타에 기록되는지 본다.
func TestRetentionIsFixedAt400Days(t *testing.T) {
	h := start(t, harnessOptions{})
	h.stop()

	db := h.openDB()
	var v string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, store.MetaRetentionDays).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != "400" {
		t.Errorf("meta.retention_days = %q, want 400", v)
	}
}

// TestFixedPortFailsInsteadOfFallingBack 는 --listen 명시 시 하드 실패를 고정한다.
func TestFixedPortFailsInsteadOfFallingBack(t *testing.T) {
	busy, err := listenBusy(t)
	if err != nil {
		t.Skipf("포트를 잡을 수 없는 환경: %v", err)
	}

	h := prepare(t, harnessOptions{})
	logs := &syncBuffer{}
	runErr := Run(context.Background(), Options{
		StatePath:     h.statePath,
		DataDir:       h.dataDir,
		Logger:        log.New(logs, "", 0),
		IngestToken:   testIngestToken,
		ForwardTokens: forward.StaticToken("t"),
		ListenPort:    busy,
		FixedPort:     true,
	})
	if runErr == nil {
		t.Fatal("--listen 으로 못 잡는 포트를 줬는데 데몬이 떴다")
	}
	if !strings.Contains(runErr.Error(), "폴백") {
		t.Errorf("오류 = %v, want 폴백하지 않는다는 설명", runErr)
	}
}

// TestPortFallbackLogsRemergeNeedWithoutTouchingState 는 constraint 8 을 고정한다.
//
// 폴백이 일어나도 state.Local.ListenPort(설정된 의도)는 그대로여야 한다. 그 값이
// 실제 포트로 덮이면 "설정과 현실이 어긋났다"는 신호가 사라져 12단계가 재병합
// 필요를 판단할 근거를 잃는다.
func TestPortFallbackLogsRemergeNeedWithoutTouchingState(t *testing.T) {
	busy, err := listenBusy(t)
	if err != nil {
		t.Skipf("포트를 잡을 수 없는 환경: %v", err)
	}

	h := start(t, harnessOptions{
		state:  func(st *installer.State) { st.Local.ListenPort = busy },
		daemon: func(o *Options) { o.ListenPort = 0 }, // 상태 파일 값을 쓰게 한다
	})
	if h.info.ListenPort == busy {
		t.Skip("점유한 포트를 그대로 잡았다 — 폴백 경로가 아니다")
	}
	logs := h.logs.String()
	if !strings.Contains(logs, "재병합") {
		t.Errorf("폴백했는데 재병합 필요 로그가 없다:\n%s", logs)
	}
	h.stop()

	st, _, err := installer.LoadStateMigrated(h.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Local.ListenPort != busy {
		t.Errorf("state.Local.ListenPort = %d, want %d — 데몬이 '설정된 의도'를 덮었다",
			st.Local.ListenPort, busy)
	}
	// 런타임 사실은 runtime.json 쪽에 있었어야 한다.
	if h.info.ListenPort == 0 {
		t.Error("runtime.json 에 실제 포트가 없다")
	}
}

// TestPickContentPrefersPrompt 는 이벤트 하나에 원문이 여럿 붙었을 때의 선택 규칙이다.
func TestPickContentPrefersPrompt(t *testing.T) {
	tests := []struct {
		name string
		in   []event.Content
		want event.ContentKind
	}{
		{"없음", nil, ""},
		{"프롬프트 하나", []event.Content{{Kind: event.ContentPrompt}}, event.ContentPrompt},
		{
			"tool_input + tool_result 는 input 이 이긴다",
			[]event.Content{{Kind: event.ContentToolResult}, {Kind: event.ContentToolInput}},
			event.ContentToolInput,
		},
		{
			"프롬프트가 뒤에 있어도 이긴다",
			[]event.Content{{Kind: event.ContentToolResult}, {Kind: event.ContentPrompt}},
			event.ContentPrompt,
		},
		{
			"우선순위에 없는 값만 있으면 첫 번째",
			[]event.Content{{Kind: event.ContentKind("weird")}},
			event.ContentKind("weird"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickContent(tt.in).Kind; got != tt.want {
				t.Errorf("pickContent = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCapTailDropsOldest 는 저장이 계속 실패할 때 메모리가 새지 않는지 본다.
func TestCapTailDropsOldest(t *testing.T) {
	logger := log.New(&syncBuffer{}, "", 0)
	in := []int{1, 2, 3, 4, 5}
	got := capTail(in, 3, "테스트", logger)
	if len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Errorf("capTail = %v, want [3 4 5]", got)
	}
	same := capTail([]int{1, 2}, 3, "테스트", logger)
	if len(same) != 2 {
		t.Errorf("상한 이하인데 잘렸다: %v", same)
	}
}

// TestTimeoutStopSec는데몬종료예산보다커야한다 는 두 패키지에 걸친 불변식이다 (PROJ-55).
//
// systemd 유닛의 TimeoutStopSec 이 이 데몬의 종료 예산보다 작으면, systemd 가 flush
// 도중에 SIGKILL 을 보내 미저장 집계를 잃고 runtime.json 이 남는다. 그 증상은 재부팅
// 때만 나타나고 로그에는 아무 흔적도 없어서 재현이 거의 불가능하다.
//
// **테스트를 이쪽에 두는 것이 load-bearing 이다.** 반대 방향(internal/autostart 가
// internal/daemon 을 import)이면 SQLite·protobuf 가 CLI 의 status 경로까지 딸려 들어온다.
// 테스트 전용 역방향 import 는 프로덕션 의존 사이클을 만들지 않는다.
func TestTimeoutStopSec는데몬종료예산보다커야한다(t *testing.T) {
	if autostart.TimeoutStopSec <= DefaultShutdownTimeout {
		t.Fatalf("autostart.TimeoutStopSec(%v) 는 DefaultShutdownTimeout(%v) 보다 커야 한다",
			autostart.TimeoutStopSec, DefaultShutdownTimeout)
	}
}

// listenBusy 는 테스트가 붙들고 있는 포트를 돌려준다. 그 포트는 테스트가 끝날 때까지
// 다른 프로세스가 잡을 수 없다.
func listenBusy(t *testing.T) (int, error) {
	t.Helper()
	l4, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	t.Cleanup(func() { l4.Close() })
	port := l4.Addr().(*net.TCPAddr).Port
	// IPv6 loopback 도 같이 막아야 bindPairAt 이 확실히 실패한다.
	if l6, err := net.Listen("tcp", net.JoinHostPort("::1", strconv.Itoa(port))); err == nil {
		t.Cleanup(func() { l6.Close() })
	}
	return port, nil
}
