package installer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/credential"
)

const (
	companyToken = "company-telemetry-token"
	ingestToken  = "local-ingest-token"
)

// httpManifest 는 회사 manifest 다. Privacy 는 §4.6 대로 전부 false 이고,
// 슬라이스·맵 필드를 채워 두어 깊은 복사 여부를 검사할 수 있게 한다.
func httpManifest() contract.Manifest {
	return contract.Manifest{
		SchemaVersion:  1,
		ConfigRevision: 9,
		OTLP: contract.OTLP{
			Endpoint:    "https://collector.example.com",
			Protocol:    "http/protobuf",
			Compression: "gzip",
			TimeoutMS:   5000,
		},
		Signals:             contract.Signals{Logs: true, Metrics: true, Traces: false},
		Privacy:             contract.Privacy{},
		RepositoryAllowlist: []string{"github.com/your-org/telemetryctl"},
		ResourceAttributes:  map[string]string{"deployment.environment": "production"},
	}
}

// ---------------------------------------------------------------------------
// localManifest — 이 단계의 가장 중요한 불변식
// ---------------------------------------------------------------------------

// TestLocalManifestDoesNotMutateCompany 는 로컬 사본을 만든 뒤 회사 manifest 원본이
// 한 글자도 바뀌지 않았음을 단언한다.
//
// 이것이 이 티켓 전체에서 가장 중요한 테스트다. 회사 manifest 는 포워더가 "무엇을 지우고
// 보낼지" 판단하는 진실원이라, 여기서 Privacy 플래그가 켜지면 포워더가 프롬프트 원문을
// 그대로 회사로 보낸다 (ADR 0003 위반).
func TestLocalManifestDoesNotMutateCompany(t *testing.T) {
	company := httpManifest()
	before := httpManifest()

	local, err := localManifest(&company, 4318)
	if err != nil {
		t.Fatalf("localManifest: %v", err)
	}

	if company.Privacy != before.Privacy {
		t.Errorf("회사 manifest 의 Privacy 가 바뀌었다: %+v (want %+v)", company.Privacy, before.Privacy)
	}
	if company.OTLP != before.OTLP {
		t.Errorf("회사 manifest 의 OTLP 가 바뀌었다: %+v (want %+v)", company.OTLP, before.OTLP)
	}
	if company.Signals != before.Signals {
		t.Errorf("회사 manifest 의 Signals 가 바뀌었다: %+v", company.Signals)
	}

	// 얕은 복사면 슬라이스·맵의 backing store 가 공유된다. 사본을 통해 원본이 바뀌는지
	// 실제로 써 보고 확인한다 — 필드를 눈으로 비교하는 것만으로는 잡히지 않는다.
	local.RepositoryAllowlist[0] = "mutated"
	local.ResourceAttributes["deployment.environment"] = "mutated"
	if company.RepositoryAllowlist[0] != before.RepositoryAllowlist[0] {
		t.Errorf("RepositoryAllowlist 가 얕게 복사됐다: 원본 = %v", company.RepositoryAllowlist)
	}
	if company.ResourceAttributes["deployment.environment"] != "production" {
		t.Errorf("ResourceAttributes 가 얕게 복사됐다: 원본 = %v", company.ResourceAttributes)
	}
}

// TestLocalManifestForcesContentGates 는 계획서 §2 의 강제를 못박는다.
func TestLocalManifestForcesContentGates(t *testing.T) {
	company := httpManifest()
	local, err := localManifest(&company, 4319)
	if err != nil {
		t.Fatalf("localManifest: %v", err)
	}

	if !local.Privacy.CollectUserPrompts {
		t.Error("collect_user_prompts 가 강제되지 않았다 — 작업 제목·요약을 만들 수 없다")
	}
	if !local.Privacy.CollectToolDetails {
		t.Error("collect_tool_details 가 강제되지 않았다 — 파일 변경 목록·툴 타임라인을 만들 수 없다")
	}

	// 강제 대상이 아닌 것은 회사 값을 그대로 따라야 한다. 여기서 하나라도 켜지면
	// 회사로 나가는 데이터가 재배선 전후로 달라진다.
	if local.Privacy.CollectAssistantResponses || local.Privacy.CollectToolContent ||
		local.Privacy.CollectRawAPIBodies || local.Privacy.CollectUserEmail {
		t.Errorf("강제 대상이 아닌 Privacy 항목이 켜졌다: %+v", local.Privacy)
	}
	if local.Signals != company.Signals {
		t.Errorf("Signals 가 바뀌었다: %+v, want %+v — 회사가 끈 시그널을 새로 보내면 안 된다",
			local.Signals, company.Signals)
	}
}

// TestLocalManifestEndpoint 는 endpoint 표기와 포트 경계를 본다.
func TestLocalManifestEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		port     int
		wantErr  bool
		endpoint string
	}{
		{name: "기본 포트", port: DefaultLocalPort, endpoint: "http://localhost:4318"},
		{name: "폴백 포트", port: 51234, endpoint: "http://localhost:51234"},
		{name: "0 은 거부", port: 0, wantErr: true},
		{name: "음수는 거부", port: -1, wantErr: true},
		{name: "65536 은 거부", port: 65536, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			company := httpManifest()
			local, err := localManifest(&company, tt.port)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("포트 %d 가 통과했다", tt.port)
				}
				return
			}
			if err != nil {
				t.Fatalf("localManifest: %v", err)
			}
			if local.OTLP.Endpoint != tt.endpoint {
				t.Errorf("endpoint = %q, want %q", local.OTLP.Endpoint, tt.endpoint)
			}
			if strings.Contains(local.OTLP.Endpoint, "127.0.0.1") {
				t.Errorf("endpoint 에 127.0.0.1 이 있다: %q — 계약 검증에서 거부된다", local.OTLP.Endpoint)
			}
			// 로컬 사본도 공유 계약을 통과해야 한다.
			if err := local.Validate(); err != nil {
				t.Errorf("로컬 manifest 가 계약 검증을 통과하지 못한다: %v", err)
			}
		})
	}
}

// TestLocalManifestRejectsGRPC 는 grpc manifest 거부를 확인한다 (계획서 「확정된 결정」).
func TestLocalManifestRejectsGRPC(t *testing.T) {
	company := httpManifest()
	company.OTLP.Protocol = "grpc"
	if _, err := localManifest(&company, DefaultLocalPort); !errors.Is(err, ErrGRPCUnsupported) {
		t.Fatalf("오류 = %v, want ErrGRPCUnsupported", err)
	}
}

// TestLocalManifestClearsUnsupportedCompression 은 수신기가 풀 수 없는 압축을 로컬
// 구간에서만 끄는지 본다. 그대로 두면 모든 배치가 415 로 조용히 사라진다.
func TestLocalManifestClearsUnsupportedCompression(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "gzip 은 유지", in: "gzip", want: "gzip"},
		{name: "빈 값은 유지", in: "", want: ""},
		{name: "zstd 는 로컬에서 끔", in: "zstd", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			company := httpManifest()
			company.OTLP.Compression = tt.in
			local, err := localManifest(&company, DefaultLocalPort)
			if err != nil {
				t.Fatalf("localManifest: %v", err)
			}
			if local.OTLP.Compression != tt.want {
				t.Errorf("compression = %q, want %q", local.OTLP.Compression, tt.want)
			}
			if company.OTLP.Compression != tt.in {
				t.Errorf("회사 manifest 의 compression 이 바뀌었다: %q", company.OTLP.Compression)
			}
		})
	}
}

func TestLocalEndpointMatchesReceiverFormat(t *testing.T) {
	if got := LocalEndpoint(4318); got != "http://localhost:4318" {
		t.Errorf("LocalEndpoint(4318) = %q", got)
	}
}

// ---------------------------------------------------------------------------
// enable → disable 왕복
// ---------------------------------------------------------------------------

// localFixture 는 enroll 이 끝난 상태를 만든다. 실제 Apply 를 쓴다 — 손으로 만든 상태로
// 테스트하면 "Apply 가 만드는 것과 다른 모양" 이라는 부류의 버그가 통째로 빠져나간다.
type localFixture struct {
	statePath  string
	backupDir  string
	claudePath string
	codexPath  string
}

func newLocalFixture(t *testing.T, m contract.Manifest) localFixture {
	t.Helper()
	keyring.MockInit()

	dir := t.TempDir()
	f := localFixture{
		statePath:  filepath.Join(dir, "state.json"),
		backupDir:  filepath.Join(dir, "backups"),
		claudePath: filepath.Join(dir, "claude", "settings.json"),
		codexPath:  filepath.Join(dir, "codex", "config.toml"),
	}

	// 사용자가 이미 갖고 있던 설정. 재배선이 이것을 건드리면 안 된다.
	mustWrite(t, f.claudePath, `{
  "model": "opus",
  "env": {"MY_OWN_KEY": "keep-me"}
}
`)
	mustWrite(t, f.codexPath, "model = \"gpt-5\"\n")

	if _, err := Apply(&contract.Enrollment{
		InstallationID:    "inst_local",
		InstallationToken: "pit_secret",
		TelemetryToken:    companyToken,
		Manifest:          m,
	}, Options{
		ClaudePath: f.claudePath,
		CodexPath:  f.codexPath,
		StatePath:  f.statePath,
		BackupDir:  f.backupDir,
		ServerURL:  "https://enroll.example.com",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return f
}

func (f localFixture) options() LocalOptions {
	return LocalOptions{StatePath: f.statePath, BackupDir: f.backupDir, IngestToken: ingestToken}
}

func (f localFixture) snapshot(t *testing.T) (claude, codex []byte) {
	t.Helper()
	return mustRead(t, f.claudePath), mustRead(t, f.codexPath)
}

// TestEnableDisableRoundTripRestoresConfigs 는 이 명령의 탈출구를 검증한다.
// enable → disable 이 두 벤더 설정을 **바이트 단위로** 원래대로 돌려놓아야 한다.
func TestEnableDisableRoundTripRestoresConfigs(t *testing.T) {
	f := newLocalFixture(t, httpManifest())
	beforeClaude, beforeCodex := f.snapshot(t)

	enable, err := EnableLocal(f.options())
	if err != nil {
		t.Fatalf("EnableLocal: %v", err)
	}
	if !enable.TelemetryTokenStashed {
		t.Fatal("회사 telemetry token 을 대피시키지 못했다 — disable 이 오프라인으로 되돌리지 못한다")
	}
	afterEnableClaude, afterEnableCodex := f.snapshot(t)
	if bytes.Equal(beforeClaude, afterEnableClaude) {
		t.Fatal("enable 이 Claude 설정을 바꾸지 않았다")
	}
	if bytes.Equal(beforeCodex, afterEnableCodex) {
		t.Fatal("enable 이 Codex 설정을 바꾸지 않았다")
	}

	disable, err := DisableLocal(LocalOptions{StatePath: f.statePath, BackupDir: f.backupDir})
	if err != nil {
		t.Fatalf("DisableLocal: %v", err)
	}
	if disable.Enabled || disable.AlreadyInState {
		t.Errorf("disable 보고가 이상하다: %+v", disable)
	}

	restoredClaude, restoredCodex := f.snapshot(t)
	if !bytes.Equal(beforeClaude, restoredClaude) {
		t.Errorf("Claude 설정이 원래대로 돌아오지 않았다:\n--- before ---\n%s\n--- after ---\n%s",
			beforeClaude, restoredClaude)
	}
	if !bytes.Equal(beforeCodex, restoredCodex) {
		t.Errorf("Codex 설정이 원래대로 돌아오지 않았다:\n--- before ---\n%s\n--- after ---\n%s",
			beforeCodex, restoredCodex)
	}

	state, err := LoadState(f.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Local.Enabled {
		t.Error("state.Local.Enabled = true, want false")
	}
	if _, found, _ := credential.Get(credential.AccountTelemetry); found {
		t.Error("disable 후에도 회사 토큰 대피본이 키링에 남아 있다")
	}
}

// TestEnableRewiresVendorConfigs 는 벤더 설정에 실제로 무엇이 적히는지 확인한다.
func TestEnableRewiresVendorConfigs(t *testing.T) {
	f := newLocalFixture(t, httpManifest())

	report, err := EnableLocal(f.options())
	if err != nil {
		t.Fatalf("EnableLocal: %v", err)
	}
	if report.Endpoint != "http://localhost:4318" || report.ListenPort != DefaultLocalPort {
		t.Errorf("보고 = %+v, want localhost:4318", report)
	}

	claude := string(mustRead(t, f.claudePath))
	codex := string(mustRead(t, f.codexPath))

	for _, want := range []string{
		`"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318"`,
		// 로컬 수신기의 3중 인증은 토큰과 X-Pulsemetry-Local 을 AND 로 묶는다
		// (receiver/auth.go). 둘째 쌍이 빠지면 모든 배치가 401 로 사라진다.
		`"OTEL_EXPORTER_OTLP_HEADERS": "Authorization=Bearer ` + ingestToken + `,X-Pulsemetry-Local=1"`,
		`"OTEL_LOG_USER_PROMPTS": "1"`,
		`"OTEL_LOG_TOOL_DETAILS": "1"`,
		`"OTEL_LOG_ASSISTANT_RESPONSES": "0"`,
		`"OTEL_LOG_TOOL_CONTENT": "0"`,
		`"OTEL_LOG_RAW_API_BODIES": "0"`,
		`"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE": "delta"`,
		`"OTEL_METRIC_EXPORT_INTERVAL"`,
		`"OTEL_LOGS_EXPORT_INTERVAL"`,
		`"MY_OWN_KEY": "keep-me"`,
	} {
		if !strings.Contains(claude, want) {
			t.Errorf("Claude 설정에 %s 가 없다:\n%s", want, claude)
		}
	}
	// 회사 토큰이 벤더 설정에 남아 있으면 재배선이 절반만 된 것이다.
	if strings.Contains(claude, companyToken) || strings.Contains(codex, companyToken) {
		t.Error("재배선 후에도 회사 telemetry token 이 벤더 설정에 남아 있다")
	}
	// 127.0.0.1 은 어느 파일에도 없어야 한다 (계획서 제약 §1).
	for name, body := range map[string]string{"claude": claude, "codex": codex} {
		if strings.Contains(body, "127.0.0.1") {
			t.Errorf("%s 설정에 127.0.0.1 이 있다:\n%s", name, body)
		}
		if !strings.Contains(body, "localhost:4318") {
			t.Errorf("%s 설정이 localhost:4318 을 가리키지 않는다:\n%s", name, body)
		}
	}
	// Codex 도 같은 수신기를 향하므로 같은 헤더가 필요하다. TOML 은 헤더가 표라서
	// 표기가 다를 뿐이다.
	if !strings.Contains(codex, `X-Pulsemetry-Local = "1"`) {
		t.Errorf("Codex 설정에 로컬 ingest 헤더가 없다 — 배치가 401 로 사라진다:\n%s", codex)
	}
	if !strings.Contains(codex, "log_user_prompt = true") {
		t.Errorf("Codex 의 log_user_prompt 가 켜지지 않았다:\n%s", codex)
	}
	if !strings.Contains(codex, `model = "gpt-5"`) {
		t.Errorf("Codex 의 사용자 설정이 사라졌다:\n%s", codex)
	}
}

// TestEnableDoesNotMutateStoredCompanyManifest 는 상태 파일에 남는 회사 manifest 가
// 재배선 후에도 그대로인지 본다. 데몬의 포워더가 이 값을 제거 기준으로 쓴다.
func TestEnableDoesNotMutateStoredCompanyManifest(t *testing.T) {
	f := newLocalFixture(t, httpManifest())
	want := httpManifest()

	if _, err := EnableLocal(f.options()); err != nil {
		t.Fatalf("EnableLocal: %v", err)
	}

	state, err := LoadState(f.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	got := state.Manifest
	if got.OTLP != want.OTLP {
		t.Errorf("저장된 회사 OTLP = %+v, want %+v", got.OTLP, want.OTLP)
	}
	if got.Privacy != want.Privacy {
		t.Errorf("저장된 회사 Privacy = %+v, want %+v — 회사로 나가는 데이터 기준이 오염됐다",
			got.Privacy, want.Privacy)
	}
	if !state.Local.Enabled || state.Local.ListenPort != DefaultLocalPort {
		t.Errorf("state.Local = %+v, want Enabled=true ListenPort=4318", state.Local)
	}
}

// TestEnableUpdatesManagedKeys 는 재배선이 state.Targets 의 관리 키를 갱신하는지 본다.
// 갱신하지 않으면 나중에 uninstall 이 새로 추가된 키를 남긴다 (§5.2).
func TestEnableUpdatesManagedKeys(t *testing.T) {
	f := newLocalFixture(t, httpManifest())
	if _, err := EnableLocal(f.options()); err != nil {
		t.Fatalf("EnableLocal: %v", err)
	}
	state, err := LoadState(f.statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	for _, target := range state.Targets {
		if target.Tool != "claude" {
			continue
		}
		for _, key := range []string{
			"env.OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE",
			"env.OTEL_METRIC_EXPORT_INTERVAL",
			"env.OTEL_LOGS_EXPORT_INTERVAL",
		} {
			if !containsString(target.ManagedKeys, key) {
				t.Errorf("state.Targets 의 관리 키에 %s 가 없다: %v", key, target.ManagedKeys)
			}
		}
		// 백업 경로는 enroll 직전의 원본을 계속 가리켜야 한다.
		if target.BackupPath == "" {
			t.Error("enroll 시점 백업 경로가 사라졌다")
		}
	}
}

// TestEnableIsIdempotentAndRemergesPort 는 포트 폴백 재병합 경로를 검증한다.
// 이미 켜진 상태에서 다른 포트로 다시 부르면 그 포트로 다시 쓰고, 회사 토큰 대피본은
// 로컬 ingest 토큰으로 덮이지 않아야 한다.
func TestEnableIsIdempotentAndRemergesPort(t *testing.T) {
	f := newLocalFixture(t, httpManifest())
	beforeClaude, beforeCodex := f.snapshot(t)

	if _, err := EnableLocal(f.options()); err != nil {
		t.Fatalf("첫 EnableLocal: %v", err)
	}

	opts := f.options()
	opts.Port = 51999
	report, err := EnableLocal(opts)
	if err != nil {
		t.Fatalf("두 번째 EnableLocal: %v", err)
	}
	if report.Endpoint != "http://localhost:51999" {
		t.Errorf("endpoint = %q, want http://localhost:51999", report.Endpoint)
	}
	if !strings.Contains(string(mustRead(t, f.claudePath)), "localhost:51999") {
		t.Error("재병합이 새 포트를 쓰지 않았다")
	}

	stashed, found, err := credential.Get(credential.AccountTelemetry)
	if err != nil || !found {
		t.Fatalf("대피본 조회: found=%v err=%v", found, err)
	}
	if stashed != companyToken {
		t.Fatalf("대피본 = %q, want 회사 토큰 — 재병합이 로컬 토큰으로 덮었다", stashed)
	}

	// 그리고 그 상태에서도 disable 은 원래대로 돌려놓아야 한다.
	if _, err := DisableLocal(LocalOptions{StatePath: f.statePath, BackupDir: f.backupDir}); err != nil {
		t.Fatalf("DisableLocal: %v", err)
	}
	restoredClaude, restoredCodex := f.snapshot(t)
	if !bytes.Equal(beforeClaude, restoredClaude) || !bytes.Equal(beforeCodex, restoredCodex) {
		t.Error("포트를 바꿔 재병합한 뒤 disable 이 원래대로 돌리지 못했다")
	}
}

// TestEnableRejectsGRPCManifest 는 grpc 설치에서 재배선이 거부되고, 그때 벤더 설정이
// 전혀 바뀌지 않는지 본다.
func TestEnableRejectsGRPCManifest(t *testing.T) {
	m := httpManifest()
	m.OTLP.Protocol = "grpc"
	m.OTLP.Compression = ""
	f := newLocalFixture(t, m)
	beforeClaude, beforeCodex := f.snapshot(t)

	if _, err := EnableLocal(f.options()); !errors.Is(err, ErrGRPCUnsupported) {
		t.Fatalf("오류 = %v, want ErrGRPCUnsupported", err)
	}
	afterClaude, afterCodex := f.snapshot(t)
	if !bytes.Equal(beforeClaude, afterClaude) || !bytes.Equal(beforeCodex, afterCodex) {
		t.Error("거부됐는데 벤더 설정이 바뀌었다")
	}
	state, _ := LoadState(f.statePath)
	if state.Local.Enabled {
		t.Error("거부됐는데 state.Local.Enabled 가 켜졌다")
	}
}

// TestLocalCommandsRequireInstall 은 미설치 상태에서 명확히 거부되는지 본다.
func TestLocalCommandsRequireInstall(t *testing.T) {
	keyring.MockInit()
	missing := filepath.Join(t.TempDir(), "state.json")

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "enable", run: func() error {
			_, err := EnableLocal(LocalOptions{StatePath: missing, IngestToken: ingestToken})
			return err
		}},
		{name: "disable", run: func() error {
			_, err := DisableLocal(LocalOptions{StatePath: missing})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if !errors.Is(err, ErrNotInstalled) {
				t.Fatalf("오류 = %v, want ErrNotInstalled", err)
			}
			if !strings.Contains(err.Error(), "enroll") {
				t.Errorf("오류 = %v, want 다음 행동(enroll)을 알려 주는 문구", err)
			}
		})
	}
}

// TestEnableRequiresIngestToken 은 인증 없는 재배선 경로가 없음을 확인한다.
func TestEnableRequiresIngestToken(t *testing.T) {
	f := newLocalFixture(t, httpManifest())
	opts := f.options()
	opts.IngestToken = ""
	if _, err := EnableLocal(opts); err == nil {
		t.Fatal("빈 ingest 토큰으로 재배선이 성공했다")
	}
}

// TestDisableWhenAlreadyDisabled 는 이미 꺼져 있을 때 아무것도 건드리지 않는지 본다.
func TestDisableWhenAlreadyDisabled(t *testing.T) {
	f := newLocalFixture(t, httpManifest())
	beforeClaude, beforeCodex := f.snapshot(t)

	report, err := DisableLocal(LocalOptions{StatePath: f.statePath, BackupDir: f.backupDir})
	if err != nil {
		t.Fatalf("DisableLocal: %v", err)
	}
	if !report.AlreadyInState {
		t.Error("AlreadyInState = false, want true")
	}
	afterClaude, afterCodex := f.snapshot(t)
	if !bytes.Equal(beforeClaude, afterClaude) || !bytes.Equal(beforeCodex, afterCodex) {
		t.Error("이미 꺼진 상태에서 disable 이 설정을 건드렸다")
	}
}

// TestDisableWithoutStashedTokenExplainsRecovery 는 대피본을 잃었을 때의 안내를 본다.
// 이 경로에서 조용히 실패하거나 로컬 토큰을 회사 설정에 써 넣으면 안 된다.
func TestDisableWithoutStashedTokenExplainsRecovery(t *testing.T) {
	f := newLocalFixture(t, httpManifest())
	if _, err := EnableLocal(f.options()); err != nil {
		t.Fatalf("EnableLocal: %v", err)
	}
	if err := credential.Delete(credential.AccountTelemetry); err != nil {
		t.Fatalf("대피본 삭제: %v", err)
	}
	rewired := mustRead(t, f.claudePath)

	_, err := DisableLocal(LocalOptions{StatePath: f.statePath, BackupDir: f.backupDir})
	if err == nil {
		t.Fatal("대피본이 없는데 disable 이 성공했다")
	}
	if !strings.Contains(err.Error(), "reconnect") {
		t.Errorf("오류 = %v, want 복구 방법(reconnect) 안내", err)
	}
	if !bytes.Equal(rewired, mustRead(t, f.claudePath)) {
		t.Error("실패한 disable 이 설정을 반쯤 바꿔 놓았다")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
