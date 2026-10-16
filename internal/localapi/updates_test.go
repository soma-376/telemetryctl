package localapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
	"github.com/your-org/pulsemetry/internal/updatecheck"
	"github.com/zalando/go-keyring"
)

type storedUpdates struct {
	snapshot updatecheck.Snapshot
}

func (s *storedUpdates) Snapshot() updatecheck.Snapshot { return s.snapshot }

func TestUpdatesRouteOnlyReadsStoredResult(t *testing.T) {
	available := true
	source := &storedUpdates{snapshot: updatecheck.Snapshot{
		Status: updatecheck.StatusError, CurrentVersion: "1.0.0", LatestVersion: "1.1.0", UpdateAvailable: &available,
		LastAttemptAt: "2026-09-18T01:00:00Z", LastSuccessAt: "2026-09-17T01:00:00Z",
	}}
	nextCalls := 0
	handler := WithUpdates(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalls++
		w.WriteHeader(http.StatusNoContent)
	}), source)
	for range 2 {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, UpdatesPath, nil))
		var got updatecheck.Snapshot
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || !reflect.DeepEqual(got, source.snapshot) || nextCalls != 0 {
			t.Fatalf("캐시 조회: status=%d snapshot=%+v next=%d", w.Code, got, nextCalls)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, UpdatesPath, nil))
	if w.Code != http.StatusMethodNotAllowed || nextCalls != 0 {
		t.Fatalf("POST가 조회 또는 다른 경로를 호출함: %d next=%d", w.Code, nextCalls)
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, TrayPath, nil))
	if w.Code != http.StatusNoContent || nextCalls != 1 {
		t.Fatal("기존 경로가 보존되지 않았다")
	}
}

func TestUpdatesClientCarriesLocalAuthAndUnknownResult(t *testing.T) {
	keyring.MockInit()
	if err := credential.Set(credential.AccountLocalIngest, "local-update-test"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RequestURI() != UpdatesPath || r.Header.Get("Authorization") != "Bearer local-update-test" || r.Header.Get(receiver.LocalHeader) != receiver.LocalHeaderValue {
			t.Error("업데이트 로컬 요청의 경로 또는 인증이 잘못되었다")
		}
		writeJSON(w, updatecheck.Snapshot{Status: updatecheck.StatusChecking, CurrentVersion: "1.0.0"})
	}))
	defer srv.Close()
	client := updatesTestClient(t, srv.URL)
	got, err := client.Updates(context.Background())
	if err != nil || got.Status != updatecheck.StatusChecking || got.UpdateAvailable != nil || got.CurrentVersion != "1.0.0" {
		t.Fatalf("최초 미확인 상태: %+v err=%v", got, err)
	}
}

func TestUpdatesClientRejectsRedirectAndBadResponses(t *testing.T) {
	keyring.MockInit()
	if err := credential.Set(credential.AccountLocalIngest, "local-update-test"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		code int
		body string
	}{
		{"구형 데몬", 404, ""},
		{"응답 오류", 500, ""},
		{"잘못된 JSON", 200, "{"},
		{"여러 JSON", 200, "{}{}"},
		{"응답 상한", 200, strings.Repeat(" ", (64<<10)+1)},
		{"리다이렉트", 307, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.URL.Path != UpdatesPath {
					t.Error("리다이렉트 대상에 요청이 전달되었다")
					w.WriteHeader(http.StatusOK)
					return
				}
				w.Header().Set("Location", "/redirect-target")
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			if _, err := updatesTestClient(t, srv.URL).Updates(context.Background()); err == nil || hits != 1 {
				t.Fatalf("잘못된 응답 허용: err=%v hits=%d", err, hits)
			}
		})
	}
}

func updatesTestClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	dir := t.TempDir()
	endpoint := strings.Replace(serverURL, "127.0.0.1", "localhost", 1)
	if err := runtimeinfo.Write(runtimeinfo.PathIn(dir), runtimeinfo.Info{
		PID: os.Getpid(), Endpoint: endpoint, ListenPort: 1234, ListenAddrs: []string{"localhost"}, DataDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	return NewClient(dir)
}
