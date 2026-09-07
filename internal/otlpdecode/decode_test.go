package otlpdecode

import (
	"testing"
	"unicode/utf8"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"

	"github.com/your-org/pulsemetry/internal/event"
)

// TestFixturesDecodeIdenticallyInBothEncodings 는 계획서 테스트 전략 §3 의 "골든 픽스처를
// protojson·protobuf 양쪽으로 실행" 요구다. 두 경로가 갈리면 로컬 화면과 상위 전달 중
// 한쪽만 조용히 틀린다.
func TestFixturesDecodeIdenticallyInBothEncodings(t *testing.T) {
	tests := []struct {
		fixture string
		kind    PayloadKind
	}{
		{"logs_session_walkthrough.json", PayloadLogs},
		{"logs_broken_records.json", PayloadLogs},
		{"metrics_lines_of_code.json", PayloadMetrics},
		{"metrics_token_usage.json", PayloadMetrics},
		{"metrics_api_request.json", PayloadMetrics},
		{"metrics_temporality_mixed.json", PayloadMetrics},
		{"metrics_broken_points.json", PayloadMetrics},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			// decodeBoth 가 두 경로 결과의 동일성을 직접 단언한다.
			res := decodeBoth(t, tc.kind, tc.fixture, testOptions())
			if len(res.Events) == 0 {
				t.Fatalf("이벤트가 하나도 나오지 않음 — 픽스처나 디코더가 잘못됨")
			}
		})
	}
}

func TestDecodeSessionWalkthrough(t *testing.T) {
	res := decodeBoth(t, PayloadLogs, "logs_session_walkthrough.json", testOptions())

	if got, want := len(res.Events), 6; got != want {
		t.Fatalf("이벤트 수 = %d, want %d", got, want)
	}
	if res.Rejected.Total() != 0 {
		t.Fatalf("거부 건수 = %+v, want 0", res.Rejected)
	}

	projectHash := event.NormalizePath("/Users/jy/dev/projects/soma-376/telemetryctl").Hash

	t.Run("공통 리소스 속성", func(t *testing.T) {
		for _, e := range res.Events {
			if e.Vendor != "claude_code" {
				t.Errorf("%s: vendor = %q, want claude_code", e.Name, e.Vendor)
			}
			if e.InstallationID != "inst_test_0001" {
				t.Errorf("%s: installation_id = %q", e.Name, e.InstallationID)
			}
			if e.SessionID != "5f3a9c21-8e44-4bb0-9d1e-2a7c6b0f1234" {
				t.Errorf("%s: session_id = %q", e.Name, e.SessionID)
			}
			if e.Signal != event.SignalLog {
				t.Errorf("%s: signal = %q, want log", e.Name, e.Signal)
			}
			if e.Attr.ProjectHash != projectHash || e.Attr.ProjectName != "telemetryctl" {
				t.Errorf("%s: project = (%q, %q)", e.Name, e.Attr.ProjectHash, e.Attr.ProjectName)
			}
			if e.Attr.TerminalType != "iTerm.app" || e.Attr.AppVersion != "2.1.4" {
				t.Errorf("%s: terminal/app = (%q, %q)", e.Name, e.Attr.TerminalType, e.Attr.AppVersion)
			}
		}
	})

	t.Run("프롬프트 이벤트", func(t *testing.T) {
		e := eventByID(t, res.Events, "evt_prompt_0001")
		if e.Name != "claude_code.user_prompt" {
			t.Fatalf("name = %q", e.Name)
		}
		if e.TS != 1786353300000000000 {
			t.Errorf("ts = %d", e.TS)
		}
		if e.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Errorf("trace_id = %q — OTLP/JSON 의 hex 표기가 깨졌다", e.TraceID)
		}
		if e.SpanID != "00f067aa0ba902b7" {
			t.Errorf("span_id = %q", e.SpanID)
		}
		if got, ok := e.Measure.PromptLength.Get(); !ok || got != 48 {
			t.Errorf("prompt_length = (%d, %v), want (48, true)", got, ok)
		}

		contents := contentsOf(res, indexOfEventID(res, "evt_prompt_0001"))
		prompt, ok := contents[event.ContentPrompt]
		if !ok {
			t.Fatalf("prompt 원문이 빠졌다")
		}
		if prompt.Truncated {
			t.Errorf("48자 프롬프트가 잘렸다")
		}
		// body("claude_code.user_prompt")보다 prompt 속성이 이겨야 한다.
		if prompt.Body == "claude_code.user_prompt" {
			t.Errorf("body 가 prompt 속성을 덮어썼다")
		}
		if prompt.DedupKey != e.DedupKey() {
			t.Errorf("Content.DedupKey 가 이벤트와 다르다")
		}
	})

	t.Run("Edit tool_result", func(t *testing.T) {
		e := eventByID(t, res.Events, "evt_tool_0002")
		if e.Attr.ToolName != "Edit" || e.Attr.Decision != "accept" || e.Attr.DecisionSource != "config" {
			t.Errorf("attr = %+v", e.Attr)
		}
		if got, ok := e.Measure.Success.Get(); !ok || !got {
			t.Errorf("success = (%v, %v), want (true, true)", got, ok)
		}
		if got, ok := e.Measure.DurationMS.Get(); !ok || got != 214 {
			t.Errorf("duration_ms = (%d, %v)", got, ok)
		}
		contents := contentsOf(res, indexOfEventID(res, "evt_tool_0002"))
		input, ok := contents[event.ContentToolInput]
		if !ok {
			t.Fatalf("tool_input 원문이 빠졌다")
		}
		if got, ok := e.Measure.ToolInputBytes.Get(); !ok || got != int64(len(input.Body)) {
			t.Errorf("tool_input_bytes = (%d, %v), want %d", got, ok, len(input.Body))
		}
	})

	t.Run("api_request 는 eventName 필드로 이름이 온다", func(t *testing.T) {
		e := eventByID(t, res.Events, "evt_api_0003")
		if e.Name != "claude_code.api_request" {
			t.Fatalf("name = %q — LogRecord.event_name 을 읽지 못했다", e.Name)
		}
		if got, ok := e.Measure.CostUSD.Get(); !ok || got != 0.0342 {
			t.Errorf("cost_usd = (%v, %v)", got, ok)
		}
		if got, ok := e.Measure.InputTokens.Get(); !ok || got != 1820 {
			t.Errorf("input_tokens = (%d, %v)", got, ok)
		}
		if got, ok := e.Measure.CacheReadTokens.Get(); !ok || got != 22140 {
			t.Errorf("cache_read_tokens = (%d, %v)", got, ok)
		}
		// 0 과 미설정의 구분이 살아 있어야 한다.
		got, ok := e.Measure.CacheCreationTokens.Get()
		if !ok || got != 0 {
			t.Errorf("cache_creation_tokens = (%d, %v), want (0, true)", got, ok)
		}
		if e.Measure.ResponseLength.Valid() {
			t.Errorf("response_length 가 오지 않았는데 설정됐다")
		}
	})

	t.Run("event.name 속성이 LogRecord eventName보다 우선한다", func(t *testing.T) {
		d := newDecoder(testOptions())
		d.logRecord(carrier{serviceName: "codex_cli_rs"}, &logspb.LogRecord{
			EventName:    "event otel/src/events.rs:1",
			TimeUnixNano: 1,
			Attributes: []*commonpb.KeyValue{
				kv("event.name", "codex.sse_event"), kv("event.kind", "response.completed"), kv("session.id", "s1"),
			},
		})
		res := d.result()
		if len(res.Events) != 1 || res.Events[0].Name != "codex.sse_event" {
			t.Fatalf("events = %+v", res.Events)
		}
	})

	t.Run("api_error", func(t *testing.T) {
		e := eventByID(t, res.Events, "evt_apierr_0005")
		if e.Measure.ErrorType != "rate_limit_error" {
			t.Errorf("error_type = %q", e.Measure.ErrorType)
		}
		if got, ok := e.Measure.StatusCode.Get(); !ok || got != 429 {
			t.Errorf("status_code = (%d, %v)", got, ok)
		}
		if got, ok := e.Measure.Attempt.Get(); !ok || got != 2 {
			t.Errorf("attempt = (%d, %v)", got, ok)
		}
	})

	t.Run("경로가 섞인 error 메시지는 error_type 에 들어가지 않는다", func(t *testing.T) {
		e := eventByID(t, res.Events, "evt_tool_0006")
		if e.Measure.ErrorType != "" {
			t.Errorf("error_type = %q — 자유 형식 메시지가 통과했다", e.Measure.ErrorType)
		}
	})

	t.Run("원문은 Contents 로만 나온다", func(t *testing.T) {
		if got, want := len(res.Contents), 4; got != want {
			t.Fatalf("Content 수 = %d, want %d", got, want)
		}
		kinds := map[event.ContentKind]int{}
		for _, c := range res.Contents {
			kinds[c.Kind]++
		}
		if kinds[event.ContentPrompt] != 1 || kinds[event.ContentToolInput] != 2 || kinds[event.ContentToolResult] != 1 {
			t.Errorf("kind 분포 = %v", kinds)
		}
	})
}

func TestDecodeMetricsFixtures(t *testing.T) {
	t.Run("lines_of_code", func(t *testing.T) {
		res := decodeBoth(t, PayloadMetrics, "metrics_lines_of_code.json", testOptions())
		if len(res.Events) != 2 {
			t.Fatalf("이벤트 수 = %d, want 2", len(res.Events))
		}
		for _, e := range res.Events {
			if e.Signal != event.SignalMetric {
				t.Errorf("signal = %q", e.Signal)
			}
			if e.Temporality != event.TemporalityDelta {
				t.Errorf("temporality = %v", e.Temporality)
			}
			if e.Measure.Unit != "count" {
				t.Errorf("unit = %q", e.Measure.Unit)
			}
			if e.Sequence != 0 {
				t.Errorf("type 이 다른 포인트인데 sequence = %d", e.Sequence)
			}
		}
		added, removed := res.Events[0], res.Events[1]
		if added.Attr.Type != "added" || removed.Attr.Type != "removed" {
			t.Fatalf("type = (%q, %q)", added.Attr.Type, removed.Attr.Type)
		}
		if v, ok := added.Measure.Value.Get(); !ok || v != 42 {
			t.Errorf("added value = (%v, %v)", v, ok)
		}
		if v, ok := removed.Measure.Value.Get(); !ok || v != 17 {
			t.Errorf("removed value = (%v, %v)", v, ok)
		}
	})

	t.Run("token_usage 는 같은 포인트만 sequence 가 갈린다", func(t *testing.T) {
		res := decodeBoth(t, PayloadMetrics, "metrics_token_usage.json", testOptions())
		if len(res.Events) != 5 {
			t.Fatalf("이벤트 수 = %d, want 5", len(res.Events))
		}
		seqByType := map[string][]int{}
		for _, e := range res.Events {
			seqByType[e.Attr.Type] = append(seqByType[e.Attr.Type], e.Sequence)
		}
		// 두 번째 스코프가 첫 input 포인트와 (Name, TS, Attr) 이 완전히 같다.
		if got := seqByType["input"]; len(got) != 2 || got[0] != 0 || got[1] != 1 {
			t.Errorf("input sequence = %v, want [0 1]", got)
		}
		for _, typ := range []string{"output", "cacheRead", "cacheCreation"} {
			if got := seqByType[typ]; len(got) != 1 || got[0] != 0 {
				t.Errorf("%s sequence = %v, want [0]", typ, got)
			}
		}
		// dedup_key 가 서로 달라야 UNIQUE 제약이 정상분을 삼키지 않는다.
		seen := map[string]bool{}
		for _, e := range res.Events {
			key := e.DedupKey()
			if seen[key] {
				t.Fatalf("dedup_key 충돌: %s (type=%s seq=%d)", key, e.Attr.Type, e.Sequence)
			}
			seen[key] = true
		}
	})

	t.Run("api_request", func(t *testing.T) {
		res := decodeBoth(t, PayloadMetrics, "metrics_api_request.json", testOptions())
		if len(res.Events) != 2 {
			t.Fatalf("이벤트 수 = %d, want 2", len(res.Events))
		}
		count, cost := res.Events[0], res.Events[1]
		if count.Name != "claude_code.api_request.count" {
			t.Fatalf("name = %q", count.Name)
		}
		if got, ok := count.Measure.StatusCode.Get(); !ok || got != 200 {
			t.Errorf("status_code = (%d, %v)", got, ok)
		}
		if v, ok := cost.Measure.Value.Get(); !ok || v != 0.0342 {
			t.Errorf("cost value = (%v, %v)", v, ok)
		}
		if cost.Measure.Unit != "USD" {
			t.Errorf("unit = %q", cost.Measure.Unit)
		}
	})
}

// TestTemporalityBranching 은 계획서 제약 §4 다. delta 를 가정하면 cumulative 카운터가
// 매 주기 전체 누적값으로 더해져 비용이 조용히 몇 배가 된다.
func TestTemporalityBranching(t *testing.T) {
	res := decodeBoth(t, PayloadMetrics, "metrics_temporality_mixed.json", testOptions())

	byName := map[string]event.Event{}
	for _, e := range res.Events {
		byName[e.Name] = e
	}

	tests := []struct {
		name string
		want event.Temporality
	}{
		{"claude_code.session.count", event.TemporalityDelta},
		{"claude_code.active_time.total", event.TemporalityCumulative},
	}
	for _, tc := range tests {
		e, ok := byName[tc.name]
		if !ok {
			t.Fatalf("%s 가 폐기됐다", tc.name)
		}
		if e.Temporality != tc.want {
			t.Errorf("%s temporality = %v, want %v", tc.name, e.Temporality, tc.want)
		}
	}

	if _, ok := byName["claude_code.commit.count"]; ok {
		t.Errorf("UNSPECIFIED temporality 메트릭이 살아남았다 — 2배 집계 위험")
	}
	if res.Rejected.UnspecifiedTemporality != 2 {
		t.Errorf("UnspecifiedTemporality = %d, want 2 (데이터포인트 수)", res.Rejected.UnspecifiedTemporality)
	}

	// Sum 이 아닌 타입(gauge 1 + histogram 1)은 폐기하고 카운트만 올린다.
	if _, ok := byName["claude_code.context.window_used"]; ok {
		t.Errorf("gauge 가 이벤트로 들어왔다 — rollup 에 last-value 규칙이 없다")
	}
	if res.Rejected.UnsupportedMetricType != 2 {
		t.Errorf("UnsupportedMetricType = %d, want 2", res.Rejected.UnsupportedMetricType)
	}
	if res.Rejected.Total() != 4 {
		t.Errorf("Rejected.Total() = %d, want 4", res.Rejected.Total())
	}
	if len(res.Events) != 2 {
		t.Errorf("살아남은 이벤트 = %d, want 2", len(res.Events))
	}
}

// TestPartialFailure 는 "깨진 일부 때문에 배치 전체를 버리지 않는다" 를 고정한다.
func TestPartialFailure(t *testing.T) {
	t.Run("logs", func(t *testing.T) {
		res := decodeBoth(t, PayloadLogs, "logs_broken_records.json", testOptions())
		if len(res.Events) != 2 {
			t.Fatalf("정상 이벤트 = %d, want 2", len(res.Events))
		}
		// 이름 없는 레코드 1건 + 타임스탬프 없는 레코드 1건.
		if res.Rejected.LogRecords != 2 {
			t.Errorf("LogRecords 거부 = %d, want 2", res.Rejected.LogRecords)
		}
		if res.Rejected.Datapoints != 0 {
			t.Errorf("Datapoints 거부 = %d, want 0", res.Rejected.Datapoints)
		}
	})

	t.Run("metrics", func(t *testing.T) {
		res := decodeBoth(t, PayloadMetrics, "metrics_broken_points.json", testOptions())
		if len(res.Events) != 1 {
			t.Fatalf("정상 이벤트 = %d, want 1", len(res.Events))
		}
		// 타임스탬프 없는 포인트 1건 + 이름 없는 메트릭의 포인트 1건.
		if res.Rejected.Datapoints != 2 {
			t.Errorf("Datapoints 거부 = %d, want 2", res.Rejected.Datapoints)
		}
	})

	t.Run("페이로드 자체가 깨지면 error", func(t *testing.T) {
		for _, enc := range []Encoding{EncodingJSON, EncodingProtobuf} {
			if _, err := DecodeLogs([]byte("\xff\xfe not otlp at all {"), enc, testOptions()); err == nil {
				t.Errorf("%s: 깨진 페이로드인데 error 가 없다", enc)
			}
		}
	})

	t.Run("installation_id 가 없으면 전부 거부된다", func(t *testing.T) {
		data := loadFixture(t, "logs_session_walkthrough.json")
		res, err := DecodeLogs(data, EncodingJSON, Options{})
		if err != nil {
			t.Fatalf("디코드: %v", err)
		}
		if len(res.Events) != 0 || res.Rejected.LogRecords != 6 {
			t.Errorf("events=%d rejected=%+v — Validate 가 NOT NULL 계약을 못 막았다", len(res.Events), res.Rejected)
		}
	})
}

func TestDecodeTracesIsNoOp(t *testing.T) {
	res, err := Decode(PayloadTraces, []byte("{}"), EncodingJSON, testOptions())
	if err != nil {
		t.Fatalf("traces 디코드: %v", err)
	}
	if len(res.Events) != 0 || len(res.Contents) != 0 || res.Rejected.Total() != 0 {
		t.Errorf("traces 는 정규화 대상이 아닌데 결과가 있다: %+v", res)
	}
}

func TestVendorInference(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		signalName  string
		fallback    string
		want        string
	}{
		{"service.name 이 이긴다", "claude-code", "codex.api_request", "fb", "claude_code"},
		{"Codex Rust 서비스명", "codex_cli_rs", "codex.sse_event", "fb", "codex"},
		{"이름 접두로 추론", "", "claude_code.token.usage", "fb", "claude_code"},
		{"codex 접두", "", "codex.tool_result", "fb", "codex"},
		{"모르는 service.name 은 정규화만", "acme-agent", "x.y", "fb", "acme_agent"},
		{"둘 다 없으면 폴백", "", "unnamed", "fb", "fb"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := vendorOf(tc.serviceName, tc.signalName, tc.fallback); got != tc.want {
				t.Errorf("vendorOf() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCapContent(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		max           int
		wantLen       int
		wantTruncated bool
	}{
		{"캡 이하는 그대로", "hello", 16, 5, false},
		{"캡과 같으면 그대로", "hello", 5, 5, false},
		{"ASCII 절단", "0123456789", 4, 4, true},
		// 한글은 3바이트라 5바이트 캡이면 1글자(3바이트)까지만 남아야 한다.
		{"UTF-8 경계 보존", "가나다", 5, 3, true},
		{"캡이 0 이면 무제한", "가나다", 0, 9, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := capContent(tc.body, tc.max)
			if len(got) != tc.wantLen || truncated != tc.wantTruncated {
				t.Errorf("capContent() = (%q(%d바이트), %v), want (%d바이트, %v)",
					got, len(got), truncated, tc.wantLen, tc.wantTruncated)
			}
			if !utf8.ValidString(got) {
				t.Errorf("잘린 결과가 유효한 UTF-8 이 아니다: %q", got)
			}
		})
	}
}

func TestContentCapAppliedOnDecode(t *testing.T) {
	opt := testOptions()
	opt.MaxContentBytes = 8
	res := decodeBoth(t, PayloadLogs, "logs_session_walkthrough.json", opt)
	if len(res.Contents) == 0 {
		t.Fatal("원문이 하나도 없다")
	}
	for _, c := range res.Contents {
		if len(c.Body) > 8 {
			t.Errorf("%s 원문이 캡(8바이트)을 넘었다: %d바이트", c.Kind, len(c.Body))
		}
		if !c.Truncated {
			t.Errorf("%s 가 잘렸는데 truncated 플래그가 없다", c.Kind)
		}
	}
	// 캡은 저장 원문에만 걸린다. events.tool_input_bytes 는 실제로 온 크기여야 —
	// 캡 이후 길이로 세면 화면의 "툴 입력 크기"가 전부 16KB 에 붙어 무의미해진다.
	e := eventByID(t, res.Events, "evt_tool_0002")
	if got, ok := e.Measure.ToolInputBytes.Get(); !ok || got <= 8 {
		t.Errorf("tool_input_bytes = (%d, %v) — 캡 이전 원본 크기여야 한다", got, ok)
	}
}

func TestSanitizeErrorType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"rate_limit_error", "rate_limit_error"},
		{"ETIMEDOUT", "ETIMEDOUT"},
		{"  overloaded_error  ", "overloaded_error"},
		{"", ""},
		{"ENOENT: open '/Users/jy/x.go'", ""},
		{`C:\Users\jy\x.go`, ""},
		{"a message with spaces", ""},
		{"0123456789012345678901234567890123456789012345678901234567890123456789", ""},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := sanitizeErrorType(tc.in); got != tc.want {
				t.Errorf("sanitizeErrorType(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
