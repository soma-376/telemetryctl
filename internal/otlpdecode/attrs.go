package otlpdecode

import (
	"strconv"
	"strings"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/your-org/pulsemetry/internal/event"
)

// 속성 allowlist 는 이 파일의 네 테이블이 전부다. 새 속성을 받으려면 여기에 한 줄을 넣고
// event.Attributes/Measures 와 events 스키마에 컬럼을 같이 늘린다. 테이블에 없는 키는
// 조용히 버려진다 — user.email·user.id·user.account_uuid·organization.id 가 여기 없는 것이
// 곧 그 값들이 저장될 수 없다는 보장이다 (ADR 0003).
//
// 벤더가 두 종류라 같은 의미의 키가 여러 표기로 온다(Claude Code 는 snake_case 속성,
// OTel 시맨틱 컨벤션은 점 표기). 별칭을 한 필드로 모으고, 한 데이터포인트에 별칭이 둘 이상
// 있으면 나중에 나온 것이 이긴다.

// stringAttrs 는 event.Attributes 의 문자열 컬럼으로 가는 속성이다.
var stringAttrs = map[string]func(*event.Attributes, string){
	"model":                func(a *event.Attributes, v string) { a.Model = v },
	"gen_ai.request.model": func(a *event.Attributes, v string) { a.Model = v },

	"type": func(a *event.Attributes, v string) { a.Type = v },

	"tool_name": func(a *event.Attributes, v string) { a.ToolName = v },
	"tool.name": func(a *event.Attributes, v string) { a.ToolName = v },
	// claude_code.code_edit_tool.decision 은 툴 이름을 tool 로 보낸다.
	"tool": func(a *event.Attributes, v string) { a.ToolName = v },

	"decision": func(a *event.Attributes, v string) { a.Decision = v },
	// tool_decision 이벤트의 source 는 결정의 출처(config·user_permanent 등)다.
	"source":          func(a *event.Attributes, v string) { a.DecisionSource = v },
	"decision_source": func(a *event.Attributes, v string) { a.DecisionSource = v },

	"language":     func(a *event.Attributes, v string) { a.Language = v },
	"query_source": func(a *event.Attributes, v string) { a.QuerySource = v },
	"speed":        func(a *event.Attributes, v string) { a.Speed = v },

	"effort":           func(a *event.Attributes, v string) { a.Effort = v },
	"reasoning_effort": func(a *event.Attributes, v string) { a.Effort = v },

	"agent_name":    func(a *event.Attributes, v string) { a.AgentName = v },
	"agent.name":    func(a *event.Attributes, v string) { a.AgentName = v },
	"subagent_type": func(a *event.Attributes, v string) { a.AgentName = v },

	"skill_name":  func(a *event.Attributes, v string) { a.SkillName = v },
	"skill.name":  func(a *event.Attributes, v string) { a.SkillName = v },
	"plugin_name": func(a *event.Attributes, v string) { a.PluginName = v },
	"plugin.name": func(a *event.Attributes, v string) { a.PluginName = v },

	"mcp_server":      func(a *event.Attributes, v string) { a.MCPServer = v },
	"mcp.server.name": func(a *event.Attributes, v string) { a.MCPServer = v },
	"mcp_tool":        func(a *event.Attributes, v string) { a.MCPTool = v },
	"mcp.tool.name":   func(a *event.Attributes, v string) { a.MCPTool = v },

	"start_type":    func(a *event.Attributes, v string) { a.StartType = v },
	"terminal.type": func(a *event.Attributes, v string) { a.TerminalType = v },
	"terminal_type": func(a *event.Attributes, v string) { a.TerminalType = v },

	"app.version":     func(a *event.Attributes, v string) { a.AppVersion = v },
	"app_version":     func(a *event.Attributes, v string) { a.AppVersion = v },
	"service.version": func(a *event.Attributes, v string) { a.AppVersion = v },

	"entrypoint":                  func(a *event.Attributes, v string) { a.Entrypoint = v },
	"environment":                 func(a *event.Attributes, v string) { a.Environment = v },
	"deployment.environment":      func(a *event.Attributes, v string) { a.Environment = v },
	"deployment.environment.name": func(a *event.Attributes, v string) { a.Environment = v },
}

// pathAttrs 는 작업 디렉터리 경로를 담고 오는 속성이다. 값은 event.NormalizePath 를 거쳐
// project_hash + project_name 으로만 들어가고 원본 문자열은 어디에도 남지 않는다.
// 벤더가 이 중 무엇을 쓰는지 확정되지 않아 후보를 넓게 잡는다 — 잘못 잡아도 해시일 뿐이다.
var pathAttrs = map[string]struct{}{
	"cwd":            {},
	"workspace.path": {},
	"workspace_dir":  {},
	"project.path":   {},
	"project_path":   {},
}

// intMeasures 는 정수 수치 컬럼이다. 값이 double·string 으로 와도 정수로 맞춰 받는다 —
// OTLP/JSON 왕복에서 int64 가 문자열이 되는 것이 정상이고, 벤더가 실수로 double 을 넣기도 한다.
var intMeasures = map[string]func(*event.Measures, int64){
	"input_tokens":          func(m *event.Measures, v int64) { m.InputTokens = event.Some(v) },
	"output_tokens":         func(m *event.Measures, v int64) { m.OutputTokens = event.Some(v) },
	"cache_read_tokens":     func(m *event.Measures, v int64) { m.CacheReadTokens = event.Some(v) },
	"cache_creation_tokens": func(m *event.Measures, v int64) { m.CacheCreationTokens = event.Some(v) },

	"duration_ms":               func(m *event.Measures, v int64) { m.DurationMS = event.Some(v) },
	"status_code":               func(m *event.Measures, v int64) { m.StatusCode = event.Some(v) },
	"http.response.status_code": func(m *event.Measures, v int64) { m.StatusCode = event.Some(v) },
	"attempt":                   func(m *event.Measures, v int64) { m.Attempt = event.Some(v) },
	"attempt_number":            func(m *event.Measures, v int64) { m.Attempt = event.Some(v) },

	"prompt_length":     func(m *event.Measures, v int64) { m.PromptLength = event.Some(v) },
	"response_length":   func(m *event.Measures, v int64) { m.ResponseLength = event.Some(v) },
	"tool_input_bytes":  func(m *event.Measures, v int64) { m.ToolInputBytes = event.Some(v) },
	"tool_result_bytes": func(m *event.Measures, v int64) { m.ToolResultBytes = event.Some(v) },
}

var floatMeasures = map[string]func(*event.Measures, float64){
	"cost_usd": func(m *event.Measures, v float64) { m.CostUSD = event.Some(v) },
}

var boolMeasures = map[string]func(*event.Measures, bool){
	"success": func(m *event.Measures, v bool) { m.Success = event.Some(v) },
}

// errorTypeAttrs 는 events.error_type 으로 가는 속성이다. 값은 sanitizeErrorType 을 통과해야 한다.
var errorTypeAttrs = map[string]struct{}{
	"error":      {},
	"error.type": {},
	"error_type": {},
}

// maxErrorTypeLen 은 error_type 으로 받아들일 최대 길이다. 이보다 길면 타입이 아니라 문장이다.
const maxErrorTypeLen = 64

// sanitizeErrorType 은 error_type 컬럼에 넣어도 되는 값만 통과시킨다.
//
// 컬럼 이름이 error_type 인데 Claude Code 의 error 속성은 자유 형식 메시지라 경로·명령을
// 그대로 품고 온다("Edit failed: /Users/jy/... not found"). 그것을 events 에 넣으면
// 전체 경로 부재라는 이 패키지의 유일한 강한 보장이 무너진다. 그래서 구분자·공백·길이로
// "타입처럼 생긴 값"만 받고 나머지는 버린다. 메시지 전문이 필요하면 tool_result 원문에 있다.
func sanitizeErrorType(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > maxErrorTypeLen {
		return ""
	}
	if strings.ContainsAny(v, "/\\ \t\n\r\"'") {
		return ""
	}
	return v
}

// contentAttrs 는 원문을 담고 오는 속성이다. 값은 Event 가 아니라 event.Content 로 빠지고
// events 에는 길이·바이트 수만 남는다.
var contentAttrs = map[string]event.ContentKind{
	"prompt":             event.ContentPrompt,
	"user_prompt":        event.ContentPrompt,
	"response":           event.ContentResponse,
	"assistant_response": event.ContentResponse,
	"tool_input":         event.ContentToolInput,
	"tool_parameters":    event.ContentToolInput,
	"tool_arguments":     event.ContentToolInput,
	"tool_result":        event.ContentToolResult,
	"tool_output":        event.ContentToolResult,
	"tool_response":      event.ContentToolResult,
}

// carrier 는 리소스→스코프→데이터포인트 순으로 겹쳐 쌓는 조립 중 상태다.
// 전부 값 필드라 대입만으로 안전하게 복사된다 — 상위 계층 carrier 를 여러 데이터포인트가
// 나눠 써도 서로 오염되지 않는다.
type carrier struct {
	attr    event.Attributes
	measure event.Measures

	sessionID   string
	eventID     string
	eventName   string
	serviceName string
	tsFallback  event.UnixNano

	content [contentKindCount]rawContent

	// target 은 tool_input 에서 뽑아 정규화한 대상 파일이다. 원본 경로 문자열은 여기 남지
	// 않는다 — carrier 는 여러 데이터포인트에 복사되므로 전체 경로가 여기 실리면 그 복사본
	// 전부가 경로를 들고 다니게 된다.
	target event.Path
}

type rawContent struct {
	body string
	set  bool
}

func (c *carrier) applyAll(kvs []*commonpb.KeyValue) {
	for _, kv := range kvs {
		if kv == nil {
			continue
		}
		c.apply(kv.GetKey(), kv.GetValue())
	}
}

// apply 는 속성 하나를 allowlist 에 비춰 반영한다. 어느 테이블에도 없으면 아무 일도 하지 않는다.
func (c *carrier) apply(key string, v *commonpb.AnyValue) {
	if v == nil {
		return
	}
	switch key {
	case "session.id", "session_id", "conversation.id", "conversation_id":
		if s := anyString(v); s != "" {
			c.sessionID = s
		}
		return
	case "event.id", "event_id":
		if s := anyString(v); s != "" {
			c.eventID = s
		}
		return
	case "event.name", "event_name":
		if s := anyString(v); s != "" {
			c.eventName = s
		}
		return
	case "event.timestamp", "event_timestamp":
		if ts, ok := anyTimestamp(v); ok {
			c.tsFallback = ts
		}
		return
	case "service.name":
		if s := anyString(v); s != "" {
			c.serviceName = s
		}
		return
	}

	if set, ok := stringAttrs[key]; ok {
		if s := anyString(v); s != "" {
			set(&c.attr, s)
		}
		return
	}
	if _, ok := pathAttrs[key]; ok {
		if s := anyString(v); s != "" {
			c.attr = c.attr.WithProject(event.NormalizePath(s))
		}
		return
	}
	if _, ok := errorTypeAttrs[key]; ok {
		if s := sanitizeErrorType(anyString(v)); s != "" {
			c.measure.ErrorType = s
		}
		return
	}
	if set, ok := intMeasures[key]; ok {
		if n, ok := anyInt(v); ok {
			set(&c.measure, n)
		}
		return
	}
	if set, ok := floatMeasures[key]; ok {
		if f, ok := anyFloat(v); ok {
			set(&c.measure, f)
		}
		return
	}
	if set, ok := boolMeasures[key]; ok {
		if b, ok := anyBool(v); ok {
			set(&c.measure, b)
		}
		return
	}
	if kind, ok := contentAttrs[key]; ok {
		if s := anyText(v); s != "" {
			c.content[contentOrdinal(kind)] = rawContent{body: s, set: true}
		}
		// tool_input 은 원문이자 파일 경로의 유일한 출처다. 원문은 event_content 로,
		// 경로는 정규화해 Target 으로 간다 — 같은 값이 두 규칙을 따로 통과한다 (ADR 0003).
		if kind == event.ContentToolInput {
			if p := toolInputTarget(v); p.Hash != "" {
				c.target = p
			}
		}
	}
}

// anyString 은 문자열 컬럼에 넣을 값을 뽑는다. 스칼라는 문자열로 맞춰 받고
// 배열·맵은 컬럼에 들어갈 형태가 아니므로 버린다.
func anyString(v *commonpb.AnyValue) string {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return strings.TrimSpace(x.StringValue)
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(x.BoolValue)
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(x.IntValue, 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(x.DoubleValue, 'g', -1, 64)
	}
	return ""
}

// anyText 는 원문 본문으로 쓸 값을 뽑는다. 구조화된 값(tool_input 이 kvlist 로 오는 경우)은
// protojson 으로 직렬화해 형태를 잃지 않게 한다 — 세션 조립기가 파일 경로를 여기서 파싱한다.
func anyText(v *commonpb.AnyValue) string {
	switch v.GetValue().(type) {
	case *commonpb.AnyValue_ArrayValue, *commonpb.AnyValue_KvlistValue:
		out, err := protojson.Marshal(v)
		if err != nil {
			return ""
		}
		return string(out)
	case *commonpb.AnyValue_BytesValue:
		return ""
	}
	if s, ok := v.GetValue().(*commonpb.AnyValue_StringValue); ok {
		return s.StringValue // 원문은 TrimSpace 하지 않는다
	}
	return anyString(v)
}

func anyInt(v *commonpb.AnyValue) (int64, bool) {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_IntValue:
		return x.IntValue, true
	case *commonpb.AnyValue_DoubleValue:
		return int64(x.DoubleValue), true
	case *commonpb.AnyValue_StringValue:
		if n, err := strconv.ParseInt(strings.TrimSpace(x.StringValue), 10, 64); err == nil {
			return n, true
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(x.StringValue), 64); err == nil {
			return int64(f), true
		}
	}
	return 0, false
}

func anyFloat(v *commonpb.AnyValue) (float64, bool) {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_DoubleValue:
		return x.DoubleValue, true
	case *commonpb.AnyValue_IntValue:
		return float64(x.IntValue), true
	case *commonpb.AnyValue_StringValue:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x.StringValue), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func anyBool(v *commonpb.AnyValue) (bool, bool) {
	switch x := v.GetValue().(type) {
	case *commonpb.AnyValue_BoolValue:
		return x.BoolValue, true
	case *commonpb.AnyValue_StringValue:
		if b, err := strconv.ParseBool(strings.TrimSpace(x.StringValue)); err == nil {
			return b, true
		}
	case *commonpb.AnyValue_IntValue:
		return x.IntValue != 0, true
	}
	return false, false
}

// anyTimestamp 는 event.timestamp 속성을 읽는다. Claude Code 는 RFC3339 문자열로 보내고,
// 다른 벤더는 유닉스 나노초 정수로 보낼 수 있다.
func anyTimestamp(v *commonpb.AnyValue) (event.UnixNano, bool) {
	if s := anyString(v); s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return event.NanoFromTime(t), true
		}
	}
	if n, ok := anyInt(v); ok && n > 0 {
		return event.UnixNano(n), true
	}
	return 0, false
}

// serviceVendors 는 service.name(또는 시그널 이름 접두)을 events.vendor 로 옮긴다.
// 스키마는 벤더를 제약하지 않으므로 여기 없는 값도 정규화만 거쳐 그대로 쓴다.
var serviceVendors = map[string]string{
	"claude-code": "claude_code",
	"claude_code": "claude_code",
	"claude":      "claude_code",
	"codex":       "codex",
	"codex-cli":   "codex",
	"codex_cli":   "codex",
	"gemini-cli":  "gemini_cli",
	"gemini_cli":  "gemini_cli",
	"cursor":      "cursor",
}

// vendorOf 는 벤더를 세 단계로 찾는다: service.name → 시그널 이름 접두 → 호출자 폴백.
// 이름 접두를 쓰는 이유는 Claude Code 가 service.name 없이도 claude_code.* 로 이름을 짓기 때문이다.
func vendorOf(serviceName, signalName, fallback string) string {
	if v, ok := serviceVendors[strings.ToLower(serviceName)]; ok {
		return v
	}
	if i := strings.IndexByte(signalName, '.'); i > 0 {
		if v, ok := serviceVendors[strings.ToLower(signalName[:i])]; ok {
			return v
		}
	}
	if serviceName != "" {
		return normalizeVendor(serviceName)
	}
	return fallback
}

func normalizeVendor(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "-", "_")
}
