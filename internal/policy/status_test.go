package policy

import (
	"errors"
	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/heartbeat"
	"testing"
	"time"
)

func TestPolicyTransitionsAndFreshness(t *testing.T) {
	now := time.Date(2026, 10, 29, 1, 0, 0, 0, time.UTC)
	manifest := contract.Manifest{Signals: contract.Signals{Logs: true}, Privacy: contract.Privacy{CollectToolDetails: true}}
	service := New(3, "applied", manifest, true, func() time.Time { return now })
	if service.Snapshot().Status != "checking" {
		t.Fatal("최초 확인을 적용 완료로 표시함")
	}
	expected := 4
	service.Record(heartbeat.Response{ExpectedConfigRevision: &expected}, nil)
	expected = 99
	snap := service.Snapshot()
	if snap.Status != "pending" || snap.AppliedRevision != 3 || *snap.ExpectedRevision != 4 || !snap.Signals.Logs || snap.Privacy.CollectUserPrompts {
		t.Fatalf("정책 상태: %+v", snap)
	}
	*snap.ExpectedRevision = 10
	if *service.Snapshot().ExpectedRevision != 4 {
		t.Fatal("조회가 내부 상태를 바꿈")
	}
	service.Record(heartbeat.Response{}, errors.New("offline"))
	if snap = service.Snapshot(); snap.Status != "error" || *snap.ExpectedRevision != 4 {
		t.Fatalf("실패 때 마지막 결과 보존: %+v", snap)
	}
	expected = 3
	service.Record(heartbeat.Response{ExpectedConfigRevision: &expected}, nil)
	if service.Snapshot().Status != "current" {
		t.Fatal("동일 버전 비교 실패")
	}
	now = now.Add(FreshFor + time.Second)
	if service.Snapshot().Status != "stale" {
		t.Fatal("절전 뒤 오래된 결과를 최신으로 표시함")
	}
	service.Record(heartbeat.Response{}, nil)
	if snap = service.Snapshot(); snap.Status != "unknown" || snap.ExpectedRevision != nil {
		t.Fatal("null을 최신 정책으로 표시함")
	}
	expected = 2
	service.Record(heartbeat.Response{ExpectedConfigRevision: &expected}, nil)
	if service.Snapshot().Status != "ahead" {
		t.Fatal("낮은 서버 판을 최신 적용으로 표시함")
	}
	if New(3, "", manifest, false, time.Now).Snapshot().Status != "disabled" {
		t.Fatal("서버 미설정")
	}
}
