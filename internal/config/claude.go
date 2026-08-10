package config

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/your-org/pulsemetry/internal/contract"
)

// claudeManagedEnvKeys 는 이 도구가 소유권을 주장하는 env 키 전부다.
//
// 이 목록의 역할은 두 가지다. (1) MergeClaude 가 병합 전에 여기 있는 키를 전부 지워
// "우리가 예전에 넣었다가 지금은 넣지 않는 키"가 남지 않게 하고, (2) uninstall 이
// 사용자가 직접 넣은 키를 건드리지 않고 우리 것만 제거할 수 있게 한다 (§5.2).
//
// 따라서 claudeEnv 가 쓰는 키는 **반드시** 여기에도 있어야 한다. 한쪽만 늘리면
// 조용히 잔재가 남는다 — claude_test.go 의 대칭성 테스트가 그것을 잡는다.
var claudeManagedEnvKeys = []string{
	"CLAUDE_CODE_ENABLE_TELEMETRY",
	"OTEL_EXPORTER_OTLP_PROTOCOL",
	"OTEL_EXPORTER_OTLP_ENDPOINT",
	"OTEL_EXPORTER_OTLP_HEADERS",
	"OTEL_EXPORTER_OTLP_COMPRESSION",
	"OTEL_METRICS_EXPORTER",
	"OTEL_LOGS_EXPORTER",
	"OTEL_TRACES_EXPORTER",
	"OTEL_LOG_USER_PROMPTS",
	"OTEL_LOG_ASSISTANT_RESPONSES",
	"OTEL_LOG_TOOL_DETAILS",
	"OTEL_LOG_TOOL_CONTENT",
	"OTEL_LOG_RAW_API_BODIES",
	// 아래 셋은 PROJ-36 에서 추가됐다 (계획서 「CLI 변경」).
	"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE",
	"OTEL_METRIC_EXPORT_INTERVAL",
	"OTEL_LOGS_EXPORT_INTERVAL",
}

// 아래 세 값은 PROJ-36 이 명시적으로 고정하는 exporter 동작이다.
//
// 전부 Claude Code 의 문서화된 기본값과 같은 값을 쓴다. 목적이 "동작을 바꾸는 것"이
// 아니라 "가정하지 않는 것"이기 때문이다 — 사용자가 셸 프로필에서 이 값들을 덮어써도
// 우리가 쓴 settings.json 의 env 가 이깁니다. 값을 적어 두면 그 가정이 검증 가능해지고,
// 관리 키 목록에 들어가 있으므로 uninstall 이 되돌릴 수도 있다 (§5.2).
const (
	// claudeTemporality 는 delta 로 고정한다. 로컬 롤업이 Sum.aggregation_temporality 를
	// 실제로 읽어 분기하긴 하지만(계획서 「반드시 알아야 할 제약」 §4), cumulative 로
	// 들어오면 리셋 추적 오차가 그대로 비용 수치에 얹힌다. 어차피 Claude Code 기본값이
	// delta 이므로 그것을 명시적으로 못박는다.
	claudeTemporality = "delta"
	// claudeMetricExportInterval 은 밀리초다. Claude Code 기본값 60초.
	claudeMetricExportInterval = "60000"
	// claudeLogsExportInterval 은 밀리초다. Claude Code 기본값 5초.
	claudeLogsExportInterval = "5000"
)

func claudeEnv(m *contract.Manifest, token string) map[string]string {
	env := map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
		"OTEL_EXPORTER_OTLP_PROTOCOL":  m.OTLP.Protocol,
		"OTEL_EXPORTER_OTLP_ENDPOINT":  m.OTLP.Endpoint,
		"OTEL_EXPORTER_OTLP_HEADERS":   claudeHeaders(m, token),
		"OTEL_METRICS_EXPORTER":        exporterEnv(m.Signals.Metrics),
		"OTEL_LOGS_EXPORTER":           exporterEnv(m.Signals.Logs),
		"OTEL_TRACES_EXPORTER":         exporterEnv(m.Signals.Traces),
		"OTEL_LOG_USER_PROMPTS":        boolEnv(m.Privacy.CollectUserPrompts),
		"OTEL_LOG_ASSISTANT_RESPONSES": boolEnv(m.Privacy.CollectAssistantResponses),
		"OTEL_LOG_TOOL_DETAILS":        boolEnv(m.Privacy.CollectToolDetails),
		"OTEL_LOG_TOOL_CONTENT":        boolEnv(m.Privacy.CollectToolContent),
		"OTEL_LOG_RAW_API_BODIES":      boolEnv(m.Privacy.CollectRawAPIBodies),

		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE": claudeTemporality,
		"OTEL_METRIC_EXPORT_INTERVAL":                       claudeMetricExportInterval,
		"OTEL_LOGS_EXPORT_INTERVAL":                         claudeLogsExportInterval,
	}
	if m.OTLP.Compression != "" {
		env["OTEL_EXPORTER_OTLP_COMPRESSION"] = m.OTLP.Compression
	}
	return env
}

// claudeHeaders 는 OTEL_EXPORTER_OTLP_HEADERS 값을 만든다.
//
// 형식은 W3C Baggage 를 닮은 "K=V,K=V" 다. 로컬 수신기로 재배선된 경우에만 두 번째
// 쌍이 붙는다 (localheader.go 가 그 이유를 설명한다).
//
// 쉼표와 등호로 나누는 형식이라 값에 그 두 문자가 없어야 파싱이 모호해지지 않는다.
// 토큰은 receiver.NewToken 이 base64url **패딩 없이** 만들므로 (= 가 없고 - _ 만 쓴다)
// 그 조건을 만족한다 — receiver/auth.go 가 인용·이스케이프를 피하려고 일부러 고른
// 인코딩이고, 여기가 그 선택에 기대는 지점이다.
//
// 되읽기는 bearerFromHeaderList 가 한다 (token.go). `local disable` 이 회사 토큰을
// 되찾는 유일한 경로이므로 이 형식과 그 파서는 함께 움직여야 한다.
func claudeHeaders(m *contract.Manifest, token string) string {
	headers := "Authorization=Bearer " + token
	if isLocalEndpoint(m.OTLP.Endpoint) {
		headers += "," + LocalIngestHeader + "=" + LocalIngestHeaderValue
	}
	return headers
}

func boolEnv(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func exporterEnv(enabled bool) string {
	if enabled {
		return "otlp"
	}
	return "none"
}

// MergeClaude authoritatively synchronizes only Pulsemetry-managed OTel keys.
func MergeClaude(path string, m *contract.Manifest, token string, _ bool) (Result, error) {
	raw, existed, err := readFileIfExists(path)
	if err != nil {
		return Result{}, err
	}
	root := map[string]any{}
	if existed && len(raw) > 0 {
		if err := json.Unmarshal(raw, &root); err != nil {
			return Result{}, fmt.Errorf("parse Claude settings %s: %w", path, err)
		}
	}
	env := map[string]any{}
	if current, ok := root["env"].(map[string]any); ok {
		env = current
	}
	for _, key := range claudeManagedEnvKeys {
		delete(env, key)
	}
	desired := claudeEnv(m, token)
	keys := make([]string, 0, len(desired))
	for key, value := range desired {
		env[key] = value
		keys = append(keys, "env."+key)
	}
	sort.Strings(keys)
	root["env"] = env
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return Result{}, err
	}
	out = append(out, '\n')
	if err := AtomicWriteFile(path, out, 0o600); err != nil {
		return Result{}, err
	}
	return Result{Path: path, ManagedKeys: keys, Created: !existed}, nil
}
