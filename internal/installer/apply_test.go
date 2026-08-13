package installer

// enroll 시 로컬 파이프라인 자동 배선 (PROJ-45, ADR 0006).
//
// 여기 테스트가 지키는 것은 셋이다.
//
//	1. 신규 enroll 은 로컬로 배선되고, 회사 토큰이 키링으로 대피한다.
//	2. 배선하지 못하는 경우(ingest 토큰 없음·grpc)는 조용히 실패하지 않고 회사 직결로 강등한다.
//	3. 기존 설치자의 수동 경로(local disable → enable)가 신규 enroll 과 같은 결과를 만든다.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/credential"
)

// newEnrollFixture 는 enroll 을 한 번 돌린 상태를 만든다. ingestTok 이 비어 있으면
// PROJ-45 이전과 같은 회사 직결 설치가 되고, 그것이 곧 "기존 설치자" 상태다.
func newEnrollFixture(t *testing.T, m contract.Manifest, ingestTok string) (localFixture, *Report) {
	t.Helper()
	keyring.MockInit()

	dir := t.TempDir()
	f := localFixture{
		statePath:  filepath.Join(dir, "state.json"),
		backupDir:  filepath.Join(dir, "backups"),
		claudePath: filepath.Join(dir, "claude", "settings.json"),
		codexPath:  filepath.Join(dir, "codex", "config.toml"),
	}
	mustWrite(t, f.claudePath, `{
  "model": "opus",
  "env": {"MY_OWN_KEY": "keep-me"}
}
`)
	mustWrite(t, f.codexPath, "model = \"gpt-5\"\n")

	rep, err := Apply(&contract.Enrollment{
		InstallationID:    "inst_local",
		InstallationToken: "pit_secret",
		TelemetryToken:    companyToken,
		Manifest:          m,
	}, Options{
		ClaudePath:  f.claudePath,
		CodexPath:   f.codexPath,
		StatePath:   f.statePath,
		BackupDir:   f.backupDir,
		ServerURL:   "https://enroll.example.com",
		IngestToken: ingestTok,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return f, rep
}

// claudeEnvOf 는 병합된 settings.json 의 env 표를 꺼낸다.
func claudeEnvOf(t *testing.T, path string) map[string]string {
	t.Helper()
	var root struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(mustRead(t, path), &root); err != nil {
		t.Fatalf("settings.json 파싱: %v", err)
	}
	return root.Env
}

// TestApply는로컬을기본배선한다 는 opt-out 전환의 본체다.
//
// 사용자가 `local enable` 을 치지 않았는데도 벤더 설정이 로컬 수신기를 가리켜야 한다.
func TestApply는로컬을기본배선한다(t *testing.T) {
	f, rep := newEnrollFixture(t, httpManifest(), ingestToken)

	if !rep.LocalEnabled {
		t.Fatal("Report.LocalEnabled 가 false — enroll 이 로컬로 배선하지 않았다")
	}
	if rep.Endpoint != "http://localhost:4318" {
		t.Errorf("Report.Endpoint = %q, want http://localhost:4318", rep.Endpoint)
	}

	state, err := LoadState(f.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !state.Local.Enabled {
		t.Error("state.Local.Enabled 가 false — 데몬과 local disable 이 상태를 잘못 읽는다")
	}
	if state.Local.ListenPort != DefaultLocalPort {
		t.Errorf("state.Local.ListenPort = %d, want %d", state.Local.ListenPort, DefaultLocalPort)
	}
	// 원문 보관 기본 ON 은 PROJ-45 가 건드리지 않는다 (ADR 0003).
	if !state.Local.StoreContent || state.Local.RetentionDays != DefaultRetentionDays {
		t.Errorf("Local 블록의 나머지 기본값이 바뀌었다: %+v", state.Local)
	}

	// state 에는 **회사 manifest 원본**이 남아야 한다. 고정 프로필이 저장되면 데몬이
	// 그것으로 포워더의 signals·privacy 기준을 세워 집행이 통째로 무력화된다.
	want := httpManifest()
	if state.Manifest.OTLP != want.OTLP {
		t.Errorf("state.Manifest.OTLP 가 고정 프로필로 덮였다: %+v", state.Manifest.OTLP)
	}
	if state.Manifest.Signals != want.Signals {
		t.Errorf("state.Manifest.Signals 가 덮였다: %+v", state.Manifest.Signals)
	}
	if state.Manifest.Privacy != want.Privacy {
		t.Errorf("state.Manifest.Privacy 가 덮였다: %+v", state.Manifest.Privacy)
	}

	env := claudeEnvOf(t, f.claudePath)
	if got := env["OTEL_EXPORTER_OTLP_ENDPOINT"]; got != "http://localhost:4318" {
		t.Errorf("endpoint = %q, want localhost:4318", got)
	}
	if got := env["OTEL_EXPORTER_OTLP_HEADERS"]; got != "Authorization=Bearer "+ingestToken+",X-Pulsemetry-Local=1" {
		t.Errorf("headers = %q — ingest 토큰과 로컬 헤더가 함께 있어야 한다", got)
	}
	if env["MY_OWN_KEY"] != "keep-me" {
		t.Error("사용자가 직접 넣은 키가 사라졌다")
	}
}

// TestApply는회사토큰을키링에대피시킨다 는 local disable 의 유일한 복귀 경로를 지킨다.
//
// 벤더 설정에는 이제 로컬 ingest 토큰만 적힌다. 여기서 대피시키지 않으면 회사 토큰의
// 사본이 어디에도 없고, `local disable` 이 "대피본을 찾지 못했다" 로 실패한다.
func TestApply는회사토큰을키링에대피시킨다(t *testing.T) {
	f, _ := newEnrollFixture(t, httpManifest(), ingestToken)

	got, found, err := credential.Get(credential.AccountTelemetry)
	if err != nil {
		t.Fatalf("credential.Get: %v", err)
	}
	if !found || got != companyToken {
		t.Fatalf("대피본 = %q (있음=%v), want %q", got, found, companyToken)
	}

	// 회사 토큰이 벤더 설정에 남아 있으면 배선이 절반만 된 것이다.
	claude := string(mustRead(t, f.claudePath))
	codex := string(mustRead(t, f.codexPath))
	if strings.Contains(claude, companyToken) || strings.Contains(codex, companyToken) {
		t.Error("배선 후에도 회사 telemetry token 이 벤더 설정에 남아 있다")
	}

	// 실제로 되돌아가는지까지 본다. 대피본만 있고 disable 이 실패하면 의미가 없다.
	rep, err := DisableLocal(LocalOptions{StatePath: f.statePath, BackupDir: f.backupDir})
	if err != nil {
		t.Fatalf("DisableLocal: %v", err)
	}
	if rep.AlreadyInState {
		t.Fatal("disable 이 '이미 꺼져 있음' 으로 끝났다 — enroll 이 상태를 켜지 않았다")
	}
	env := claudeEnvOf(t, f.claudePath)
	if got := env["OTEL_EXPORTER_OTLP_ENDPOINT"]; got != httpManifest().OTLP.Endpoint {
		t.Errorf("disable 후 endpoint = %q, want 회사 endpoint", got)
	}
	if got := env["OTEL_EXPORTER_OTLP_HEADERS"]; got != "Authorization=Bearer "+companyToken {
		t.Errorf("disable 후 headers = %q, want 회사 토큰 단독", got)
	}
	// 로컬 전용 키가 남으면 회사 직결 상태에 로컬 흔적이 섞인다.
	if _, ok := env["CLAUDE_CODE_ENHANCED_TELEMETRY_BETA"]; ok {
		t.Error("disable 후에도 CLAUDE_CODE_ENHANCED_TELEMETRY_BETA 가 남아 있다")
	}
}

// TestApply는grpc면회사직결로설치한다 — 배선 실패가 설치 실패가 되면 안 된다.
//
// forward 가 grpc 상위 전달을 못 하므로 배선하면 로컬에만 쌓이고 회사에는 아무것도 가지
// 않는다. 그렇다고 enroll 을 실패시키면 로컬 파이프라인 때문에 설치가 통째로 막힌다.
func TestApply는grpc면회사직결로설치한다(t *testing.T) {
	m := httpManifest()
	m.OTLP.Protocol = "grpc"

	f, rep := newEnrollFixture(t, m, ingestToken)

	if rep.LocalEnabled {
		t.Fatal("grpc 테넌트를 로컬로 배선했다 — 회사로 아무것도 가지 않는다")
	}
	if rep.Endpoint != m.OTLP.Endpoint {
		t.Errorf("Report.Endpoint = %q, want 회사 endpoint", rep.Endpoint)
	}
	state, err := LoadState(f.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Local.Enabled {
		t.Error("state.Local.Enabled 가 true — 상태와 벤더 설정이 어긋난다")
	}
	// 배선하지 않았으면 대피본도 없어야 한다. 남아 있으면 나중에 stashTelemetryToken 이
	// 남의 토큰을 회사 토큰으로 착각한다.
	if _, found, err := credential.Get(credential.AccountTelemetry); err != nil || found {
		t.Errorf("배선하지 않았는데 대피본이 있다 (있음=%v, err=%v)", found, err)
	}
}

// TestApply는ingest토큰이없으면회사직결로설치한다 — 키링을 못 여는 환경의 강등 경로다.
func TestApply는ingest토큰이없으면회사직결로설치한다(t *testing.T) {
	f, rep := newEnrollFixture(t, httpManifest(), "")

	if rep.LocalEnabled {
		t.Fatal("ingest 토큰 없이 로컬로 배선했다 — 수신기 인증을 통과할 수 없다")
	}
	state, err := LoadState(f.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Local.Enabled {
		t.Error("state.Local.Enabled 가 true")
	}
	env := claudeEnvOf(t, f.claudePath)
	if got := env["OTEL_EXPORTER_OTLP_ENDPOINT"]; got != httpManifest().OTLP.Endpoint {
		t.Errorf("endpoint = %q, want 회사 endpoint", got)
	}
	// 회사 직결이면 벤더 설정에 회사 토큰이 적혀 있어야 한다 (그것이 진실원이다).
	if got := env["OTEL_EXPORTER_OTLP_HEADERS"]; got != "Authorization=Bearer "+companyToken {
		t.Errorf("headers = %q, want 회사 토큰", got)
	}
}

// TestEnroll배선과enable배선이같은설정을만든다 는 두 경로의 등가성을 못박는다.
//
// 기존 설치자가 `local enable` 로 전환했을 때 나오는 설정이 신규 enroll 과 달라지면,
// "회사 manifest 로 파생된 옛 로컬 설정" 이 그 경로로만 남는다.
func TestEnroll배선과enable배선이같은설정을만든다(t *testing.T) {
	tests := []struct {
		name string
		m    contract.Manifest
	}{
		{name: "좁은 회사 manifest", m: httpManifest()},
		{name: "넓은 회사 manifest", m: wideManifest()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// ⓐ 신규 enroll — 처음부터 로컬로 배선된다.
			enrolled, rep := newEnrollFixture(t, tt.m, ingestToken)
			if !rep.LocalEnabled {
				t.Fatal("enroll 이 배선하지 않았다")
			}

			// ⓑ 기존 설치자 — 회사 직결로 설치한 뒤 수동으로 켠다.
			existing, rep2 := newEnrollFixture(t, tt.m, "")
			if rep2.LocalEnabled {
				t.Fatal("픽스처 전제가 깨졌다: ingest 토큰 없이 배선됐다")
			}
			if _, err := EnableLocal(LocalOptions{
				StatePath:   existing.statePath,
				BackupDir:   existing.backupDir,
				IngestToken: ingestToken,
			}); err != nil {
				t.Fatalf("EnableLocal: %v", err)
			}

			// 벤더 설정은 바이트 단위로 같아야 한다.
			if a, b := string(mustRead(t, enrolled.claudePath)), string(mustRead(t, existing.claudePath)); a != b {
				t.Errorf("두 경로의 settings.json 이 다르다:\n--- enroll ---\n%s\n--- enable ---\n%s", a, b)
			}
			if a, b := string(mustRead(t, enrolled.codexPath)), string(mustRead(t, existing.codexPath)); a != b {
				t.Errorf("두 경로의 config.toml 이 다르다:\n--- enroll ---\n%s\n--- enable ---\n%s", a, b)
			}
		})
	}
}

// TestExisting설치자의disable후enable 는 바이너리만 교체한 사용자의 수동 경로를 본다.
//
// ①disable 은 no-op, ②enable 은 벤더 파일에서 회사 토큰을 되읽어 대피, ③다시 disable 하면
// 회사 설정으로 정확히 복귀하고 로컬 전용 키가 잔재 없이 사라진다.
func TestExisting설치자의disable후enable(t *testing.T) {
	f, rep := newEnrollFixture(t, httpManifest(), "") // 회사 직결 = 기존 설치자
	if rep.LocalEnabled {
		t.Fatal("픽스처 전제가 깨졌다")
	}
	before := string(mustRead(t, f.claudePath))

	// ① 이미 꺼져 있으므로 아무것도 하지 않는다.
	off, err := DisableLocal(LocalOptions{StatePath: f.statePath, BackupDir: f.backupDir})
	if err != nil {
		t.Fatalf("DisableLocal: %v", err)
	}
	if !off.AlreadyInState {
		t.Error("AlreadyInState 가 false — 꺼진 설치를 다시 병합하려 했다")
	}
	if got := string(mustRead(t, f.claudePath)); got != before {
		t.Error("no-op disable 이 벤더 설정을 건드렸다")
	}

	// ② 켠다. 회사 토큰은 벤더 파일에서 되읽어 대피시켜야 한다 (stashTelemetryToken).
	on, err := EnableLocal(LocalOptions{
		StatePath:   f.statePath,
		BackupDir:   f.backupDir,
		IngestToken: ingestToken,
	})
	if err != nil {
		t.Fatalf("EnableLocal: %v", err)
	}
	if !on.TelemetryTokenStashed {
		t.Fatal("회사 토큰을 대피시키지 못했다 — disable 이 되돌리지 못한다")
	}
	stashed, found, err := credential.Get(credential.AccountTelemetry)
	if err != nil || !found || stashed != companyToken {
		t.Fatalf("대피본 = %q (있음=%v, err=%v), want %q", stashed, found, err, companyToken)
	}
	if got := claudeEnvOf(t, f.claudePath)["OTEL_EXPORTER_OTLP_ENDPOINT"]; got != "http://localhost:4318" {
		t.Errorf("enable 후 endpoint = %q", got)
	}

	// ③ 되돌린다.
	if _, err := DisableLocal(LocalOptions{StatePath: f.statePath, BackupDir: f.backupDir}); err != nil {
		t.Fatalf("DisableLocal: %v", err)
	}
	env := claudeEnvOf(t, f.claudePath)
	if got := env["OTEL_EXPORTER_OTLP_ENDPOINT"]; got != httpManifest().OTLP.Endpoint {
		t.Errorf("복귀 후 endpoint = %q, want 회사 endpoint", got)
	}
	if got := env["OTEL_EXPORTER_OTLP_HEADERS"]; got != "Authorization=Bearer "+companyToken {
		t.Errorf("복귀 후 headers = %q, want 회사 토큰 단독", got)
	}
	// 로컬 전용 키가 전부 사라져야 한다. 하나라도 남으면 회사 직결 상태에서도 Codex 가
	// localhost 로 계속 보내거나 Claude 가 확장 텔레메트리를 켠 채로 남는다.
	if _, ok := env["CLAUDE_CODE_ENHANCED_TELEMETRY_BETA"]; ok {
		t.Error("CLAUDE_CODE_ENHANCED_TELEMETRY_BETA 가 남았다")
	}
	codex := string(mustRead(t, f.codexPath))
	for _, key := range []string{"metrics_exporter", "trace_exporter", "localhost"} {
		if strings.Contains(codex, key) {
			t.Errorf("복귀 후 config.toml 에 %q 가 남았다:\n%s", key, codex)
		}
	}
}

// TestReconnect는로컬배선을덮지않는다 — 토큰 재발급 한 번에 재배선이 풀리면 안 된다.
//
// 벤더 설정에 적혀 있는 것은 로컬 ingest 토큰이다. 여기에 회사 토큰을 쓰면 endpoint 도
// 회사 것으로 함께 돌아가고, state.Local.Enabled 는 true 로 남아 상태와 현실이 갈린다.
func TestReconnect는로컬배선을덮지않는다(t *testing.T) {
	f, rep := newEnrollFixture(t, httpManifest(), ingestToken)
	if !rep.LocalEnabled {
		t.Fatal("픽스처 전제가 깨졌다: enroll 이 배선하지 않았다")
	}
	claudeBefore := string(mustRead(t, f.claudePath))
	codexBefore := string(mustRead(t, f.codexPath))

	const rotated = "company-telemetry-token-rotated"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pit_secret" {
			t.Errorf("Authorization = %q, want 설치 자격증명", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"installation_id":"inst_local","telemetry_token":"` + rotated + `"}`))
	}))
	defer srv.Close()

	out, err := Reconnect(f.statePath, srv.URL)
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if !out.LocalEnabled {
		t.Error("Report.LocalEnabled 가 false")
	}

	// 벤더 설정은 한 바이트도 바뀌면 안 된다.
	if got := string(mustRead(t, f.claudePath)); got != claudeBefore {
		t.Errorf("reconnect 가 settings.json 을 바꿨다 — 재배선이 풀렸다:\n%s", got)
	}
	if got := string(mustRead(t, f.codexPath)); got != codexBefore {
		t.Errorf("reconnect 가 config.toml 을 바꿨다:\n%s", got)
	}

	// 새 회사 토큰은 대피본에만 반영된다. 이 값이 곧 disable 이 쓸 값이다.
	stashed, found, err := credential.Get(credential.AccountTelemetry)
	if err != nil || !found || stashed != rotated {
		t.Fatalf("대피본 = %q (있음=%v, err=%v), want %q", stashed, found, err, rotated)
	}

	// disable 하면 **새** 토큰으로 회사 설정에 복귀해야 한다. 낡은 토큰으로 돌아가면
	// 재발급을 한 의미가 없다.
	if _, err := DisableLocal(LocalOptions{StatePath: f.statePath, BackupDir: f.backupDir}); err != nil {
		t.Fatalf("DisableLocal: %v", err)
	}
	if got := claudeEnvOf(t, f.claudePath)["OTEL_EXPORTER_OTLP_HEADERS"]; got != "Authorization=Bearer "+rotated {
		t.Errorf("복귀 후 headers = %q, want 새 회사 토큰", got)
	}
}

// wideManifest 는 httpManifest 와 정반대인 회사 manifest 다. 고정 프로필이 회사 값에
// 영향받지 않는지 두 극단으로 확인하는 데 쓴다.
func wideManifest() contract.Manifest {
	m := httpManifest()
	m.OTLP.Compression = ""
	m.OTLP.TimeoutMS = 30000
	m.Signals = contract.Signals{Logs: true, Metrics: true, Traces: true}
	m.Privacy = contract.Privacy{
		CollectUserPrompts:        true,
		CollectAssistantResponses: true,
		CollectToolDetails:        true,
		CollectToolContent:        true,
		CollectRawAPIBodies:       true,
	}
	return m
}
