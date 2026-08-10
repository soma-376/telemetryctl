package otlpdecode

import (
	"strings"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/your-org/pulsemetry/internal/event"
)

func anyInt64(v int64) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v}}
}

func anyDouble(v float64) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: v}}
}

func anyBoolValue(v bool) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v}}
}

// 벤더가 같은 의미의 값을 서로 다른 AnyValue 타입으로 보낸다. 타입 하나 때문에 수치가
// 통째로 비면 화면의 카드가 0 이 되므로 강제 변환 규칙을 고정한다.
func TestAttributeValueCoercion(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value *commonpb.AnyValue
		check func(*testing.T, carrier)
	}{
		{
			name: "duration_ms 가 double 로 와도 정수로 받는다",
			key:  "duration_ms", value: anyDouble(214.7),
			check: func(t *testing.T, c carrier) {
				if got, ok := c.measure.DurationMS.Get(); !ok || got != 214 {
					t.Errorf("duration_ms = (%d, %v)", got, ok)
				}
			},
		},
		{
			name: "input_tokens 가 문자열로 와도 받는다",
			key:  "input_tokens", value: anyStr("1820"),
			check: func(t *testing.T, c carrier) {
				if got, ok := c.measure.InputTokens.Get(); !ok || got != 1820 {
					t.Errorf("input_tokens = (%d, %v)", got, ok)
				}
			},
		},
		{
			name: "cost_usd 가 int 로 와도 받는다",
			key:  "cost_usd", value: anyInt64(2),
			check: func(t *testing.T, c carrier) {
				if got, ok := c.measure.CostUSD.Get(); !ok || got != 2 {
					t.Errorf("cost_usd = (%v, %v)", got, ok)
				}
			},
		},
		{
			name: "success 가 문자열로 와도 받는다",
			key:  "success", value: anyStr("false"),
			check: func(t *testing.T, c carrier) {
				got, ok := c.measure.Success.Get()
				if !ok || got {
					t.Errorf("success = (%v, %v), want (false, true)", got, ok)
				}
			},
		},
		{
			name: "success 가 정수로 와도 받는다",
			key:  "success", value: anyInt64(1),
			check: func(t *testing.T, c carrier) {
				if got, ok := c.measure.Success.Get(); !ok || !got {
					t.Errorf("success = (%v, %v)", got, ok)
				}
			},
		},
		{
			name: "숫자가 아닌 문자열은 미설정으로 남는다",
			key:  "input_tokens", value: anyStr("n/a"),
			check: func(t *testing.T, c carrier) {
				if c.measure.InputTokens.Valid() {
					t.Errorf("파싱 못 한 값이 설정됐다")
				}
			},
		},
		{
			name: "모르는 속성은 아무 데도 안 남는다",
			key:  "user.email", value: anyStr(fixtureUserEmail),
			check: func(t *testing.T, c carrier) {
				if c.attr != (event.Attributes{}) {
					t.Errorf("attr = %+v, want 비어 있음", c.attr)
				}
			},
		},
		{
			name: "event.timestamp 는 RFC3339 로 온다",
			key:  "event.timestamp", value: anyStr("2026-08-10T09:15:00Z"),
			check: func(t *testing.T, c carrier) {
				if c.tsFallback != 1786353300000000000 {
					t.Errorf("tsFallback = %d", c.tsFallback)
				}
			},
		},
		{
			name: "event.timestamp 가 나노초 정수로 와도 읽는다",
			key:  "event.timestamp", value: anyInt64(1786353300000000000),
			check: func(t *testing.T, c carrier) {
				if c.tsFallback != 1786353300000000000 {
					t.Errorf("tsFallback = %d", c.tsFallback)
				}
			},
		},
		{
			name: "conversation.id 는 Codex 의 session.id 다",
			key:  "conversation.id", value: anyStr("conv_42"),
			check: func(t *testing.T, c carrier) {
				if c.sessionID != "conv_42" {
					t.Errorf("sessionID = %q", c.sessionID)
				}
			},
		},
		{
			name: "bool 속성도 문자열 컬럼에 들어갈 수 있다",
			key:  "type", value: anyBoolValue(true),
			check: func(t *testing.T, c carrier) {
				if c.attr.Type != "true" {
					t.Errorf("type = %q", c.attr.Type)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c carrier
			c.apply(tc.key, tc.value)
			tc.check(t, c)
		})
	}
}

// TestStructuredContentIsSerialized 는 tool_input 이 kvlist 로 오는 벤더에서도
// 세션 조립기가 쓸 형태가 남는지 본다.
func TestStructuredContentIsSerialized(t *testing.T) {
	var c carrier
	c.apply("tool_input", &commonpb.AnyValue{
		Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{
			Values: []*commonpb.KeyValue{kv("file_path", "/tmp/a.go")},
		}},
	})
	body := c.content[ContentToolInput.ordinal()]
	if !body.set || body.body == "" {
		t.Fatal("구조화된 tool_input 이 버려졌다")
	}
	if want := "file_path"; !strings.Contains(body.body, want) {
		t.Errorf("직렬화 결과에 %q 가 없다: %s", want, body.body)
	}
}

func TestAttributeLayeringPrecedence(t *testing.T) {
	base := carrier{}
	base.apply("model", anyStr("resource-model"))
	base.apply("terminal.type", anyStr("iTerm.app"))

	// 데이터포인트가 리소스를 덮어쓰되, 리소스에만 있는 값은 남는다.
	dp := base
	dp.apply("model", anyStr("datapoint-model"))

	if dp.attr.Model != "datapoint-model" {
		t.Errorf("model = %q, want datapoint-model", dp.attr.Model)
	}
	if dp.attr.TerminalType != "iTerm.app" {
		t.Errorf("terminal.type = %q — 리소스 속성이 사라졌다", dp.attr.TerminalType)
	}
	// 복사본이 원본을 오염시키지 않는다.
	if base.attr.Model != "resource-model" {
		t.Errorf("상위 carrier 가 오염됐다: %q", base.attr.Model)
	}
}

func TestScrubUnsupportedKind(t *testing.T) {
	if _, _, err := Scrub(PayloadKind(9), []byte("{}"), EncodingJSON, ScrubPolicy{}); err == nil {
		t.Error("알 수 없는 kind 인데 error 가 없다")
	}
	if _, err := Decode(PayloadKind(9), []byte("{}"), EncodingJSON, testOptions()); err == nil {
		t.Error("알 수 없는 kind 인데 error 가 없다")
	}
}
