package config

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/your-org/pulsemetry/internal/contract"
)

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
}

func claudeEnv(m *contract.Manifest, token string) map[string]string {
	env := map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1",
		"OTEL_EXPORTER_OTLP_PROTOCOL":  m.OTLP.Protocol,
		"OTEL_EXPORTER_OTLP_ENDPOINT":  m.OTLP.Endpoint,
		"OTEL_EXPORTER_OTLP_HEADERS":   "Authorization=Bearer " + token,
		"OTEL_METRICS_EXPORTER":        exporterEnv(m.Signals.Metrics),
		"OTEL_LOGS_EXPORTER":           exporterEnv(m.Signals.Logs),
		"OTEL_TRACES_EXPORTER":         exporterEnv(m.Signals.Traces),
		"OTEL_LOG_USER_PROMPTS":        boolEnv(m.Privacy.CollectUserPrompts),
		"OTEL_LOG_ASSISTANT_RESPONSES": boolEnv(m.Privacy.CollectAssistantResponses),
		"OTEL_LOG_TOOL_DETAILS":        boolEnv(m.Privacy.CollectToolDetails),
		"OTEL_LOG_TOOL_CONTENT":        boolEnv(m.Privacy.CollectToolContent),
		"OTEL_LOG_RAW_API_BODIES":      boolEnv(m.Privacy.CollectRawAPIBodies),
	}
	if m.OTLP.Compression != "" {
		env["OTEL_EXPORTER_OTLP_COMPRESSION"] = m.OTLP.Compression
	}
	return env
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
