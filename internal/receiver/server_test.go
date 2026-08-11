package receiver

import (
	"bytes"
	"context"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

func startTestServer(t *testing.T, mutate func(*Options)) (*Server, *collector, *bytes.Buffer) {
	t.Helper()
	sink := &collector{}
	logs := &bytes.Buffer{}
	opt := Options{
		Token:  testToken,
		Sink:   sink,
		Decode: otlpdecode.Options{InstallationID: "inst_test"},
		Logger: log.New(logs, "", 0),
	}
	if mutate != nil {
		mutate(&opt)
	}
	srv, err := Start(opt)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { shutdown(t, srv) })
	return srv, sink, logs
}

func shutdown(t *testing.T, srv *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// ingest 는 인증 3중을 갖춘 실제 HTTP 요청을 보낸다.
func ingest(t *testing.T, base string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/v1/logs", bytes.NewReader(minimalLogsJSON()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set(LocalHeader, LocalHeaderValue)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s 로 요청 실패: %v", base, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// 계획서 제약 §1 의 핵심. 리스너는 전부 loopback 이어야 하고, 둘이라면 **같은 포트**여야 한다.
// 한쪽만 임의 포트를 잡으면 벤더 설정에 적을 포트가 하나로 정해지지 않는다.
func TestListenersAreLoopbackAndShareOnePort(t *testing.T) {
	srv, _, _ := startTestServer(t, func(o *Options) { o.Port = freePort(t) })

	addrs := srv.Addrs()
	if len(addrs) == 0 {
		t.Fatal("리스너가 하나도 없다")
	}
	sawV4, sawV6 := false, false
	for _, a := range addrs {
		host, portStr, err := net.SplitHostPort(a)
		if err != nil {
			t.Fatalf("주소 파싱 %q: %v", a, err)
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Fatalf("loopback 이 아닌 주소에 바인딩됨: %s", a)
		}
		if portStr != strconv.Itoa(srv.Port()) {
			t.Fatalf("리스너 %s 의 포트가 서버 포트 %d 와 다르다", a, srv.Port())
		}
		if ip.To4() != nil {
			sawV4 = true
		} else {
			sawV6 = true
		}
	}
	if !sawV4 {
		t.Error("127.0.0.1 리스너가 없다")
	}
	if srv.HasIPv6() != sawV6 {
		t.Errorf("HasIPv6()=%v 인데 실제 [::1] 리스너 존재=%v", srv.HasIPv6(), sawV6)
	}
	if !sawV6 {
		t.Log("이 환경에는 IPv6 loopback 이 없어 127.0.0.1 만 검증했다")
	}

	// Endpoint 는 반드시 localhost 표기다 — 127.0.0.1 은 manifest 검증에서 거부된다
	// (계획서 제약 §1, internal/contract/manifest.go:88).
	want := "http://localhost:" + strconv.Itoa(srv.Port())
	if srv.Endpoint() != want {
		t.Errorf("Endpoint() = %q, want %q", srv.Endpoint(), want)
	}
	if srv.HealthURL() != want+HealthPath {
		t.Errorf("HealthURL() = %q", srv.HealthURL())
	}
}

// 두 스택 모두에서 실제로 요청을 받아야 한다. 리스너를 열어 놓고 한쪽으로만 테스트하면
// "조용히 절반이 깨지는" 바로 그 상황을 놓친다.
func TestBothLoopbackStacksAcceptRequests(t *testing.T) {
	srv, sink, _ := startTestServer(t, func(o *Options) { o.Port = freePort(t) })
	addrs := srv.Addrs()

	for _, addr := range addrs {
		if code := ingest(t, "http://"+addr); code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", addr, code)
		}
	}

	// localhost 이름으로도 닿아야 한다 — 벤더 설정에 들어가는 것이 이 이름이다.
	resp, err := http.Get(srv.HealthURL())
	if err != nil {
		t.Fatalf("localhost 로 healthz 실패: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", resp.StatusCode)
	}

	shutdown(t, srv)
	if got := len(sink.snapshot()); got != len(addrs) {
		t.Fatalf("sink 배치 수 = %d, want %d", got, len(addrs))
	}
}

// 포트가 사용 중이면 임의 포트로 폴백하고, 두 리스너가 그 포트를 함께 잡아야 한다.
func TestPortFallbackWhenBusy(t *testing.T) {
	busy, _, _ := startTestServer(t, func(o *Options) { o.Port = freePort(t) })

	srv, _, logs := startTestServer(t, func(o *Options) { o.Port = busy.Port() })
	if srv.Port() == busy.Port() {
		t.Fatal("사용 중인 포트를 그대로 잡았다")
	}
	for _, a := range srv.Addrs() {
		_, portStr, _ := net.SplitHostPort(a)
		if portStr != strconv.Itoa(srv.Port()) {
			t.Fatalf("폴백 후 리스너 %s 가 다른 포트를 잡았다 (서버 포트 %d)", a, srv.Port())
		}
	}
	if !strings.Contains(logs.String(), "폴백") {
		t.Errorf("폴백 사실이 로그에 남지 않았다: %q", logs.String())
	}
	// 폴백한 포트로도 정상 수신해야 한다.
	if code := ingest(t, srv.Endpoint()); code != http.StatusOK {
		t.Fatalf("폴백 포트 status = %d", code)
	}
}

// --listen 을 명시했으면 폴백하지 않고 하드 실패다 (계획서 「리스크」).
func TestFixedPortFailsHardWhenBusy(t *testing.T) {
	busy, _, _ := startTestServer(t, func(o *Options) { o.Port = freePort(t) })

	srv, err := Start(Options{
		Port:      busy.Port(),
		FixedPort: true,
		Token:     testToken,
		Sink:      &collector{},
		Decode:    otlpdecode.Options{InstallationID: "inst_test"},
	})
	if err == nil {
		shutdown(t, srv)
		t.Fatal("--listen 을 명시했는데 폴백했다")
	}
	if !strings.Contains(err.Error(), "--listen") {
		t.Errorf("오류가 원인을 설명하지 않는다: %v", err)
	}
}

// Shutdown 은 여러 번 불려도 안전하고, 큐에 남은 배치를 마저 처리해야 한다.
// 종료 직전에 도착한 텔레메트리를 버리면 세션 마지막 구간이 통째로 사라진다.
func TestShutdownDrainsQueue(t *testing.T) {
	srv, sink, _ := startTestServer(t, func(o *Options) { o.Port = freePort(t) })

	for i := range 3 {
		if code := ingest(t, srv.Endpoint()); code != http.StatusOK {
			t.Fatalf("요청 %d status = %d", i, code)
		}
	}

	shutdown(t, srv)
	shutdown(t, srv) // 멱등해야 한다 (defer 와 명시 호출이 겹친다)

	if got := len(sink.snapshot()); got != 3 {
		t.Fatalf("종료 후 처리된 배치 = %d, want 3", got)
	}
	// Server 는 카운터와 핸들러를 그대로 노출한다 — status 명령이 이 경로로 읽는다.
	if got := srv.Stats().Decoded; got != 3 {
		t.Errorf("Server.Stats().Decoded = %d, want 3", got)
	}
	if srv.Handler() == nil {
		t.Error("Server.Handler() 가 nil 이다")
	}
}

// verifyLoopback 은 loopback 이 아닌 리스너를 만나면 기동을 거부해야 한다.
// 지금 코드가 loopback 주소 상수만 넘기더라도, 그 전제가 깨지는 변경을 여기서 막는다.
func TestVerifyLoopbackRejectsNonLoopback(t *testing.T) {
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("0.0.0.0 바인딩 불가: %v", err)
	}
	port, err := listenerPort(l)
	if err != nil {
		_ = l.Close()
		t.Fatal(err)
	}
	if _, err := verifyLoopback(listenerSet{listeners: []net.Listener{l}, port: port}); err == nil {
		_ = l.Close()
		t.Fatal("0.0.0.0 리스너가 통과했다")
	} else if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("오류 메시지가 원인을 설명하지 않는다: %v", err)
	}
}

// 포트가 어긋난 리스너 조합도 거부해야 한다 — 두 리스너가 다른 포트를 잡는 회귀를 막는다.
func TestVerifyLoopbackRejectsPortMismatch(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port, err := listenerPort(l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyLoopback(listenerSet{listeners: []net.Listener{l}, port: port + 1}); err == nil {
		t.Fatal("포트가 어긋난 리스너가 통과했다")
	}
}

func TestVerifyLoopbackRejectsEmptySet(t *testing.T) {
	if _, err := verifyLoopback(listenerSet{port: 1}); err == nil {
		t.Fatal("리스너가 없는 집합이 통과했다")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port, err := listenerPort(l)
	if err != nil {
		_ = l.Close()
		t.Fatal(err)
	}
	_ = l.Close()
	return port
}
