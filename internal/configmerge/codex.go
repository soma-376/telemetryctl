package configmerge

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	"github.com/BurntSushi/toml"
	"github.com/your-org/telemetryctl/internal/manifest"
)

// codexOTelTable 은 manifest 로부터 Codex config.toml 의 [otel] 테이블 값을 만든다.
//
// TODO(verify): [otel] 하위 키 이름은 Codex 의 실제 OTel 설정 스키마와 대조해 확정할 것.
// 문서(§4.1)가 확인해 준 키는 environment, log_user_prompt 뿐이다. endpoint/protocol/token 을
// Codex 가 어떤 키로 받는지 확인 전까지 이 매핑은 잠정이다.
func codexOTelTable(m *manifest.Manifest) map[string]any {
	env := "production"
	if v, ok := m.ResourceAttributes["deployment.environment"]; ok && v != "" {
		env = v
	}
	return map[string]any{
		"environment":     env,
		"log_user_prompt": m.Privacy.CollectUserPrompts,
		// --- 아래는 TODO(verify): Codex 실제 키로 교체 ---
		"endpoint": m.OTLP.Endpoint,
		"protocol": m.OTLP.Protocol,
	}
}

// MergeCodex 는 Codex config.toml 의 [otel] 테이블만 병합한다.
// 다른 최상위 키(model 등)와 다른 테이블은 파서를 통해 보존한다.
//
// 한계 (TODO): BurntSushi/toml 은 decode→encode 왕복에서 주석과 키 순서를 보존하지 못한다.
// 사용자 config.toml 의 주석/서식을 지키려면 서식 보존 편집 전략(별도 후속 작업)이 필요하다.
// 정규식 편집은 §4.1 에 따라 금지.
func MergeCodex(path string, m *manifest.Manifest, force bool) (Result, error) {
	raw, existed, err := readFileIfExists(path)
	if err != nil {
		return Result{}, err
	}

	root := map[string]any{}
	if existed && len(raw) > 0 {
		if err := toml.Unmarshal(raw, &root); err != nil {
			return Result{}, fmt.Errorf("codex config.toml 파싱 실패 (%s): %w", path, err)
		}
	}

	existingOTel, _ := root["otel"].(map[string]any)

	// 충돌 감지 (§4.2)
	if existingOTel != nil {
		if cur, ok := existingOTel["endpoint"].(string); ok && cur != "" && cur != m.OTLP.Endpoint && !force {
			return Result{Path: path}, fmt.Errorf("%w (codex): 기존=%q, 신규=%q — --force 로만 교체", ErrEndpointConflict, cur, m.OTLP.Endpoint)
		}
	}

	res := Result{Path: path, Created: !existed}
	if existed {
		bak, err := backupOnce(path, raw)
		if err != nil {
			return Result{}, err
		}
		res.BackupPath = bak
	}

	// [otel] 테이블만 우리 값으로 병합. 기존 [otel] 안의 사용자 커스텀 키는 보존.
	otel := existingOTel
	if otel == nil {
		otel = map[string]any{}
	}
	desired := codexOTelTable(m)
	keys := make([]string, 0, len(desired))
	for k, v := range desired {
		otel[k] = v
		keys = append(keys, "otel."+k)
	}
	sort.Strings(keys)
	root["otel"] = otel
	res.ManagedKeys = keys

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(root); err != nil {
		return Result{}, err
	}
	if err := ensureDir(path); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return Result{}, err
	}
	return res, nil
}
