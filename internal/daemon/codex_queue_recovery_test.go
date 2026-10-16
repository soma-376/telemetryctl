package daemon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/codexsource"
	"github.com/your-org/pulsemetry/internal/otlpdecode"
	"github.com/your-org/pulsemetry/internal/receiver"
)

func TestReceiverDroppedCodexPromptIsNotRebuiltFromJSONL(t *testing.T) {
	db := openTestStore(t)
	defer db.Close()
	p := newTestPipeline(t, db, &syncBuffer{}, func() time.Time { return time.Unix(1_700_000_000, 0) })
	gate := make(chan struct{})
	entered := make(chan struct{})
	var release sync.Once
	var enteredCount, delivered atomic.Int64
	sink := receiver.SinkFunc(func(ctx context.Context, batch receiver.Batch) error {
		if enteredCount.Add(1) == 1 {
			close(entered)
			<-gate
		}
		err := p.Consume(ctx, batch)
		if err == nil {
			delivered.Add(1)
		}
		return err
	})
	rc, err := receiver.New(receiver.Options{
		Token: testIngestToken, Sink: sink,
		Decode: receiverDecodeOptions(), QueueSize: 1, Workers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		release.Do(func() { close(gate) })
		_ = rc.Close()
	})
	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testIngestToken)
		req.Header.Set(receiver.LocalHeader, "1")
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		rc.ServeHTTP(response, req)
		return response
	}

	// 첫 배치는 Sink에서 멈추고, 둘째는 큐를 채운다. 셋째 사용자 프롬프트만 드롭된다.
	dummy := `{"resourceLogs":[]}`
	if response := post(dummy); response.Code != http.StatusOK {
		t.Fatalf("첫 배치: %d", response.Code)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("수신기 소비 장벽 진입 없음")
	}
	if response := post(dummy); response.Code != http.StatusOK {
		t.Fatalf("대기 배치: %d", response.Code)
	}
	droppedBody := codexPromptOTLP("dropped-owner", "dropped")
	decoded, err := otlpdecode.Decode(otlpdecode.PayloadLogs, []byte(droppedBody), otlpdecode.EncodingJSON, receiverDecodeOptions())
	if err != nil || len(decoded.Events) != 1 {
		t.Fatalf("드롭 대상이 정상 OTel 프롬프트가 아님: events=%d err=%v", len(decoded.Events), err)
	}
	dropped := post(droppedBody)
	if dropped.Code != http.StatusOK || !strings.Contains(dropped.Body.String(), `"partialSuccess"`) ||
		!strings.Contains(dropped.Body.String(), `"errorMessage"`) || strings.Contains(dropped.Body.String(), `"rejectedLogRecords"`) {
		t.Fatalf("드롭 응답 = %d %q", dropped.Code, dropped.Body.String())
	}
	if stats := rc.Stats(); stats.Dropped != 1 || stats.Accepted != 2 {
		t.Fatalf("수신 큐 판정: accepted=%d dropped=%d", stats.Accepted, stats.Dropped)
	}

	release.Do(func() { close(gate) })
	deadline := time.After(10 * time.Second)
	for delivered.Load() != 2 {
		select {
		case <-deadline:
			t.Fatalf("수신 큐 회복 지연: delivered=%d", delivered.Load())
		case <-time.After(time.Millisecond):
		}
	}
	if response := post(codexPromptOTLP("later-owner", "later")); response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"partialSuccess"`) {
		t.Fatalf("후속 정상 배치 = %d %q", response.Code, response.Body.String())
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if delivered.Load() != 3 {
		t.Fatalf("드롭 대상이 Sink에 전달됐거나 후속 배치가 누락됨: %d", delivered.Load())
	}
	if !p.close(time.Now().Add(10 * time.Second)) {
		t.Fatal("파이프라인 최종 flush 지연")
	}
	if got := countRows(t, db.SQL(), `SELECT COUNT(*) FROM turns t JOIN sessions s ON s.id=t.session_id WHERE s.session_key='dropped-owner'`); got != 0 {
		t.Fatalf("드롭한 OTel 턴이 저장됨: %d", got)
	}
	if got := countRows(t, db.SQL(), `SELECT COUNT(*) FROM turns t JOIN sessions s ON s.id=t.session_id WHERE s.session_key='later-owner'`); got != 1 {
		t.Fatalf("후속 정상 OTel 턴 = %d, want 1", got)
	}

	root := filepath.Join(t.TempDir(), "sessions")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ owner, body string }{{"dropped-owner", "dropped"}, {"later-owner", "later"}} {
		contents := fmt.Sprintf("%s\n%s\n",
			fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"source":"cli"}}`, tc.owner),
			fmt.Sprintf(`{"type":"event_msg","timestamp":"2023-11-14T22:13:20.250Z","payload":{"type":"user_message","message":%q}}`, tc.body))
		if err := os.WriteFile(filepath.Join(root, tc.owner+".jsonl"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	epoch, err := db.BeginCodexWorkerEpoch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := (codexsource.Worker{DB: db, Root: root, Epoch: epoch}).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, db.SQL(), `SELECT COUNT(*) FROM turns t JOIN sessions s ON s.id=t.session_id WHERE s.session_key='dropped-owner'`); got != 0 {
		t.Fatalf("JSONL이 드롭한 OTel 턴을 재구성함: %d", got)
	}
	if got := countRows(t, db.SQL(), `SELECT COUNT(*) FROM llm_calls`); got != 0 {
		t.Fatalf("JSONL이 OTel 없는 호출을 생성함: %d", got)
	}
	if got := countRows(t, db.SQL(), `SELECT COUNT(*) FROM codex_turn_provenance WHERE label='client_submitted' AND processing_state='finalized'`); got != 1 {
		t.Fatalf("후속 정상 턴 분류 = %d, want 1", got)
	}
}

func receiverDecodeOptions() otlpdecode.Options {
	return otlpdecode.Options{InstallationID: "inst-proj173"}
}

func codexPromptOTLP(owner, prompt string) string {
	return fmt.Sprintf(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex"}},{"key":"session.id","value":{"stringValue":%q}}]},"scopeLogs":[{"logRecords":[{"timeUnixNano":"1700000000000000000","body":{"stringValue":"codex.user_prompt"},"attributes":[{"key":"event.name","value":{"stringValue":"codex.user_prompt"}},{"key":"prompt","value":{"stringValue":%q}}]}]}]}]}`, owner, prompt)
}
