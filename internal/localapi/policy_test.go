package localapi

import (
	"context"
	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/heartbeat"
	"github.com/your-org/pulsemetry/internal/policy"
	"github.com/your-org/pulsemetry/internal/receiver"
	"github.com/zalando/go-keyring"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPolicyClientReadsAuthenticatedSnapshot(t *testing.T) {
	keyring.MockInit()
	if err := credential.Set(credential.AccountLocalIngest, "policy-test-only"); err != nil {
		t.Fatal(err)
	}
	service := policy.New(3, "", contract.Manifest{Signals: contract.Signals{Metrics: true}}, true, time.Now)
	expected := 4
	service.Record(heartbeat.Response{ExpectedConfigRevision: &expected}, nil)
	handler := WithPolicy(http.NotFoundHandler(), service)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != PolicyPath || r.Header.Get("Authorization") != "Bearer policy-test-only" || r.Header.Get(receiver.LocalHeader) != receiver.LocalHeaderValue {
			t.Error("정책 조회 인증·경로 오류")
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	snap, err := updatesTestClient(t, server.URL).Policy(context.Background())
	if err != nil || snap.AppliedRevision != 3 || snap.ExpectedRevision == nil || *snap.ExpectedRevision != 4 || snap.Status != "pending" || !snap.Signals.Metrics {
		t.Fatalf("GUI 정책 조회: %+v %v", snap, err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, PolicyPath, nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatal("쓰기 요청 허용")
	}
}

func TestPolicyClientRejectsMissingAppliedInformation(t *testing.T) {
	keyring.MockInit()
	_ = credential.Set(credential.AccountLocalIngest, "policy-test-only")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()
	if _, err := updatesTestClient(t, server.URL).Policy(context.Background()); err == nil {
		t.Fatal("비어 있는 정책을 실제 정책으로 표시함")
	}
}
