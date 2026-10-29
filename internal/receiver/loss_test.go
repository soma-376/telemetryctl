package receiver

import (
	"errors"
	"net/http"
	"testing"

	"github.com/your-org/pulsemetry/internal/otlpdecode"
)

func TestLossCountsPreForwardDropsBySignal(t *testing.T) {
	rc, _, _ := newTestReceiver(t, nil)
	// 닫힌 큐도 포화와 같은 폐기 분기를 지난다. 워커 경합 없이 시그널 구분을 검증한다.
	_ = rc.Close()
	for _, path := range []string{"/v1/logs", "/v1/logs", "/v1/metrics"} {
		do(rc, authedRequest(http.MethodPost, path, "application/json", []byte(`{}`)))
	}
	if rc.Lost(otlpdecode.PayloadLogs) != 2 || rc.Lost(otlpdecode.PayloadMetrics) != 1 || rc.Lost(otlpdecode.PayloadTraces) != 0 {
		t.Fatal("상위 전달 전 손실의 시그널 구분이 다름")
	}
}

func TestLossCountsDecodeFailureButNotLocalSinkFailure(t *testing.T) {
	rc, _, _ := newTestReceiver(t, func(o *Options) { o.Sink = &collector{err: errors.New("local storage failed")} })
	rc.consume(queueItem{kind: otlpdecode.PayloadLogs, enc: otlpdecode.EncodingJSON, body: []byte(`{"resourceLogs":"invalid"}`)})
	rc.consume(queueItem{kind: otlpdecode.PayloadMetrics, enc: otlpdecode.EncodingJSON, body: []byte(`{}`)})
	if rc.Lost(otlpdecode.PayloadLogs) != 1 || rc.Lost(otlpdecode.PayloadMetrics) != 0 || rc.Stats().SinkErrors != 1 {
		t.Fatal("디코드 손실과 이미 전달된 뒤의 로컬 저장 실패를 구분하지 않음")
	}
}
