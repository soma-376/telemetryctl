package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/localapi"
	"github.com/your-org/pulsemetry/internal/runtimeinfo"
	"github.com/your-org/pulsemetry/internal/updatecheck"
	"github.com/zalando/go-keyring"
)

func TestUpdateStatusDistinguishesUnknownAndPreservesLastSuccess(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		status    string
		available *bool
		want      string
	}{
		{updatecheck.StatusDisabled, nil, "비활성"},
		{updatecheck.StatusChecking, nil, "확인 중"},
		{updatecheck.StatusReady, &no, "최신 버전"},
		{updatecheck.StatusReady, &yes, "새 버전 있음"},
		{updatecheck.StatusUnsupported, nil, "지원하지 않음"},
		{updatecheck.StatusError, nil, "서버 확인 실패"},
		{updatecheck.StatusError, &yes, "서버 확인 실패"},
		{updatecheck.StatusReady, nil, "확인 불가"},
	} {
		t.Run(tc.status+tc.want, func(t *testing.T) {
			snapshot := updatecheck.Snapshot{Status: tc.status, CurrentVersion: "1.0.0", UpdateAvailable: tc.available}
			if tc.available != nil {
				snapshot.LatestVersion = "1.1.0"
				snapshot.LastSuccessAt = "2026-09-17T01:00:00Z"
			}
			var output bytes.Buffer
			printUpdateSnapshot(&output, snapshot)
			got := output.String()
			if !strings.Contains(got, tc.want) || !strings.Contains(got, "데몬 버전 1.0.0") {
				t.Fatalf("출력=%q", got)
			}
			if tc.available == nil && strings.Contains(got, "최신 버전") {
				t.Fatalf("미확인을 최신으로 표시함: %q", got)
			}
			if tc.available != nil && (!strings.Contains(got, snapshot.LastSuccessAt) || !strings.Contains(got, snapshot.LatestVersion)) {
				t.Fatalf("마지막 성공 결과가 사라짐: %q", got)
			}
		})
	}
}

func TestStatusUpdatesSurviveMissingDatabaseAndUnavailableDaemon(t *testing.T) {
	keyring.MockInit()
	if err := credential.Set(credential.AccountLocalIngest, "status-update-test"); err != nil {
		t.Fatal(err)
	}
	dir, args := tempTarget(t)
	writeState(t, statePathOf(dir), dir)
	state, err := installer.LoadState(statePathOf(dir))
	if err != nil {
		t.Fatal(err)
	}
	state.ServerURL = "https://enrollment.example.invalid"
	if err := installer.SaveState(statePathOf(dir), state); err != nil {
		t.Fatal(err)
	}
	// DB를 읽지 못해도 업데이트 상태를 독립적으로 표시한다.
	if err := os.WriteFile(filepath.Join(dir, "pulsemetry.db"), []byte("broken database"), 0600); err != nil {
		t.Fatal(err)
	}
	res := runCmd(t, runStatus, args...)
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%s", res.code, res.stderr)
	}
	res.mustContain(t, "데몬 업데이트: 확인 불가")

	for _, code := range []int{http.StatusOK, http.StatusNotFound} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != localapi.UpdatesPath {
					t.Errorf("예상하지 않은 조회: %s", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer status-update-test" {
					t.Error("로컬 인증 누락")
				}
				w.WriteHeader(code)
				if code == http.StatusOK {
					available := true
					_ = json.NewEncoder(w).Encode(updatecheck.Snapshot{
						Status: updatecheck.StatusReady, CurrentVersion: "1.0.0", LatestVersion: "1.1.0", UpdateAvailable: &available,
						LastSuccessAt: "2026-09-18T01:00:00Z", LastAttemptAt: "2026-09-18T01:00:00Z",
					})
				}
			}))
			defer srv.Close()
			if err := runtimeinfo.Write(runtimeinfo.PathIn(dir), runtimeinfo.Info{
				PID: os.Getpid(), Endpoint: strings.Replace(srv.URL, "127.0.0.1", "localhost", 1),
				ListenPort: 1234, ListenAddrs: []string{"localhost"}, DataDir: dir,
			}); err != nil {
				t.Fatal(err)
			}
			res := runCmd(t, runStatus, args...)
			if res.code != 0 || requests != 1 {
				t.Fatalf("code=%d requests=%d stdout=%s stderr=%s", res.code, requests, res.stdout, res.stderr)
			}
			if code == http.StatusOK {
				res.mustContain(t, "데몬 업데이트: 새 버전 있음", "서버 최신 버전 1.1.0")
			} else {
				res.mustContain(t, "데몬 업데이트: 확인 불가")
			}
		})
	}
}

func TestUpdateStatusUsesDaemonStateWhenCLIStateIsMissing(t *testing.T) {
	keyring.MockInit()
	if err := credential.Set(credential.AccountLocalIngest, "status-update-test"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		available := true
		_ = json.NewEncoder(w).Encode(updatecheck.Snapshot{
			Status: updatecheck.StatusReady, CurrentVersion: "1.0.0", LatestVersion: "1.1.0", UpdateAvailable: &available,
		})
	}))
	defer srv.Close()
	if err := runtimeinfo.Write(runtimeinfo.PathIn(dir), runtimeinfo.Info{
		PID: os.Getpid(), Endpoint: strings.Replace(srv.URL, "127.0.0.1", "localhost", 1),
		ListenPort: 1234, ListenAddrs: []string{"localhost"}, DataDir: dir,
	}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	printUpdateStatus(&output, localTarget{DataDir: dir})
	if !strings.Contains(output.String(), "새 버전 있음") || strings.Contains(output.String(), "비활성") {
		t.Fatalf("실행 데몬 대신 CLI의 빈 설치 상태를 사용함: %s", output.String())
	}
}
