package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/receiver"
)

func companyManifest() *contract.Manifest {
	return &contract.Manifest{
		SchemaVersion:  1,
		ConfigRevision: 3,
		OTLP: contract.OTLP{
			Endpoint: "https://collector.example.com",
			Protocol: "http/protobuf",
		},
		Signals: contract.Signals{Logs: true, Metrics: true, Traces: false},
		Privacy: contract.Privacy{}, // 전부 false 가 회사 기본값이다 (§4.6)
	}
}

// TestClaudeManagedKeysCoverWrittenKeys 는 "쓰는 키"와 "관리한다고 선언한 키"의 대칭성을
// 지킨다. 한쪽만 늘어나면 uninstall 이 우리가 넣은 키를 남긴다 (§5.2).
func TestClaudeManagedKeysCoverWrittenKeys(t *testing.T) {
	managed := make(map[string]bool, len(claudeManagedEnvKeys))
	for _, k := range claudeManagedEnvKeys {
		if managed[k] {
			t.Errorf("claudeManagedEnvKeys 에 중복 항목: %s", k)
		}
		managed[k] = true
	}

	// 압축이 있는 manifest 로도 한 번 돌려야 OTEL_EXPORTER_OTLP_COMPRESSION 이 포함된다.
	m := companyManifest()
	m.OTLP.Compression = "gzip"
	for key := range claudeEnv(m, "tok") {
		if !managed[key] {
			t.Errorf("claudeEnv 가 쓰는 %s 가 claudeManagedEnvKeys 에 없다 — uninstall 이 남긴다", key)
		}
	}
}

// TestClaude확장텔레메트리는로컬에서만켜진다 는 CLAUDE_CODE_ENHANCED_TELEMETRY_BETA 의
// 조건부 기록을 못박는다 (PROJ-45).
//
// 회사 직결 상태에서 켜면 회사가 받는 데이터가 재배선 여부에 따라 달라진다 —
// installer/local.go 의 불변식 1 위반이다. 로컬에서는 forward 가 회사 기준으로 다시
// 거르므로 켜도 상위로 나가는 것이 늘지 않는다.
func TestClaude확장텔레메트리는로컬에서만켜진다(t *testing.T) {
	const key = "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA"

	tests := []struct {
		name     string
		endpoint string
		want     string // 빈 문자열이면 키가 없어야 한다
	}{
		{name: "로컬 재배선", endpoint: "http://localhost:4318", want: "1"},
		{name: "회사 직결 https", endpoint: "https://collector.example.com"},
		// https://localhost 는 우리 수신기가 아니다 — 수신기는 평문 http 로만 뜬다.
		{name: "https localhost 는 로컬이 아니다", endpoint: "https://localhost:4318"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := companyManifest()
			m.OTLP.Endpoint = tt.endpoint
			got, ok := claudeEnv(m, "tok")[key]
			if tt.want == "" {
				if ok {
					t.Fatalf("%s 가 회사 직결 설정에 %q 로 들어갔다 — 회사가 받는 데이터가 늘어난다", key, got)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("%s = %q (있음=%v), want %q", key, got, ok, tt.want)
			}
		})
	}

	// 조건부로 쓰는 키라도 관리 목록에는 언제나 있어야 한다. 없으면 local disable 이
	// 지우지 못해 회사 직결 상태에 로컬 흔적이 남는다.
	if !containsKey(claudeManagedEnvKeys, key) {
		t.Errorf("%s 가 claudeManagedEnvKeys 에 없다 — disable 이 지우지 못한다", key)
	}
}

// TestClaudeManagedKeysIncludeProj36Additions 는 계획서가 지정한 세 키가 관리 목록에
// 들어갔는지 본다. PROJ-45 가 추가한 두 키도 함께 지킨다.
func TestClaudeManagedKeysIncludeProj36Additions(t *testing.T) {
	want := []string{
		"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE",
		"OTEL_METRIC_EXPORT_INTERVAL",
		"OTEL_LOGS_EXPORT_INTERVAL",
		// PROJ-45. 무조건 쓰는 키만 여기 넣는다 — 이 루프는 "관리 목록에 있고 claudeEnv 가
		// 실제로 쓴다"를 함께 본다. 로컬에서만 쓰는 CLAUDE_CODE_ENHANCED_TELEMETRY_BETA 는
		// TestClaude확장텔레메트리는로컬에서만켜진다 가 따로 지킨다.
		"OTEL_TRACES_EXPORT_INTERVAL",
	}
	for _, key := range want {
		t.Run(key, func(t *testing.T) {
			if !containsKey(claudeManagedEnvKeys, key) {
				t.Fatalf("%s 가 claudeManagedEnvKeys 에 없다", key)
			}
			if _, ok := claudeEnv(companyManifest(), "tok")[key]; !ok {
				t.Fatalf("%s 를 claudeEnv 가 쓰지 않는다", key)
			}
		})
	}

	env := claudeEnv(companyManifest(), "tok")
	if got := env["OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE"]; got != "delta" {
		t.Errorf("temporality = %q, want delta (계획서 「CLI 변경」의 delta 고정)", got)
	}
}

// TestMergeClaudeRemovesStaleManagedKeys 는 관리 키만 지우고 사용자 키는 남기는지 본다.
func TestMergeClaudeRemovesStaleManagedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	writeJSON(t, path, map[string]any{
		"model": "opus",
		"env": map[string]any{
			"MY_OWN_KEY":                  "keep-me",
			"OTEL_EXPORTER_OTLP_ENDPOINT": "https://old.example.com",
			"OTEL_LOG_USER_PROMPTS":       "1",
		},
	})

	if _, err := MergeClaude(path, companyManifest(), "tok", false); err != nil {
		t.Fatalf("MergeClaude: %v", err)
	}

	root := readJSON(t, path)
	if root["model"] != "opus" {
		t.Errorf("관리 대상이 아닌 최상위 키가 사라졌다: %v", root)
	}
	env, _ := root["env"].(map[string]any)
	if env["MY_OWN_KEY"] != "keep-me" {
		t.Errorf("사용자 env 키가 사라졌다: %v", env)
	}
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "https://collector.example.com" {
		t.Errorf("endpoint = %v, want manifest 값", env["OTEL_EXPORTER_OTLP_ENDPOINT"])
	}
	// 회사 manifest 의 Privacy 는 전부 false 이므로 강제 없이 0 이어야 한다.
	if env["OTEL_LOG_USER_PROMPTS"] != "0" {
		t.Errorf("OTEL_LOG_USER_PROMPTS = %v, want 0 (회사 manifest 기준)", env["OTEL_LOG_USER_PROMPTS"])
	}
}

// TestMergeClaudeLocalEndpointUsesLocalhost 는 로컬 재배선 값이 실제 파일에 어떻게 적히는지
// 확인한다. 127.0.0.1 이 한 글자라도 들어가면 안 된다 (계획서 제약 §1).
func TestMergeClaudeLocalEndpointUsesLocalhost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	m := companyManifest()
	m.OTLP.Endpoint = "http://localhost:4318"
	m.Privacy.CollectUserPrompts = true
	m.Privacy.CollectToolDetails = true

	if _, err := MergeClaude(path, m, "ingest-token", false); err != nil {
		t.Fatalf("MergeClaude: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "127.0.0.1") {
		t.Fatalf("설정 파일에 127.0.0.1 이 있다:\n%s", raw)
	}

	env, _ := readJSON(t, path)["env"].(map[string]any)
	checks := map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318",
		// 로컬 수신기는 bearer 토큰과 X-Pulsemetry-Local 을 **둘 다** 요구한다
		// (receiver/auth.go 의 3중 인증). 하나만 적으면 전량 401 이다.
		"OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer ingest-token,X-Pulsemetry-Local=1",
		"OTEL_LOG_USER_PROMPTS":      "1",
		"OTEL_LOG_TOOL_DETAILS":      "1",
		// 강제하지 않기로 한 것들은 회사 값 그대로여야 한다.
		"OTEL_LOG_ASSISTANT_RESPONSES": "0",
		"OTEL_LOG_TOOL_CONTENT":        "0",
		"OTEL_LOG_RAW_API_BODIES":      "0",
		// 시그널도 회사 값 그대로다 (traces=false).
		"OTEL_TRACES_EXPORTER": "none",
	}
	for key, want := range checks {
		if got := env[key]; got != want {
			t.Errorf("env[%s] = %v, want %q", key, got, want)
		}
	}
}

// TestMergeClaudeCompanyEndpointOmitsLocalHeader 는 회사 Collector 로 나가는 설정에는
// 로컬 헤더가 붙지 않음을 고정한다.
//
// 붙어도 회사 수신기는 모르는 헤더를 무시하므로 실질적 피해는 없지만, 이 음성 케이스가 없으면
// "로컬일 때만 붙인다" 는 규칙이 테스트로 표현되지 않아 조건이 통째로 사라져도 아무도
// 모른다.
func TestMergeClaudeCompanyEndpointOmitsLocalHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := MergeClaude(path, companyManifest(), "company-telemetry-token", false); err != nil {
		t.Fatalf("MergeClaude: %v", err)
	}
	env, _ := readJSON(t, path)["env"].(map[string]any)
	got, _ := env["OTEL_EXPORTER_OTLP_HEADERS"].(string)
	if want := "Authorization=Bearer company-telemetry-token"; got != want {
		t.Errorf("headers = %q, want %q", got, want)
	}
	if strings.Contains(got, LocalIngestHeader) {
		t.Errorf("회사 endpoint 설정에 %s 가 들어갔다: %q", LocalIngestHeader, got)
	}
}

// TestMergeClaudeLocalHeaderSurvivesTokenReadback 은 `local disable` 의 탈출구를 지킨다.
//
// 회사 토큰을 되찾는 유일한 경로가 ReadClaudeToken 이고 (token.go 헤더 주석), 그 파서는
// "K=V,K=V" 를 쉼표로 나눈다. 두 번째 헤더 쌍을 넣은 뒤에도 첫 쌍을 정확히 읽어야
// 재배선을 되돌릴 수 있다.
func TestMergeClaudeLocalHeaderSurvivesTokenReadback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	m := companyManifest()
	m.OTLP.Endpoint = "http://localhost:4318"

	if _, err := MergeClaude(path, m, "local-ingest-token", false); err != nil {
		t.Fatalf("MergeClaude: %v", err)
	}
	env, _ := readJSON(t, path)["env"].(map[string]any)
	headers, _ := env["OTEL_EXPORTER_OTLP_HEADERS"].(string)
	if !strings.Contains(headers, LocalIngestHeader+"="+LocalIngestHeaderValue) {
		t.Fatalf("로컬 헤더가 없다: %q", headers)
	}

	got, err := ReadClaudeToken(path)
	if err != nil {
		t.Fatalf("ReadClaudeToken: %v", err)
	}
	if got != "local-ingest-token" {
		t.Errorf("token = %q, want local-ingest-token — 두 번째 헤더 쌍이 파서를 깨뜨렸다", got)
	}
}

// TestMergeClaudeReportsManagedKeys 는 Result.ManagedKeys 가 실제로 쓴 키 전부인지 본다.
func TestMergeClaudeReportsManagedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	result, err := MergeClaude(path, companyManifest(), "tok", false)
	if err != nil {
		t.Fatalf("MergeClaude: %v", err)
	}
	if !result.Created {
		t.Error("Created = false, want true (새 파일)")
	}
	if !sort.StringsAreSorted(result.ManagedKeys) {
		t.Errorf("ManagedKeys 가 정렬돼 있지 않다: %v", result.ManagedKeys)
	}
	env, _ := readJSON(t, path)["env"].(map[string]any)
	if len(result.ManagedKeys) != len(env) {
		t.Errorf("ManagedKeys %d개, 파일의 env %d개 — 어긋나면 uninstall 이 잔재를 남긴다",
			len(result.ManagedKeys), len(env))
	}
	for _, key := range result.ManagedKeys {
		if _, ok := env[strings.TrimPrefix(key, "env.")]; !ok {
			t.Errorf("ManagedKeys 의 %s 가 파일에 없다", key)
		}
	}
}

func containsKey(list []string, key string) bool {
	for _, v := range list {
		if v == key {
			return true
		}
	}
	return false
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return root
}

// 복제한 경로 상수가 데몬 쪽과 어긋나면 훅이 404 로 조용히 사라진다.
func TestClaudeHookPathsMatchDaemon(t *testing.T) {
	if hookSessionStartPath != receiver.HookSessionStartPath {
		t.Errorf("session-start = %q, daemon = %q", hookSessionStartPath, receiver.HookSessionStartPath)
	}
	if hookSessionEndPath != receiver.HookSessionEndPath {
		t.Errorf("session-end = %q, daemon = %q", hookSessionEndPath, receiver.HookSessionEndPath)
	}
	if hookAPIPathPrefix != receiver.HookAPIPathPrefix {
		t.Errorf("prefix = %q, receiver = %q", hookAPIPathPrefix, receiver.HookAPIPathPrefix)
	}
	for _, ev := range claudeLifecycleEvents {
		if !strings.HasPrefix(ev.path, hookAPIPathPrefix) {
			t.Errorf("%s 경로 %q 가 인증 접두 밖이다", ev.event, ev.path)
		}
	}
}

func claudeHooks(t *testing.T, root map[string]any, event string) []map[string]any {
	t.Helper()
	hooks, _ := root["hooks"].(map[string]any)
	groups := toMapSlice(root, hooks[event])
	var out []map[string]any
	for _, g := range groups {
		out = append(out, toMapSlice(root, g["hooks"])...)
	}
	return out
}

func TestMergeClaudeHooks(t *testing.T) {
	const local = "http://localhost:4318"
	userHook := func() map[string]any {
		return map[string]any{"type": "command", "command": "my-own-script"}
	}

	t.Run("로컬 배선이면 두 이벤트에 http 훅을 건다", func(t *testing.T) {
		root := map[string]any{}
		mergeClaudeHooks(root, local, "tok-1", true)

		for _, ev := range claudeLifecycleEvents {
			got := claudeHooks(t, root, ev.event)
			if len(got) != 1 {
				t.Fatalf("%s handler = %d개", ev.event, len(got))
			}
			if typ, _ := got[0]["type"].(string); typ != "http" {
				t.Errorf("%s type = %q", ev.event, typ)
			}
			wantURL := local + ev.path + "?vendor=claude_code"
			if u, _ := got[0]["url"].(string); u != wantURL {
				t.Errorf("%s url = %q, want %q", ev.event, u, wantURL)
			}
			headers, _ := got[0]["headers"].(map[string]any)
			if headers["Authorization"] != "Bearer tok-1" {
				t.Errorf("%s Authorization = %v", ev.event, headers["Authorization"])
			}
			// 이 헤더가 빠지면 receiver 가 401 을 준다.
			if headers[LocalIngestHeader] != LocalIngestHeaderValue {
				t.Errorf("%s 로컬 헤더 = %v", ev.event, headers[LocalIngestHeader])
			}
		}
	})

	t.Run("회사 직결이면 걸지 않는다", func(t *testing.T) {
		root := map[string]any{}
		if managed := mergeClaudeHooks(root, "https://collector.example.com", "tok-1", false); len(managed) != 0 {
			t.Fatalf("managed = %v", managed)
		}
		if _, ok := root["hooks"]; ok {
			t.Fatalf("hooks 가 생겼다: %v", root["hooks"])
		}
	})

	t.Run("사용자 훅은 보존하고 우리 것만 지운다", func(t *testing.T) {
		root := map[string]any{}
		mergeClaudeHooks(root, local, "tok-1", true)
		hooks, _ := root["hooks"].(map[string]any)
		groups := toMapSlice(root, hooks["SessionEnd"])
		groups = append(groups, map[string]any{"hooks": []map[string]any{userHook()}})
		hooks["SessionEnd"] = groups

		mergeClaudeHooks(root, local, "tok-1", false)

		got := claudeHooks(t, root, "SessionEnd")
		if len(got) != 1 {
			t.Fatalf("handler = %d개, want 1 (사용자 것만)", len(got))
		}
		if got[0]["command"] != "my-own-script" {
			t.Fatalf("남은 handler = %v", got[0])
		}
		if _, ok := hooks["SessionStart"]; ok {
			t.Fatalf("우리 것뿐이던 SessionStart 가 안 지워졌다")
		}
	})

	t.Run("포트가 바뀌어도 옛 handler 를 알아보고 하나만 남긴다", func(t *testing.T) {
		root := map[string]any{}
		mergeClaudeHooks(root, "http://localhost:9999", "old-tok", true)
		// 파일에서 다시 읽으면 []any 다. 그 모양으로도 판정이 되어야 한다.
		raw, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		reread := map[string]any{}
		if err := json.Unmarshal(raw, &reread); err != nil {
			t.Fatal(err)
		}

		mergeClaudeHooks(reread, local, "tok-1", true)

		got := claudeHooks(t, reread, "SessionEnd")
		if len(got) != 1 {
			t.Fatalf("handler = %d개, want 1 — 옛 포트의 handler 가 남았다", len(got))
		}
		if u, _ := got[0]["url"].(string); !strings.Contains(u, "4318") {
			t.Fatalf("url = %q", u)
		}
	})
}

// 경로만 보면 /v1/hooks/ 를 쓰는 남의 서버로 향하는 사용자 훅까지 우리 것으로 오인한다.
func TestMergeClaudeHooksKeepsRemoteUserHookOnSamePath(t *testing.T) {
	const local = "http://localhost:4318"
	remote := map[string]any{
		"type": "http", "url": "https://mycorp.example.com/v1/hooks/audit",
	}
	root := map[string]any{"hooks": map[string]any{
		"SessionEnd": []map[string]any{{"hooks": []map[string]any{remote}}},
	}}

	mergeClaudeHooks(root, local, "tok-1", true)

	got := claudeHooks(t, root, "SessionEnd")
	if len(got) != 2 {
		t.Fatalf("handler = %d개, want 2 (사용자 것 + 우리 것): %#v", len(got), got)
	}
	var kept bool
	for _, h := range got {
		if u, _ := h["url"].(string); u == remote["url"] {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("남의 서버로 향하는 사용자 훅을 지웠다: %#v", got)
	}

	// disable 에서도 마찬가지다.
	mergeClaudeHooks(root, "https://collector.example.com", "tok-1", false)
	got = claudeHooks(t, root, "SessionEnd")
	if len(got) != 1 || got[0]["url"] != remote["url"] {
		t.Fatalf("disable 후 = %#v, want 사용자 훅만", got)
	}
}
