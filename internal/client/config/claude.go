package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/your-org/pulsemetry/internal/contract"
)

// claudeManagedEnvKeys 는 installer 가 관리하는 Claude Code env 키다.
// uninstall 시 이 키들만 제거한다. 사용자가 넣은 다른 env 는 절대 건드리지 않는다.
//
// TODO(verify): 아래 키 이름은 Claude Code telemetry 문서(공식 env 변수)와 대조해 확정할 것.
// OTLP 표준 변수(OTEL_EXPORTER_OTLP_*)는 안정적이나 CLAUDE_CODE_* / OTEL_LOG_USER_PROMPTS 는
// 클라이언트 버전에 따라 달라질 수 있다 (§5.10).
var claudeManagedEnvKeys = []string{
	"CLAUDE_CODE_ENABLE_TELEMETRY",
	"OTEL_EXPORTER_OTLP_PROTOCOL",
	"OTEL_EXPORTER_OTLP_ENDPOINT",
	"OTEL_EXPORTER_OTLP_HEADERS",
	"OTEL_EXPORTER_OTLP_COMPRESSION",
	"OTEL_METRICS_EXPORTER",
	"OTEL_LOGS_EXPORTER",
	"OTEL_LOG_USER_PROMPTS",
}

// claudeEnv 는 manifest 설정과 설치 토큰으로부터 주입할 env 키/값을 만든다.
// 토큰은 manifest(설정)가 아니라 enroll 봉투에서 오므로 별도 인자로 받는다.
func claudeEnv(m *contract.Manifest, token string) map[string]string {
	env := map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY":   "1",
		"OTEL_EXPORTER_OTLP_PROTOCOL":    m.OTLP.Protocol,
		"OTEL_EXPORTER_OTLP_ENDPOINT":    m.OTLP.Endpoint,
		"OTEL_EXPORTER_OTLP_HEADERS":     "Authorization=Bearer " + token,
		"OTEL_LOG_USER_PROMPTS":          boolEnv(m.Privacy.CollectUserPrompts),
	}
	if c := m.OTLP.Compression; c != "" {
		env["OTEL_EXPORTER_OTLP_COMPRESSION"] = c
	}
	if m.Signals.Metrics {
		env["OTEL_METRICS_EXPORTER"] = "otlp"
	}
	if m.Signals.Logs {
		env["OTEL_LOGS_EXPORTER"] = "otlp"
	}
	return env
}

func boolEnv(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// MergeClaude 는 Claude Code settings.json 의 env 객체에 OTel 키만 병합한다.
// 기존 env 의 다른 키와 최상위 다른 설정(model, mcp 등)은 보존한다.
// token 은 enroll 봉투의 installation_token 이다 (Authorization 헤더에 쓰인다).
func MergeClaude(path string, m *contract.Manifest, token string, force bool) (Result, error) {
	raw, existed, err := readFileIfExists(path)
	if err != nil {
		return Result{}, err
	}

	root := map[string]any{}
	if existed && len(raw) > 0 {
		if err := json.Unmarshal(raw, &root); err != nil {
			return Result{}, fmt.Errorf("claude settings.json 파싱 실패 (%s): %w", path, err)
		}
	}

	env := map[string]any{}
	if e, ok := root["env"].(map[string]any); ok {
		env = e
	}

	// 충돌 감지 (§4.2): 기존 endpoint 가 우리 것과 다르면 자동 교체 금지.
	if cur, ok := env["OTEL_EXPORTER_OTLP_ENDPOINT"].(string); ok && cur != "" && cur != m.OTLP.Endpoint && !force {
		return Result{Path: path}, fmt.Errorf("%w (claude): 기존=%q, 신규=%q — --force 로만 교체", ErrEndpointConflict, cur, m.OTLP.Endpoint)
	}

	res := Result{Path: path, Created: !existed}
	if existed {
		bak, err := backupOnce(path, raw)
		if err != nil {
			return Result{}, err
		}
		res.BackupPath = bak
	}

	desired := claudeEnv(m, token)
	keys := make([]string, 0, len(desired))
	for k, v := range desired {
		env[k] = v
		keys = append(keys, "env."+k)
	}
	sort.Strings(keys)
	root["env"] = env
	res.ManagedKeys = keys

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return Result{}, err
	}
	out = append(out, '\n')
	if err := ensureDir(path); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return Result{}, err
	}
	return res, nil
}
