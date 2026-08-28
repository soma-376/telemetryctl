package otlpdecode

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	logscolpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	tracecolpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/event"
)

// defaultPrivacy 는 회사 manifest 의 기본값이다 — §4.6 대로 전부 false.
func defaultPrivacy() contract.Privacy { return contract.Privacy{} }

func allowAllPrivacy() contract.Privacy {
	return contract.Privacy{
		CollectUserPrompts:        true,
		CollectAssistantResponses: true,
		CollectToolDetails:        true,
		CollectToolContent:        true,
		CollectUserEmail:          true,
		CollectRawAPIBodies:       true,
	}
}

func TestPolicyFromPrivacy(t *testing.T) {
	tests := []struct {
		name        string
		privacy     contract.Privacy
		wantDrop    []string
		wantKeep    []string
		wantIsZero  bool
		wantSuffix  string
		checkSuffix bool
	}{
		{
			name:        "기본값(전부 false)은 원문 전부를 지운다",
			privacy:     defaultPrivacy(),
			wantDrop:    []string{"prompt", "assistant_response", "tool_input", "tool_result", "user.email", "raw_request"},
			wantKeep:    []string{"prompt_length", "tool_name", "model", "cost_usd", "session.id", "cwd"},
			wantSuffix:  "user_prompt",
			checkSuffix: true,
		},
		{
			name:       "전부 허용이면 지울 것이 없다",
			privacy:    allowAllPrivacy(),
			wantIsZero: true,
		},
		{
			name:     "프롬프트만 허용",
			privacy:  contract.Privacy{CollectUserPrompts: true},
			wantDrop: []string{"tool_input", "assistant_response"},
			wantKeep: []string{"prompt", "user_prompt"},
		},
		{
			name:     "tool details 만 허용",
			privacy:  contract.Privacy{CollectToolDetails: true},
			wantDrop: []string{"prompt", "tool_result"},
			wantKeep: []string{"tool_input", "command", "file_path"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := PolicyFromPrivacy(tc.privacy)
			if policy.IsZero() != tc.wantIsZero {
				t.Fatalf("IsZero() = %v, want %v", policy.IsZero(), tc.wantIsZero)
			}
			for _, key := range tc.wantDrop {
				if !policy.DropAttributes[key] {
					t.Errorf("%q 가 제거 대상이 아니다", key)
				}
			}
			for _, key := range tc.wantKeep {
				if policy.DropAttributes[key] {
					t.Errorf("%q 는 보존해야 하는데 제거 대상이다", key)
				}
			}
			if tc.checkSuffix && !matchesSuffix("claude_code."+tc.wantSuffix, policy.ClearBodySuffixes) {
				t.Errorf("body 제거 접미사에 %q 가 없다", tc.wantSuffix)
			}
		})
	}
}

// TestScrubRemovesContentAndPreservesTheRest 는 재인코딩의 핵심 계약이다.
func TestScrubRemovesContentAndPreservesTheRest(t *testing.T) {
	for _, enc := range []Encoding{EncodingJSON, EncodingProtobuf} {
		t.Run(enc.String(), func(t *testing.T) {
			raw := payloadIn(t, PayloadLogs, "logs_session_walkthrough.json", enc)
			before, err := DecodeLogs(raw, enc, testOptions())
			if err != nil {
				t.Fatalf("원본 디코드: %v", err)
			}

			out, stats, err := Scrub(PayloadLogs, raw, enc, PolicyFromPrivacy(defaultPrivacy()))
			if err != nil {
				t.Fatalf("Scrub: %v", err)
			}
			// prompt 1 + tool_input 2 + tool_result 1 + user.email 1
			if stats.AttributesRemoved != 5 {
				t.Errorf("AttributesRemoved = %d, want 5", stats.AttributesRemoved)
			}
			// body 가 있는 user_prompt 레코드 1건.
			if stats.BodiesCleared != 1 {
				t.Errorf("BodiesCleared = %d, want 1", stats.BodiesCleared)
			}

			after, err := DecodeLogs(out, enc, testOptions())
			if err != nil {
				t.Fatalf("전달 바이트 디코드: %v", err)
			}
			if len(after.Events) != len(before.Events) {
				t.Fatalf("이벤트 수가 변했다: %d → %d", len(before.Events), len(after.Events))
			}
			if len(after.Contents) != 0 {
				t.Fatalf("원문이 %d건 남았다", len(after.Contents))
			}

			// 원문이 아닌 것은 전부 그대로여야 한다.
			for i := range before.Events {
				b, a := before.Events[i], after.Events[i]
				if b.Name != a.Name || b.TS != a.TS || b.SessionID != a.SessionID {
					t.Errorf("[%d] 식별 필드가 변했다: %+v → %+v", i, b, a)
				}
				// user.email 은 denylist 라 Scrub 이 실제로 지운다. 그것 말고는 그대로여야 한다.
				// 지웠다는 사실 자체는 아래에서 따로 단언한다 — 비교에서 빼기만 하면
				// 지우지 않게 됐을 때도 조용히 통과한다.
				if a.Attr.UserEmail != "" {
					t.Errorf("[%d] %s: 전달 바이트에 user.email 이 남았다: %q", i, b.Name, a.Attr.UserEmail)
				}
				bAttr := b.Attr
				bAttr.UserEmail = ""
				if bAttr != a.Attr {
					t.Errorf("[%d] %s: 속성이 변했다\nbefore=%+v\nafter =%+v", i, b.Name, bAttr, a.Attr)
				}
				if b.Measure.CostUSD != a.Measure.CostUSD || b.Measure.DurationMS != a.Measure.DurationMS {
					t.Errorf("[%d] %s: 수치가 변했다", i, b.Name)
				}
				if b.Measure.PromptLength != a.Measure.PromptLength && b.Name == "claude_code.api_request" {
					t.Errorf("[%d] prompt_length 가 제거됐다 — 길이는 원문이 아니다", i)
				}
			}
			// prompt_length 는 살아남아야 한다 (원문이 아니라 길이다).
			e := eventByID(t, after.Events, "evt_prompt_0001")
			if got, ok := e.Measure.PromptLength.Get(); !ok || got != 48 {
				t.Errorf("prompt_length = (%d, %v) — 길이까지 지웠다", got, ok)
			}
		})
	}
}

// TestScrubHonorsManifest 는 무엇을 지울지가 하드코딩이 아니라 manifest 에서 온다는 것이다.
func TestScrubHonorsManifest(t *testing.T) {
	raw := loadFixture(t, "logs_session_walkthrough.json")

	tests := []struct {
		name        string
		privacy     contract.Privacy
		wantKinds   []event.ContentKind
		wantAbsent  []event.ContentKind
		wantRemoved int
	}{
		{
			name:       "전부 금지",
			privacy:    defaultPrivacy(),
			wantAbsent: []event.ContentKind{event.ContentPrompt, event.ContentToolInput, event.ContentToolResult},
		},
		{
			name:       "프롬프트만 허용",
			privacy:    contract.Privacy{CollectUserPrompts: true},
			wantKinds:  []event.ContentKind{event.ContentPrompt},
			wantAbsent: []event.ContentKind{event.ContentToolInput, event.ContentToolResult},
		},
		{
			name:       "tool details 와 tool content 허용",
			privacy:    contract.Privacy{CollectToolDetails: true, CollectToolContent: true},
			wantKinds:  []event.ContentKind{event.ContentToolInput, event.ContentToolResult},
			wantAbsent: []event.ContentKind{event.ContentPrompt},
		},
		{
			name:      "전부 허용이면 아무것도 안 지운다",
			privacy:   allowAllPrivacy(),
			wantKinds: []event.ContentKind{event.ContentPrompt, event.ContentToolInput, event.ContentToolResult},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := Scrub(PayloadLogs, raw, EncodingJSON, PolicyFromPrivacy(tc.privacy))
			if err != nil {
				t.Fatalf("Scrub: %v", err)
			}
			res, err := DecodeLogs(out, EncodingJSON, testOptions())
			if err != nil {
				t.Fatalf("디코드: %v", err)
			}
			present := map[event.ContentKind]bool{}
			for _, c := range res.Contents {
				present[c.Kind] = true
			}
			for _, kind := range tc.wantKinds {
				if !present[kind] {
					t.Errorf("%s 가 사라졌다 — manifest 는 허용했다", kind)
				}
			}
			for _, kind := range tc.wantAbsent {
				if present[kind] {
					t.Errorf("%s 가 남았다 — manifest 는 금지했다", kind)
				}
			}
		})
	}
}

// TestScrubPreservesEncoding 은 "protobuf 로 받았으면 protobuf 로" 계약이다.
func TestScrubPreservesEncoding(t *testing.T) {
	policy := PolicyFromPrivacy(defaultPrivacy())

	t.Run("json 은 json 으로", func(t *testing.T) {
		raw := payloadIn(t, PayloadLogs, "logs_session_walkthrough.json", EncodingJSON)
		out, _, err := Scrub(PayloadLogs, raw, EncodingJSON, policy)
		if err != nil {
			t.Fatalf("Scrub: %v", err)
		}
		var probe map[string]any
		if err := json.Unmarshal(out, &probe); err != nil {
			t.Fatalf("출력이 JSON 이 아니다: %v", err)
		}
		if _, ok := probe["resourceLogs"]; !ok {
			t.Fatalf("OTLP/JSON 최상위 키가 없다: %v", probe)
		}
	})

	t.Run("protobuf 는 protobuf 로", func(t *testing.T) {
		raw := payloadIn(t, PayloadLogs, "logs_session_walkthrough.json", EncodingProtobuf)
		out, _, err := Scrub(PayloadLogs, raw, EncodingProtobuf, policy)
		if err != nil {
			t.Fatalf("Scrub: %v", err)
		}
		var probe map[string]any
		if json.Unmarshal(out, &probe) == nil {
			t.Fatalf("출력이 JSON 으로 파싱된다 — 인코딩이 바뀌었다")
		}
		var req logscolpb.ExportLogsServiceRequest
		if err := proto.Unmarshal(out, &req); err != nil {
			t.Fatalf("출력이 protobuf 가 아니다: %v", err)
		}
		if len(req.GetResourceLogs()) != 1 {
			t.Fatalf("resourceLogs = %d", len(req.GetResourceLogs()))
		}
	})

	t.Run("json 의 trace_id 는 hex 표기를 유지한다", func(t *testing.T) {
		raw := payloadIn(t, PayloadLogs, "logs_session_walkthrough.json", EncodingJSON)
		out, _, err := Scrub(PayloadLogs, raw, EncodingJSON, policy)
		if err != nil {
			t.Fatalf("Scrub: %v", err)
		}
		// base64 로 나가면 상위 Collector 가 24바이트짜리 잘못된 ID 를 받는다.
		if !strings.Contains(string(out), "4bf92f3577b34da6a3ce929d0e0e4736") {
			t.Errorf("trace_id 가 hex 로 남지 않았다: %s", truncateForLog(string(out)))
		}
	})
}

// TestScrubKeepsUnknownAttributes 는 denylist 라는 점을 고정한다.
// 상위 Collector 는 우리가 모르는 속성도 받아야 한다.
func TestScrubKeepsUnknownAttributes(t *testing.T) {
	req := &logscolpb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
				kv("service.name", "claude-code"),
				kv("acme.internal.squad", "platform"),
				kv("user.email", fixtureUserEmail),
			}},
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{{
					TimeUnixNano: 1786353300000000000,
					EventName:    "claude_code.user_prompt",
					Attributes: []*commonpb.KeyValue{
						kv("prompt", "지워져야 한다"),
						kv("future_field_we_do_not_know", "보존돼야 한다"),
					},
				}},
			}},
		}},
	}
	raw, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	out, stats, err := Scrub(PayloadLogs, raw, EncodingProtobuf, PolicyFromPrivacy(defaultPrivacy()))
	if err != nil {
		t.Fatalf("Scrub: %v", err)
	}
	if stats.AttributesRemoved != 2 {
		t.Errorf("AttributesRemoved = %d, want 2 (prompt, user.email)", stats.AttributesRemoved)
	}

	var got logscolpb.ExportLogsServiceRequest
	if err := proto.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	res := got.GetResourceLogs()[0]
	if v := attrString(res.GetResource().GetAttributes(), "acme.internal.squad"); v != "platform" {
		t.Errorf("모르는 리소스 속성이 사라졌다: %q", v)
	}
	rec := res.GetScopeLogs()[0].GetLogRecords()[0]
	if v := attrString(rec.GetAttributes(), "future_field_we_do_not_know"); v != "보존돼야 한다" {
		t.Errorf("모르는 레코드 속성이 사라졌다: %q", v)
	}
	if v := attrString(rec.GetAttributes(), "prompt"); v != "" {
		t.Errorf("prompt 가 남았다: %q", v)
	}
}

func TestScrubClearsLogBodyCarryingContent(t *testing.T) {
	req := &logscolpb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{
					{
						TimeUnixNano: 1786353300000000000,
						EventName:    "codex.user_prompt",
						Body:         anyStr("본문에 실려 온 프롬프트"),
					},
					{
						TimeUnixNano: 1786353301000000000,
						EventName:    "codex.api_request",
						Body:         anyStr("본문이지만 원문 이벤트가 아니다"),
					},
				},
			}},
		}},
	}
	raw, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, stats, err := Scrub(PayloadLogs, raw, EncodingProtobuf, PolicyFromPrivacy(defaultPrivacy()))
	if err != nil {
		t.Fatalf("Scrub: %v", err)
	}
	if stats.BodiesCleared != 1 {
		t.Fatalf("BodiesCleared = %d, want 1", stats.BodiesCleared)
	}
	var got logscolpb.ExportLogsServiceRequest
	if err := proto.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	records := got.GetResourceLogs()[0].GetScopeLogs()[0].GetLogRecords()
	if records[0].GetBody() != nil {
		t.Errorf("user_prompt body 가 남았다: %v", records[0].GetBody())
	}
	if anyString(records[1].GetBody()) != "본문이지만 원문 이벤트가 아니다" {
		t.Errorf("관계없는 레코드의 body 를 지웠다")
	}
}

func TestScrubTraces(t *testing.T) {
	req := &tracecolpb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					Name:       "tool.execute",
					Attributes: []*commonpb.KeyValue{kv("tool_input", "지워짐"), kv("tool_name", "Edit")},
					Events: []*tracepb.Span_Event{{
						Name:       "result",
						Attributes: []*commonpb.KeyValue{kv("tool_result", "지워짐"), kv("success", "true")},
					}},
				}},
			}},
		}},
	}
	raw, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, stats, err := Scrub(PayloadTraces, raw, EncodingProtobuf, PolicyFromPrivacy(defaultPrivacy()))
	if err != nil {
		t.Fatalf("Scrub: %v", err)
	}
	if stats.AttributesRemoved != 2 {
		t.Fatalf("AttributesRemoved = %d, want 2", stats.AttributesRemoved)
	}
	var got tracecolpb.ExportTraceServiceRequest
	if err := proto.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	span := got.GetResourceSpans()[0].GetScopeSpans()[0].GetSpans()[0]
	if attrString(span.GetAttributes(), "tool_name") != "Edit" {
		t.Errorf("tool_name 이 사라졌다")
	}
	if attrString(span.GetEvents()[0].GetAttributes(), "success") != "true" {
		t.Errorf("success 가 사라졌다")
	}
}

func TestScrubMetricsKeepsDataPoints(t *testing.T) {
	for _, enc := range []Encoding{EncodingJSON, EncodingProtobuf} {
		t.Run(enc.String(), func(t *testing.T) {
			raw := payloadIn(t, PayloadMetrics, "metrics_token_usage.json", enc)
			out, _, err := Scrub(PayloadMetrics, raw, enc, PolicyFromPrivacy(defaultPrivacy()))
			if err != nil {
				t.Fatalf("Scrub: %v", err)
			}
			before, err := DecodeMetrics(raw, enc, testOptions())
			if err != nil {
				t.Fatalf("원본 디코드: %v", err)
			}
			after, err := DecodeMetrics(out, enc, testOptions())
			if err != nil {
				t.Fatalf("전달 바이트 디코드: %v", err)
			}
			if len(after.Events) != len(before.Events) {
				t.Fatalf("데이터포인트 수가 변했다: %d → %d", len(before.Events), len(after.Events))
			}
			for i := range before.Events {
				if before.Events[i].Measure.Value != after.Events[i].Measure.Value {
					t.Errorf("[%d] 값이 변했다", i)
				}
				if before.Events[i].Temporality != after.Events[i].Temporality {
					t.Errorf("[%d] temporality 가 변했다", i)
				}
			}
		})
	}
}

func kv(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: anyStr(value)}
}

func anyStr(v string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}
}

func truncateForLog(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
