package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/your-org/pulsemetry/internal/dashboard"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
)

const healthcheckTestJSON = `{"status":"ok","queue_depth":3,"queue_capacity":256,"stats":{"accepted":11,"dropped":2}}`

func TestProbeHealthRejectsUnsafeEndpointsBeforeRequest(t *testing.T) {
	endpoints := []string{
		"", " ",
		"http://example.invalid:4318",
		"http://127.0.0.1:4318",
		"http://[::1]:4318",
		"https://localhost:4318",
		"ftp://localhost:4318",
		"HTTP://localhost:4318",
		"http://LOCALHOST:4318",
		"http://localhost.:4318",
		"http://localhost.example.invalid:4318",
		"http://user@localhost:4318",
		"http://localhost:4318@example.invalid:4318",
		"http://localhost",
		"http://localhost:",
		"http://localhost:0",
		"http://localhost:65536",
		"http://localhost:-1",
		"http://localhost:+4318",
		"http://localhost:port",
		"http://localhost:4318/",
		"http://localhost:4318/path",
		"http://localhost:4318?target=external",
		"http://localhost:4318?",
		"http://localhost:4318#fragment",
		"http://localhost:4318#",
		" http://localhost:4318",
		"http://localhost:4318 ",
	}
	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			requests := recordHealthcheckRequests(t)
			if _, err := probeHealth(endpoint); err == nil {
				t.Errorf("잘못된 endpoint %q 의 헬스체크가 성공했다", endpoint)
			}
			if got := requests.Load(); got != 0 {
				t.Errorf("endpoint 검증 전 요청 = %d회, want 0회 (%q)", got, endpoint)
			}
		})
	}
}

func TestProbeHealthAcceptsLocalhostPortBounds(t *testing.T) {
	for _, port := range []int{1, 65535} {
		t.Run(strconv.Itoa(port), func(t *testing.T) {
			requests := recordHealthcheckRequests(t)
			if _, err := probeHealth(fmt.Sprintf("http://localhost:%d", port)); err != nil {
				t.Fatalf("허용된 포트 %d 의 헬스체크: %v", port, err)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("허용된 endpoint 요청 = %d회, want 1회", got)
			}
		})
	}
}

func TestProbeHealthReadsLocalhostResponse(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.RequestURI() != receiver.HealthPath {
			t.Errorf("헬스체크 요청 = %s %s, want GET %s", r.Method, r.URL.RequestURI(), receiver.HealthPath)
		}
		writeHealthcheckResponse(t, w)
	}))
	t.Cleanup(server.Close)

	h, err := probeHealth(healthcheckLocalEndpoint(t, server))
	if err != nil {
		t.Fatalf("정상 localhost 헬스체크: %v", err)
	}
	if h.Status != "ok" || h.QueueDepth != 3 || h.QueueCapacity != 256 || h.Stats.Accepted != 11 || h.Stats.Dropped != 2 {
		t.Errorf("헬스체크 응답이 보존되지 않았다: %+v", h)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("정상 localhost 요청 = %d회, want 1회", got)
	}
}

func TestProbeHealthDoesNotFollowRedirects(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, kind := range []string{"absolute", "relative"} {
			t.Run(fmt.Sprintf("%d_%s", code, kind), func(t *testing.T) {
				var initialRequests, targetRequests atomic.Int64
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					targetRequests.Add(1)
					writeHealthcheckResponse(t, w)
				}))
				t.Cleanup(target.Close)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != receiver.HealthPath {
						targetRequests.Add(1)
						writeHealthcheckResponse(t, w)
						return
					}
					initialRequests.Add(1)
					location := target.URL + "/redirect-target"
					if kind == "relative" {
						location = "/redirect-target"
					}
					http.Redirect(w, r, location, code)
				}))
				t.Cleanup(server.Close)

				if _, err := probeHealth(healthcheckLocalEndpoint(t, server)); err == nil {
					t.Errorf("HTTP %d 리다이렉트를 헬스체크 성공으로 판정했다", code)
				}
				if got := initialRequests.Load(); got != 1 {
					t.Errorf("최초 /healthz 요청 = %d회, want 1회", got)
				}
				if got := targetRequests.Load(); got != 0 {
					t.Errorf("리다이렉트 목적지 요청 = %d회, want 0회", got)
				}
			})
		}
	}
}

func TestDaemonHealthcheckRejectsUnsafeRuntimeEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://example.invalid:4318", "http://localhost:4318/path"} {
		t.Run(endpoint, func(t *testing.T) {
			requests := recordHealthcheckRequests(t)
			dir := t.TempDir()
			writeRawHealthcheckRuntime(t, dir, os.Getpid(), endpoint)

			running, detail := daemonRunning(dir)
			if running || !strings.Contains(detail, "헬스체크") {
				t.Errorf("잘못된 endpoint 실행 판정 = (%v, %q), want false와 헬스체크 진단", running, detail)
			}
			out := renderHealthcheckDaemonStatus(t, dir)
			if !strings.Contains(out, "헬스체크: 응답 없음") {
				t.Errorf("status에 잘못된 endpoint 진단이 없다:\n%s", out)
			}
			if got := requests.Load(); got != 0 {
				t.Errorf("runtime 소비자 두 경계의 요청 = %d회, want 0회", got)
			}
		})
	}
}

func TestDaemonHealthcheckSkipsUnavailableRuntime(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(*testing.T, string)
		wantDetail string
		wantStatus string
	}{
		{
			name: "파일 없음", wantDetail: "runtime.json 없음", wantStatus: "runtime.json 없음",
		},
		{
			name: "깨진 JSON",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(runtimeinfo.PathIn(dir), []byte(`{"pid":`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantDetail: "runtime.json 읽기 실패", wantStatus: "runtime.json 없음",
		},
		{
			name: "죽은 PID",
			setup: func(t *testing.T, dir string) {
				writeRawHealthcheckRuntime(t, dir, maxPID, "http://localhost:4318")
			},
			wantDetail: "낡은 runtime.json", wantStatus: "낡은 runtime.json",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requests := recordHealthcheckRequests(t)
			dir := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, dir)
			}
			running, detail := daemonRunning(dir)
			if running || !strings.Contains(detail, tc.wantDetail) {
				t.Errorf("실행 판정 = (%v, %q), want false와 %q", running, detail, tc.wantDetail)
			}
			out := renderHealthcheckDaemonStatus(t, dir)
			if !strings.Contains(out, tc.wantStatus) || strings.Contains(out, "헬스체크:") {
				t.Errorf("status는 헬스체크 없이 %q를 표시해야 한다:\n%s", tc.wantStatus, out)
			}
			if got := requests.Load(); got != 0 {
				t.Errorf("데몬 미실행 시 요청 = %d회, want 0회", got)
			}
		})
	}
}

func TestDaemonHealthcheckReadsNormalRuntime(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeHealthcheckResponse(t, w)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	writeRawHealthcheckRuntime(t, dir, os.Getpid(), healthcheckLocalEndpoint(t, server))

	if running, detail := daemonRunning(dir); !running || detail != "" {
		t.Errorf("정상 runtime 실행 판정 = (%v, %q), want true와 빈 진단", running, detail)
	}
	out := renderHealthcheckDaemonStatus(t, dir)
	if !strings.Contains(out, "헬스체크: ok · 큐 3/256") || !strings.Contains(out, "접수 11") {
		t.Errorf("status에 정상 헬스체크 결과가 없다:\n%s", out)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("runtime 소비자별 헬스체크 요청 합계 = %d회, want 2회", got)
	}
}

type healthcheckRecordingTransport struct {
	requests atomic.Int64
}

func (r *healthcheckRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.requests.Add(1)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(healthcheckTestJSON)),
		Request:    req,
	}, nil
}

// 실제 외부 접속 없이 검증 누락을 잡는다. 정상 응답을 돌려줘도 잘못된 주소는
// transport까지 도달하지 않아야 한다. 전역 교체이므로 이 테스트들은 병렬로 돌리지 않는다.
func recordHealthcheckRequests(t *testing.T) *atomic.Int64 {
	t.Helper()
	previous := http.DefaultTransport
	recorder := &healthcheckRecordingTransport{}
	http.DefaultTransport = recorder
	t.Cleanup(func() { http.DefaultTransport = previous })
	return &recorder.requests
}

func healthcheckLocalEndpoint(t *testing.T, server *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("테스트 서버 포트: %v", err)
	}
	return "http://localhost:" + port
}

func writeHealthcheckResponse(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := io.WriteString(w, healthcheckTestJSON); err != nil {
		t.Errorf("테스트 헬스체크 응답: %v", err)
	}
}

// 변조된 파일도 만드는 fixture이므로 runtimeinfo.Write의 쓰기 검증을 거치지 않는다.
func writeRawHealthcheckRuntime(t *testing.T, dir string, pid int, endpoint string) {
	t.Helper()
	info := runtimeinfo.Info{
		SchemaVersion: runtimeinfo.SchemaVersion,
		PID:           pid,
		Endpoint:      endpoint,
		ListenPort:    4318,
		ListenAddrs:   []string{"127.0.0.1:4318"},
		DataDir:       dir,
		DatabasePath:  filepath.Join(dir, "pulsemetry.db"),
	}
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimeinfo.PathIn(dir), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func renderHealthcheckDaemonStatus(t *testing.T, dir string) string {
	t.Helper()
	reader, err := dashboard.Open(filepath.Join(dir, "pulsemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Errorf("dashboard.Reader.Close: %v", err)
		}
	})
	status, err := reader.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printDaemonStatus(&out, status)
	return out.String()
}
