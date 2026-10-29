package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/your-org/pulsemetry/internal/heartbeat"
	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/policy"
	"github.com/your-org/pulsemetry/internal/receiver"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type policyReporter struct {
	fail     atomic.Bool
	revision atomic.Int32
}

func (r *policyReporter) Send(context.Context, heartbeat.Report) (heartbeat.Response, error) {
	if r.fail.Load() {
		return heartbeat.Response{}, errors.New("unavailable")
	}
	revision := int(r.revision.Load())
	return heartbeat.Response{ExpectedConfigRevision: &revision}, nil
}

func TestHeartbeatPolicyReachesAuthenticatedGUIRoute(t *testing.T) {
	reporter := &policyReporter{}
	reporter.revision.Store(4)
	h := start(t, harnessOptions{
		state: func(s *installer.State) { s.ServerURL = "https://example.test"; s.ConfigRevision = 3 },
		daemon: func(o *Options) {
			isolateUpdateTest(o)
			o.HeartbeatReporter = reporter
			o.HeartbeatInterval = 20 * time.Millisecond
		},
	})
	defer h.stop()
	read := func() policy.Snapshot {
		req, _ := http.NewRequest(http.MethodGet, h.info.Endpoint+"/v1/policy", nil)
		req.Header.Set("Authorization", "Bearer "+testIngestToken)
		req.Header.Set(receiver.LocalHeader, receiver.LocalHeaderValue)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("정책 조회: HTTP %d", resp.StatusCode)
		}
		var snap policy.Snapshot
		if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
			t.Fatal(err)
		}
		return snap
	}
	waitFor(t, "정책 대기", func() bool { return read().Status == "pending" })
	if snap := read(); snap.AppliedRevision != 3 || *snap.ExpectedRevision != 4 {
		t.Fatalf("적용 판이 서버 값으로 바뀜: %+v", snap)
	}
	reporter.fail.Store(true)
	waitFor(t, "확인 실패", func() bool { return read().Status == "error" })
	reporter.revision.Store(3)
	reporter.fail.Store(false)
	waitFor(t, "확인 복구", func() bool { return read().Status == "current" })
	req, _ := http.NewRequest(http.MethodGet, h.info.Endpoint+"/v1/policy", nil)
	req.Header.Set(receiver.LocalHeader, receiver.LocalHeaderValue)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("인증 없이 정책 조회됨")
	}
}
