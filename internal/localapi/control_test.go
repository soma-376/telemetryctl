package localapi

import (
	"context"
	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
	"github.com/zalando/go-keyring"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestControlRequiresSeparateTokenAndInstance(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	h := NewControlHandler("control", dir, "started", 42, func() { calls++ })
	for _, tc := range []struct {
		name, method, token, started, origin string
		pid, want                            int
	}{
		{"ok", "POST", "control", "started", "", 42, 202},
		{"ingest", "POST", "ingest", "started", "", 42, 401},
		{"missing", "POST", "", "started", "", 42, 401},
		{"pid", "POST", "control", "started", "", 43, 409},
		{"instance", "POST", "control", "other", "", 42, 409},
		{"browser", "POST", "control", "started", "https://example.com", 42, 401},
		{"preflight", "OPTIONS", "control", "started", "", 42, 405},
		{"get", "GET", "control", "started", "", 42, 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, ShutdownPath, nil)
			r.Header.Set("Authorization", "Bearer "+tc.token)
			r.Header.Set("X-Pulsemetry-Local", "1")
			r.Header.Set("X-Pulsemetry-PID", strconv.Itoa(tc.pid))
			r.Header.Set("X-Pulsemetry-Started-At", tc.started)
			r.Header.Set("X-Pulsemetry-Data-Dir", filepath.Clean(dir))
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("%d != %d", w.Code, tc.want)
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS 허용")
			}
		})
	}
	if calls != 1 {
		t.Fatalf("종료 요청 횟수: %d", calls)
	}
}

func TestControlClientNeverFollowsRedirect(t *testing.T) {
	keyring.MockInit()
	if err := credential.Set(credential.AccountLocalControl, "control"); err != nil {
		t.Fatal(err)
	}
	hits := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(202) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer source.Close()
	dir := t.TempDir()
	endpoint := strings.Replace(source.URL, "127.0.0.1", "localhost", 1)
	if err := runtimeinfo.Write(runtimeinfo.PathIn(dir), runtimeinfo.Info{PID: os.Getpid(), StartedAt: "start", Endpoint: endpoint, ListenPort: 1234, ListenAddrs: []string{"localhost"}, DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := StopDaemon(context.Background(), dir); err == nil {
		t.Fatal("리다이렉트를 성공으로 처리했다")
	}
	if hits != 0 {
		t.Fatal("제어 토큰이 리다이렉트 대상에 전달됐다")
	}
}
