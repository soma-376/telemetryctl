package config

import (
	"bytes"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/your-org/pulsemetry/internal/contract"
)

var codexManagedOTelKeys = []string{
	"environment",
	"log_user_prompt",
	"exporter",
	// metrics_exporter·trace_exporter 는 로컬 배선에서만 쓰지만 목록에는 언제나 있어야
	// 한다. 빠지면 local disable 이 두 테이블을 지우지 못해 회사 직결 상태에서도 Codex 가
	// localhost 로 메트릭·트레이스를 계속 보낸다 (PROJ-45).
	"metrics_exporter",
	"trace_exporter",
	"endpoint",
	"protocol",
	"headers",
}

// codex 로컬 배선이 쓰는 고정값 (PROJ-45 참고 자료).
const (
	// codexLocalProtocol 은 OTLP/HTTP protobuf 를 가리키는 Codex 표기다.
	codexLocalProtocol = "binary"
	// codexLogsExporterKey 는 로그 exporter 테이블 이름이다. Codex 는 로그만 exporter 로
	// 부르고 나머지는 시그널 이름을 앞에 붙인다.
	codexLogsExporterKey    = "exporter"
	codexMetricsExporterKey = "metrics_exporter"
	codexTracesExporterKey  = "trace_exporter"
	codexLegacyHookCommand  = "pulsemetry hook codex"
	codexHookTimeoutSeconds = int64(3)
)

var codexLifecycleEvents = []string{"SessionStart", "SessionEnd"}

func quoteHookArg(value string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return `'` + strings.ReplaceAll(value, `'`, `'"'"'`) + `'`
}

func codexHookCommand(executable, dataDir string) (string, error) {
	if executable == "" {
		return codexLegacyHookCommand, nil
	}
	if !filepath.IsAbs(executable) {
		return "", fmt.Errorf("Codex hook 실행 경로가 절대 경로가 아님: %q", executable)
	}
	command := quoteHookArg(executable) + " hook codex"
	if dataDir != "" {
		if !filepath.IsAbs(dataDir) {
			return "", fmt.Errorf("Codex hook 데이터 경로가 절대 경로가 아님: %q", dataDir)
		}
		command += " --data-dir " + quoteHookArg(dataDir)
	}
	return command, nil
}

func codexHookHandler(command string) map[string]any {
	handler := map[string]any{
		"type": "command", "command": command, "timeout": codexHookTimeoutSeconds,
	}
	return handler
}

func sameCodexHook(v map[string]any, desired string) bool {
	typ, _ := v["type"].(string)
	command, _ := v["command"].(string)
	if typ != "command" {
		return false
	}
	if command == desired || command == codexLegacyHookCommand {
		return true
	}
	// 절대 경로는 업그레이드로 달라질 수 있다. 예약한 서브커맨드와 pulsemetry 실행
	// 파일 이름이 함께 맞는 handler만 이전 설치의 것으로 인정한다.
	trimmed := strings.TrimSpace(command)
	marker := " hook codex"
	markerAt := strings.LastIndex(trimmed, marker)
	if markerAt < 0 {
		return false
	}
	tail := strings.TrimSpace(trimmed[markerAt+len(marker):])
	if tail != "" && !strings.HasPrefix(tail, "--data-dir ") {
		return false
	}
	trimmed = strings.TrimSpace(trimmed[:markerAt])
	trimmed = strings.Trim(strings.TrimSpace(trimmed), `"'`)
	base := strings.ToLower(filepath.Base(trimmed))
	return base == "pulsemetry" || base == "pulsemetry.exe"
}

// mergeCodexHooks 는 이벤트 배열이나 사용자 handler를 소유하지 않고 우리 handler 하나만 관리한다.
func mergeCodexHooks(root map[string]any, enabled bool, command string) []string {
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	managed := []string{}
	for _, eventName := range codexLifecycleEvents {
		groups, _ := hooks[eventName].([]map[string]any)
		// 우리 handler 는 남기지 않고 아래에서 새로 넣는다 (mergeClaudeHooks 와 같은
		// 규칙). 매칭된 것을 그대로 두면 이전 설치의 낡은 경로·timeout 이 병합을
		// 몇 번 돌려도 영원히 남는다.
		out := groups[:0]
		for _, group := range groups {
			handlers, _ := group["hooks"].([]map[string]any)
			kept := handlers[:0]
			for _, handler := range handlers {
				if sameCodexHook(handler, command) {
					continue
				}
				kept = append(kept, handler)
			}
			if len(kept) > 0 {
				group["hooks"] = kept
				out = append(out, group)
			}
		}
		if enabled {
			out = append(out, map[string]any{"hooks": []map[string]any{codexHookHandler(command)}})
			managed = append(managed, "hooks."+eventName+":"+command)
		}
		if len(out) == 0 {
			delete(hooks, eventName)
		} else {
			hooks[eventName] = out
		}
	}
	// hooksEmpty 는 우리 handler 를 걷어낸 뒤 남은 훅이 하나도 없다는 뜻이다.
	hooksEmpty := len(hooks) == 0
	if hooksEmpty {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}

	// features.hooks 는 우리 키가 아니라 Codex 의 훅 기능 자체를 켜는 토글이다. 누가
	// 켰는지 알 방법이 없으므로 소유권으로 판단하지 않고 **필요 여부**로 판단한다 —
	// 남은 handler 가 하나도 없을 때만 끈다. 사용자 훅이 남았는데 끄면 그 훅이 통째로
	// 죽고, 끄는 분기가 아예 없으면 local disable 이 스위치를 남긴다.
	features, _ := root["features"].(map[string]any)
	if enabled {
		if features == nil {
			features = map[string]any{}
		}
		features["hooks"] = true
		root["features"] = features
		managed = append(managed, "features.hooks")
		return managed
	}
	if features != nil && hooksEmpty {
		delete(features, "hooks")
		if len(features) == 0 {
			delete(root, "features")
		} else {
			root["features"] = features
		}
	}
	return managed
}

// codexSignalPaths 는 로컬 배선에서 시그널별 exporter 가 가리킬 경로다.
//
// 값은 otlpdecode.PayloadKind.Path() 와 같아야 한다. 그 상수를 import 하지 않는 이유는
// config 가 otlpdecode 를 끌어들이면 enroll·status 경로까지 protobuf 디코더가 딸려오기
// 때문이다 — LocalIngestHeader·DefaultLocalPort 와 같은 의존성 방향 규칙이다.
// 두 값이 어긋나면 codex_test.go 가 잡는다 (테스트에서만 otlpdecode 를 import 한다).
var codexSignalPaths = map[string]string{
	codexLogsExporterKey:    "/v1/logs",
	codexMetricsExporterKey: "/v1/metrics",
	codexTracesExporterKey:  "/v1/traces",
}

func codexOTelTable(m *contract.Manifest, token string) (map[string]any, error) {
	environment := "production"
	if value := m.ResourceAttributes["deployment.environment"]; value != "" {
		environment = value
	}

	// 로컬 수신기는 bearer 토큰만으로 통과시키지 않는다 — localheader.go 참고.
	// TOML 은 헤더가 원래 표라서 claude 쪽처럼 문자열을 조립할 필요가 없다.
	headers := map[string]any{
		"Authorization": "Bearer " + token,
	}
	if isLocalEndpoint(m.OTLP.Endpoint) {
		headers[LocalIngestHeader] = LocalIngestHeaderValue
	}

	table := map[string]any{
		"environment":     environment,
		"log_user_prompt": m.Privacy.CollectUserPrompts,
	}

	// 로컬 배선은 시그널마다 exporter 를 따로 둔다.
	//
	// Claude 와 달리 Codex 는 base endpoint 를 주면 경로를 스스로 붙이지 않는다. 하나의
	// exporter 만 두면 메트릭·트레이스가 /v1/logs 로 가고 수신기가 전부 거부한다.
	// 회사 직결은 예전 그대로 exporter 하나만 쓴다 — 그 경로에서 무엇이 나가는지는
	// PROJ-45 의 범위가 아니고, 바꾸면 회사가 받는 데이터가 달라진다 (불변식 1).
	if isLocalEndpoint(m.OTLP.Endpoint) {
		base := strings.TrimRight(m.OTLP.Endpoint, "/")
		for key, path := range codexSignalPaths {
			// 헤더 맵은 exporter 마다 복사한다. 같은 맵을 세 테이블이 공유하면 나중에
			// 한 exporter 의 헤더만 고치려는 코드가 세 개를 한꺼번에 바꾼다 —
			// installer.cloneManifest 가 막는 것과 같은 종류의 사고다.
			table[key] = map[string]any{
				"otlp-http": map[string]any{
					"endpoint": base + path,
					"protocol": codexLocalProtocol,
					"headers":  cloneHeaders(headers),
				},
			}
		}
		return table, nil
	}

	exporterID := ""
	exporter := map[string]any{
		"endpoint": m.OTLP.Endpoint,
		"headers":  headers,
	}
	switch m.OTLP.Protocol {
	case "http/protobuf":
		exporterID = "otlp-http"
		exporter["protocol"] = "binary"
	case "http/json":
		exporterID = "otlp-http"
		exporter["protocol"] = "json"
	case "grpc":
		exporterID = "otlp-grpc"
	default:
		return nil, fmt.Errorf("unsupported Codex OTLP protocol %q", m.OTLP.Protocol)
	}

	table[codexLogsExporterKey] = map[string]any{exporterID: exporter}
	return table, nil
}

func cloneHeaders(h map[string]any) map[string]any {
	out := make(map[string]any, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
}

// MergeCodex authoritatively synchronizes only Pulsemetry-managed [otel] keys.
func MergeCodex(path string, m *contract.Manifest, token string, force bool) (Result, error) {
	return MergeCodexWithExecutable(path, m, token, force, "", "")
}

// MergeCodexWithExecutable 는 로컬 lifecycle 훅에 PATH 대신 현재 설치 바이너리의 절대
// 경로를 쓴다. executable은 회사 직결에서도 받아 두어 local disable이 이전 훅을 찾는다.
func MergeCodexWithExecutable(path string, m *contract.Manifest, token string, _ bool, executable, dataDir string) (Result, error) {
	hookCommand, err := codexHookCommand(executable, dataDir)
	if err != nil {
		return Result{}, err
	}
	raw, existed, err := readFileIfExists(path)
	if err != nil {
		return Result{}, err
	}
	root := map[string]any{}
	if existed && len(raw) > 0 {
		if err := toml.Unmarshal(raw, &root); err != nil {
			return Result{}, fmt.Errorf("parse Codex config %s: %w", path, err)
		}
	}
	otel, _ := root["otel"].(map[string]any)
	if otel == nil {
		otel = map[string]any{}
	}
	for _, key := range codexManagedOTelKeys {
		delete(otel, key)
	}
	desired, err := codexOTelTable(m, token)
	if err != nil {
		return Result{}, err
	}
	managed := make([]string, 0, len(desired))
	for key, value := range desired {
		otel[key] = value
		managed = append(managed, "otel."+key)
	}
	managed = append(managed, mergeCodexHooks(root, isLocalEndpoint(m.OTLP.Endpoint), hookCommand)...)
	sort.Strings(managed)
	root["otel"] = otel
	var out bytes.Buffer
	if err := toml.NewEncoder(&out).Encode(root); err != nil {
		return Result{}, err
	}
	if err := AtomicWriteFile(path, out.Bytes(), 0o600); err != nil {
		return Result{}, err
	}
	return Result{Path: path, ManagedKeys: managed, Created: !existed}, nil
}
