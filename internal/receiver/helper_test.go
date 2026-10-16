package receiver

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

const testToken = "test-ingest-token-0123456789"

// collector 는 Sink 구현이다. 워커가 넘긴 배치를 모아 두고, gate 를 닫아 두면 워커를
// 막아 큐 포화를 만들 수 있다.
type collector struct {
	mu      sync.Mutex
	batches []Batch

	gate chan struct{} // nil 이 아니면 Consume 이 여기서 기다린다
	err  error
}

func (c *collector) Consume(ctx context.Context, b Batch) error {
	if c.gate != nil {
		select {
		case <-c.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.mu.Lock()
	c.batches = append(c.batches, b)
	c.mu.Unlock()
	return c.err
}

func (c *collector) snapshot() []Batch {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Batch(nil), c.batches...)
}

// newTestReceiver 는 로그를 버퍼로 받는 수신기를 만든다. 버퍼는 토큰 유출 검사에 쓴다.
func newTestReceiver(t *testing.T, mutate func(*Options)) (*Receiver, *collector, *bytes.Buffer) {
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
	rc, err := New(opt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rc.Close() })
	return rc, sink, logs
}

// authedRequest 는 인증 3중을 모두 만족하는 요청이다.
func authedRequest(method, path, contentType string, body []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set(LocalHeader, LocalHeaderValue)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req
}

func do(rc *Receiver, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	rc.ServeHTTP(rec, req)
	return rec
}

// minimalLogsJSON 은 이벤트 하나가 나오는 최소 OTLP/JSON 로그 페이로드다.
func minimalLogsJSON() []byte {
	return []byte(`{
      "resourceLogs": [{
        "resource": {"attributes": [
          {"key": "service.name", "value": {"stringValue": "claude-code"}},
          {"key": "session.id", "value": {"stringValue": "sess-1"}}
        ]},
        "scopeLogs": [{
          "logRecords": [{
            "timeUnixNano": "1750000000000000000",
            "eventName": "claude_code.user_prompt",
            "body": {"stringValue": "안녕"}
          }]
        }]
      }]
    }`)
}

// assertNoCORS 는 응답에 CORS 헤더가 하나도 없는지 본다.
func assertNoCORS(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for name := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Errorf("CORS 헤더가 응답에 있음: %s", name)
		}
	}
}
