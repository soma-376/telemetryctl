package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/forward"
	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
	"github.com/your-org/pulsemetry/internal/store"
)

// 픽스처 안의 표식. logs_session_walkthrough.json 에 실제로 들어 있는 문자열이다.
const (
	fixtureSession = "5f3a9c21-8e44-4bb0-9d1e-2a7c6b0f1234"
	fixturePrompt  = "OTLP 디코더에 temporality 분기를 넣고"
	fixtureEmail   = "kjy02927@gmail.com"
	fixtureOrgID   = "org_01HXYZQ7K3M9V2"
	fixturePath    = "/Users/jy/dev/projects/soma-376/telemetryctl"

	// fixtureNow 는 픽스처의 마지막 이벤트보다 뒤이면서 유휴 임계값(10분) 안쪽인 시각이다.
	// 시계를 주입해야 세션 마감과 보존 정책이 벽시계에 의존하지 않는다.
	fixtureUnix = 1786353362
)

const testIngestToken = "test-ingest-token-not-a-real-secret"

// upstream 은 회사 Collector 대역이다. 받은 본문을 전부 기록한다.
type upstream struct {
	srv *httptest.Server

	mu     sync.Mutex
	bodies [][]byte
	types  []string
	auth   []string
	paths  []string
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.bodies = append(u.bodies, body)
		u.types = append(u.types, r.Header.Get("Content-Type"))
		u.auth = append(u.auth, r.Header.Get("Authorization"))
		u.paths = append(u.paths, r.URL.Path)
		u.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *upstream) received() [][]byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([][]byte(nil), u.bodies...)
}

func (u *upstream) contentTypes() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.types...)
}

func (u *upstream) authHeaders() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.auth...)
}

// receivedPaths 는 상위가 실제로 받은 경로들이다. 시그널 게이팅을 볼 때 쓴다 —
// 본문만 보면 "전달되지 않았다" 와 "전달됐는데 내용이 비었다" 를 구분할 수 없다.
func (u *upstream) receivedPaths() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.paths...)
}

// syncBuffer 는 로거 출력을 테스트에서 안전하게 읽기 위한 버퍼다.
// 데몬은 여러 고루틴에서 로그를 쓴다.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type harness struct {
	t         *testing.T
	dataDir   string
	statePath string
	info      runtimeinfo.Info
	upstream  *upstream
	logs      *syncBuffer

	cancel context.CancelFunc
	exit   chan error
	once   sync.Once
}

type harnessOptions struct {
	// state 는 상태 파일을 쓰기 전에 손볼 기회다.
	state func(*installer.State)
	// daemon 은 Options 를 손볼 기회다.
	daemon func(*Options)
	// noStart 면 Run 을 띄우지 않고 준비만 한다.
	noStart bool
}

// start 는 데몬을 띄우고 기동이 끝날 때까지 기다린다.
func start(t *testing.T, o harnessOptions) *harness {
	t.Helper()
	h := prepare(t, o)
	if !o.noStart {
		h.run(o)
	}
	return h
}

func prepare(t *testing.T, o harnessOptions) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{
		t:         t,
		dataDir:   filepath.Join(dir, "data"),
		statePath: filepath.Join(dir, "state.json"),
		upstream:  newUpstream(t),
		logs:      &syncBuffer{},
		exit:      make(chan error, 1),
	}

	st := &installer.State{
		StateSchemaVersion: installer.StateSchemaVersion,
		InstallationID:     "inst-e2e",
		ServerURL:          h.upstream.srv.URL,
		ConfigRevision:     7,
		InstallerVersion:   installer.Version,
		InstalledAt:        "2026-08-10T00:00:00Z",
		Manifest: contract.Manifest{
			SchemaVersion: 1,
			OTLP:          contract.OTLP{Endpoint: h.upstream.srv.URL, Protocol: "http/protobuf"},
			Signals:       contract.Signals{Logs: true, Metrics: true},
			// Privacy 는 전부 false — 회사 기본값이다 (§4.6). 이 상태에서 원문·tool
			// details 가 상위로 나가면 ADR 0003 위반이다.
			Privacy: contract.Privacy{},
		},
		Local: installer.DefaultLocal(),
	}
	if o.state != nil {
		o.state(st)
	}
	if err := installer.SaveState(h.statePath, st); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) run(o harnessOptions) {
	h.t.Helper()
	ready := make(chan runtimeinfo.Info, 1)
	opts := Options{
		StatePath: h.statePath,
		DataDir:   h.dataDir,
		Logger:    log.New(h.logs, "", 0),
		// 실제 포트를 미리 확보해 두고 그 번호를 요청한다. FixedPort 가 아니므로
		// 그 사이에 누가 채 가도 임의 포트로 폴백하고, 테스트는 Ready 가 준 값을 쓴다.
		ListenPort:    freePort(h.t),
		IngestToken:   testIngestToken,
		ForwardTokens: forward.StaticToken("telemetry-token-for-test"),
		// 틱을 짧게 잡아 테스트가 실시간을 기다리지 않게 한다.
		Interval:      50 * time.Millisecond,
		FlushInterval: 20 * time.Millisecond,
		PruneInterval: time.Hour,
		TokenInterval: time.Hour,
		Now:           func() time.Time { return time.Unix(fixtureUnix, 0).UTC() },
		Ready:         func(i runtimeinfo.Info) { ready <- i },
	}
	if o.daemon != nil {
		o.daemon(&opts)
	}

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.exit <- Run(ctx, opts) }()

	select {
	case info := <-ready:
		h.info = info
	case err := <-h.exit:
		h.t.Fatalf("데몬이 기동하지 못했다: %v\n%s", err, h.logs.String())
	case <-time.After(20 * time.Second):
		h.t.Fatalf("데몬이 20초 안에 기동하지 못했다\n%s", h.logs.String())
	}
	h.t.Cleanup(func() { h.stop() })
}

// stop 은 데몬을 세우고 Run 이 돌아올 때까지 기다린다. 반환값은 종료에 걸린 시간이다.
func (h *harness) stop() time.Duration {
	var took time.Duration
	h.once.Do(func() {
		started := time.Now()
		h.cancel()
		select {
		case err := <-h.exit:
			if err != nil {
				h.t.Errorf("Run 이 오류로 끝났다: %v", err)
			}
		case <-time.After(60 * time.Second):
			h.t.Fatalf("데몬이 종료되지 않았다\n%s", h.logs.String())
		}
		took = time.Since(started)
	})
	return took
}

// post 는 픽스처를 수신기로 보낸다.
func (h *harness) post(path string, body []byte) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.info.Endpoint+path, bytes.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(receiver.LocalHeader, receiver.LocalHeaderValue)
	req.Header.Set("Authorization", "Bearer "+testIngestToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("수신기 POST 실패: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func (h *harness) postFixture(name string) *http.Response {
	h.t.Helper()
	kind := "/v1/logs"
	if strings.HasPrefix(name, "metrics") {
		kind = "/v1/metrics"
	}
	return h.post(kind, fixture(h.t, name))
}

// openDB 는 종료된 데몬이 남긴 DB 를 연다.
func (h *harness) openDB() *sql.DB {
	h.t.Helper()
	db, err := store.Open(context.Background(), store.PathIn(h.dataDir))
	if err != nil {
		h.t.Fatalf("store.Open: %v", err)
	}
	h.t.Cleanup(func() { db.Close() })
	return db.SQL()
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "otlpdecode", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// freePort 는 커널이 방금 내준 포트 번호를 돌려준다.
//
// 기본 포트(4318)를 그대로 쓰면 개발자 기계에서 진짜로 돌고 있는 데몬을 밀어내거나
// 그 데몬 때문에 폴백이 일어난다. 테스트는 언제나 남는 포트를 쓴다.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

// waitFor 는 조건이 참이 될 때까지 기다린다. 파이프라인은 비동기라 POST 반환이
// 저장 완료를 뜻하지 않는다.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s 가 제한 시간 안에 성립하지 않았다", what)
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// dumpText 는 테이블 하나를 문자열로 이어 붙인다. 전체 경로·이메일 부재 단언에 쓴다.
func dumpText(t *testing.T, db *sql.DB, table string) string {
	t.Helper()
	rows, err := db.Query("SELECT * FROM " + table)
	if err != nil {
		t.Fatalf("SELECT * FROM %s: %v", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for rows.Next() {
		cells := make([]any, len(cols))
		for i := range cells {
			cells[i] = new(sql.RawBytes)
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatal(err)
		}
		for _, c := range cells {
			sb.Write(*c.(*sql.RawBytes))
			sb.WriteByte('\n')
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// mustJSON 은 구버전 상태 파일을 손으로 만들 때 쓴다.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
