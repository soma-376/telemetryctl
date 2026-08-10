package event

import (
	"regexp"
	"strings"
	"testing"
)

// baseEvent 는 모든 dedup 테스트가 변형의 출발점으로 쓰는 이벤트다.
// 필드를 하나 늘리면 이 함수와 mutators 양쪽을 같이 늘려야 테스트가 계속 의미를 갖는다.
func baseEvent() Event {
	return Event{
		Vendor:         "claude_code",
		InstallationID: "inst-1",
		Signal:         SignalMetric,
		Name:           "claude_code.token.usage",
		TS:             1_754_800_000_000_000_000,
		SessionID:      "sess-1",
		EventID:        "evt-1",
		TraceID:        "trace-1",
		SpanID:         "span-1",
		Sequence:       0,
		Temporality:    TemporalityDelta,
		Attr: Attributes{
			Model:          "claude-opus-5",
			Type:           "input",
			ToolName:       "Edit",
			Decision:       "accept",
			DecisionSource: "config",
			Language:       "go",
			QuerySource:    "user",
			Speed:          "fast",
			Effort:         "high",
			AgentName:      "explore",
			SkillName:      "go-review",
			PluginName:     "ecc",
			MCPServer:      "github",
			MCPTool:        "search",
			StartType:      "cli",
			TerminalType:   "iterm",
			AppVersion:     "1.2.3",
			Entrypoint:     "claude",
			Environment:    "prod",
			ProjectHash:    strings.Repeat("a", 64),
			ProjectName:    "telemetryctl",
		},
		Measure: Measures{
			Value:       Some(42.0),
			InputTokens: Some(int64(100)),
			Success:     Some(true),
		},
	}
}

func TestDedupKeyIsDeterministic(t *testing.T) {
	first := baseEvent().DedupKey()
	for i := range 100 {
		// 같은 값을 매번 새로 조립해도(구조체 리터럴 순서·복사 경로와 무관하게) 키가 같아야 한다.
		if got := baseEvent().DedupKey(); got != first {
			t.Fatalf("반복 %d: 키가 흔들림 got=%s want=%s", i, got, first)
		}
	}
	copied := baseEvent()
	if got := copied.DedupKey(); got != first {
		t.Fatalf("복사본의 키가 다름 got=%s want=%s", got, first)
	}
}

func TestDedupKeyFormat(t *testing.T) {
	key := baseEvent().DedupKey()
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(key) {
		t.Fatalf("키가 sha256 hex 가 아님: %q", key)
	}
}

// 키는 되돌릴 수 없어야 한다. 민감할 수 있는 입력이 그대로 실리지 않는지 확인한다.
func TestDedupKeyContainsNoPlaintext(t *testing.T) {
	e := baseEvent()
	e.Attr.ProjectName = "secret-project"
	e.SessionID = "sess-secret"
	key := e.DedupKey()

	for _, plain := range []string{
		"secret-project", "sess-secret", "claude_code",
		"claude_code.token.usage", "evt-1", "claude-opus-5", "1754800000000000000",
	} {
		if strings.Contains(key, plain) {
			t.Errorf("키에 원문 %q 가 그대로 들어감: %s", plain, key)
		}
	}
}

func TestDedupKeyDistinguishesFields(t *testing.T) {
	// §5.5 목록 + 확장분. 각 항목이 키에 실제로 기여하는지 하나씩 확인한다.
	// 새 필드를 dedupFields 에 넣는 것을 잊으면 여기서 잡힌다.
	mutators := map[string]func(*Event){
		"vendor":          func(e *Event) { e.Vendor = "codex" },
		"installation_id": func(e *Event) { e.InstallationID = "inst-2" },
		"signal":          func(e *Event) { e.Signal = SignalLog },
		"name":            func(e *Event) { e.Name = "claude_code.cost.usage" },
		"event_id":        func(e *Event) { e.EventID = "evt-2" },
		"trace_id":        func(e *Event) { e.TraceID = "trace-2" },
		"span_id":         func(e *Event) { e.SpanID = "span-2" },
		"session_id":      func(e *Event) { e.SessionID = "sess-2" },
		"ts":              func(e *Event) { e.TS++ },
		"sequence":        func(e *Event) { e.Sequence = 1 },
		"model":           func(e *Event) { e.Attr.Model = "x" },
		"type":            func(e *Event) { e.Attr.Type = "x" },
		"tool_name":       func(e *Event) { e.Attr.ToolName = "x" },
		"decision":        func(e *Event) { e.Attr.Decision = "x" },
		"decision_source": func(e *Event) { e.Attr.DecisionSource = "x" },
		"language":        func(e *Event) { e.Attr.Language = "x" },
		"query_source":    func(e *Event) { e.Attr.QuerySource = "x" },
		"speed":           func(e *Event) { e.Attr.Speed = "x" },
		"effort":          func(e *Event) { e.Attr.Effort = "x" },
		"agent_name":      func(e *Event) { e.Attr.AgentName = "x" },
		"skill_name":      func(e *Event) { e.Attr.SkillName = "x" },
		"plugin_name":     func(e *Event) { e.Attr.PluginName = "x" },
		"mcp_server":      func(e *Event) { e.Attr.MCPServer = "x" },
		"mcp_tool":        func(e *Event) { e.Attr.MCPTool = "x" },
		"start_type":      func(e *Event) { e.Attr.StartType = "x" },
		"terminal_type":   func(e *Event) { e.Attr.TerminalType = "x" },
		"app_version":     func(e *Event) { e.Attr.AppVersion = "x" },
		"entrypoint":      func(e *Event) { e.Attr.Entrypoint = "x" },
		"environment":     func(e *Event) { e.Attr.Environment = "x" },
		"project_hash":    func(e *Event) { e.Attr.ProjectHash = strings.Repeat("b", 64) },
		"project_name":    func(e *Event) { e.Attr.ProjectName = "other" },
	}

	base := baseEvent().DedupKey()
	seen := map[string]string{base: "(base)"}
	for name, mutate := range mutators {
		e := baseEvent()
		mutate(&e)
		key := e.DedupKey()
		if key == base {
			t.Errorf("%s 를 바꿔도 키가 같음 — dedupFields 누락 의심", name)
			continue
		}
		if prev, dup := seen[key]; dup {
			t.Errorf("%s 와 %s 의 키가 충돌함: %s", name, prev, key)
			continue
		}
		seen[key] = name
	}
}

// 메트릭에는 event_id 가 없다. 그때도 서로 다른 데이터포인트가 구분돼야 한다.
func TestDedupKeyDistinguishesMetricDataPointsWithoutEventID(t *testing.T) {
	point := func(typ string, seq int) Event {
		return Event{
			Vendor: "claude_code", InstallationID: "inst-1",
			Signal: SignalMetric, Name: "claude_code.token.usage",
			TS: 1_754_800_000_000_000_000, SessionID: "sess-1", Sequence: seq,
			Attr: Attributes{Model: "claude-opus-5", Type: typ},
		}
	}
	input, output := point("input", 0), point("output", 1)
	if input.DedupKey() == output.DedupKey() {
		t.Fatal("같은 시각의 input/output 데이터포인트가 같은 키를 가짐 — 한쪽이 UNIQUE 로 버려진다")
	}
	// 속성만으로도 갈려야 한다. 상위가 배치를 다르게 잘라 Sequence 가 흔들려도 살아남는 성질이다.
	sameSeq := point("output", 0)
	if input.DedupKey() == sameSeq.DedupKey() {
		t.Fatal("Sequence 가 같으면 속성이 달라도 같은 키가 됨")
	}
}

// 재전송된 같은 이벤트는 값이 같다. 수치를 키에 넣지 않기로 한 결정을 고정한다.
func TestDedupKeyIgnoresMeasuresAndTemporality(t *testing.T) {
	base := baseEvent()
	other := baseEvent()
	other.Measure = Measures{
		Value:      Some(1.0),
		CostUSD:    Some(0.5),
		DurationMS: Some(int64(12)),
		Success:    Some(false),
		ErrorType:  "timeout",
	}
	other.Temporality = TemporalityCumulative
	if base.DedupKey() != other.DedupKey() {
		t.Fatal("수치·temporality 가 키에 섞여 들어감")
	}
}

// 길이 접두가 없으면 인접한 두 필드 사이에서 글자가 옮겨가도 같은 키가 나온다.
func TestDedupKeyFieldBoundariesAreUnambiguous(t *testing.T) {
	left := baseEvent()
	left.Vendor, left.InstallationID = "a", "bc"
	right := baseEvent()
	right.Vendor, right.InstallationID = "ab", "c"
	if left.DedupKey() == right.DedupKey() {
		t.Fatal("필드 경계가 모호함: (a,bc) 와 (ab,c) 가 같은 키")
	}

	// 값에 구분자를 넣어도 마찬가지여야 한다.
	sep := baseEvent()
	sep.Vendor, sep.InstallationID = "a:1", "b"
	sep2 := baseEvent()
	sep2.Vendor, sep2.InstallationID = "a", "1:b"
	if sep.DedupKey() == sep2.DedupKey() {
		t.Fatal("값에 섞인 구분자가 필드 경계를 무너뜨림")
	}
}

func TestDedupKeyEmptyEventIsStable(t *testing.T) {
	// 유효하지 않은 이벤트라도 키 계산이 패닉하거나 흔들리면 안 된다 —
	// 저장 경로가 Validate 보다 먼저 키를 찍는 경우가 생길 수 있다.
	if (Event{}).DedupKey() != (Event{}).DedupKey() {
		t.Fatal("제로값 이벤트의 키가 흔들림")
	}
}
