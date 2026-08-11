package installer

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/your-org/pulsemetry/internal/contract"
)

func testManifest() contract.Manifest {
	return contract.Manifest{
		SchemaVersion:  1,
		ConfigRevision: 7,
		OTLP: contract.OTLP{
			Endpoint: "https://collector.example.com",
			Protocol: "grpc",
		},
		Signals: contract.Signals{Logs: true, Metrics: true, Traces: true},
	}
}

func TestSaveLoadStateRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		state State
	}{
		{
			name: "타깃 없는 최소 상태",
			state: State{
				StateSchemaVersion: StateSchemaVersion,
				InstallationID:     "inst_minimal",
				ServerURL:          "https://enroll.example.com",
				ConfigRevision:     1,
				InstallerVersion:   Version,
				InstalledAt:        "2026-01-02T03:04:05Z",
				Manifest:           testManifest(),
			},
		},
		{
			name: "타깃 두 개와 백업 메타데이터",
			state: State{
				StateSchemaVersion: StateSchemaVersion,
				InstallationID:     "inst_full",
				ServerURL:          "https://enroll.example.com",
				ConfigRevision:     7,
				InstallerVersion:   Version,
				InstalledAt:        "2026-01-02T03:04:05Z",
				Manifest:           testManifest(),
				Targets: []Target{
					{
						Tool:           "claude",
						Path:           "/home/u/.claude/settings.json",
						BackupPath:     "/home/u/.local/share/pulsemetry/backups/claude/settings.x.json",
						OriginalSHA256: "abc123",
						ManagedKeys:    []string{"env.OTEL_EXPORTER_OTLP_ENDPOINT", "env.OTEL_EXPORTER_OTLP_HEADERS"},
						Created:        false,
					},
					{
						Tool:        "codex",
						Path:        "/home/u/.codex/config.toml",
						ManagedKeys: []string{"otel.endpoint"},
						Created:     true,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := SaveState(path, &tt.state); err != nil {
				t.Fatalf("SaveState: %v", err)
			}
			got, err := LoadState(path)
			if err != nil {
				t.Fatalf("LoadState: %v", err)
			}
			if got == nil {
				t.Fatal("LoadState = nil, want 저장한 상태")
			}
			if !reflect.DeepEqual(*got, tt.state) {
				t.Errorf("왕복 결과가 다름\n got = %+v\nwant = %+v", *got, tt.state)
			}
		})
	}
}

// AtomicWriteFile 로 옮기면서 상위 디렉터리 생성을 잃지 않았는지 확인한다.
// 최초 설치 시 ~/.pulsemetry 는 존재하지 않는다.
func TestSaveStateCreatesParentDirs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "state.json")
	state := State{StateSchemaVersion: StateSchemaVersion, InstallationID: "inst_1"}

	if err := SaveState(path, &state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got == nil || got.InstallationID != "inst_1" {
		t.Fatalf("LoadState = %+v, want installation_id=inst_1", got)
	}
}

// state.json 에 비밀은 없지만 권한 비트는 기존(0600)을 유지해야 한다.
func TestSaveStateFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 는 unix 권한 비트를 쓰지 않음")
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := SaveState(path, &State{StateSchemaVersion: StateSchemaVersion}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("state.json 권한 = %o, want 600", perm)
	}
}

// 데몬이 상태를 반복 갱신하므로 덮어쓰기가 이전 내용을 남기지 않아야 한다.
func TestSaveStateOverwritesPreviousContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	large := State{
		StateSchemaVersion: StateSchemaVersion,
		InstallationID:     "inst_old",
		Manifest:           testManifest(),
		Targets: []Target{
			{Tool: "claude", Path: "/a", ManagedKeys: []string{"k1", "k2", "k3"}},
			{Tool: "codex", Path: "/b", ManagedKeys: []string{"k4", "k5", "k6"}},
		},
	}
	if err := SaveState(path, &large); err != nil {
		t.Fatalf("SaveState(large): %v", err)
	}

	small := State{StateSchemaVersion: StateSchemaVersion, InstallationID: "inst_new"}
	if err := SaveState(path, &small); err != nil {
		t.Fatalf("SaveState(small): %v", err)
	}

	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !reflect.DeepEqual(*got, small) {
		t.Errorf("덮어쓰기 후 = %+v, want %+v", *got, small)
	}
}

// 임시 파일이 rename 되지 않고 남으면 상태 디렉터리가 오염된다.
func TestSaveStateLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := SaveState(path, &State{StateSchemaVersion: StateSchemaVersion}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("상태 디렉터리 = %v, want [state.json]", names)
	}
}

func TestLoadStateMissingFileReturnsNil(t *testing.T) {
	state, err := LoadState(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("없는 상태 파일: %v", err)
	}
	if state != nil {
		t.Fatalf("LoadState = %+v, want nil", state)
	}
}

func TestLoadStateRejectsCorruptJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(path); err == nil {
		t.Fatal("깨진 state.json 파싱이 성공했음")
	}
}

// 스키마 3 → 4 마이그레이션. 이미 설치된 사용자가 바이너리만 갈아도 재enroll 없이
// 동작해야 하고, 없던 Local 블록은 제로값이 아니라 **명시적 기본값**으로 채워져야 한다.
// 제로값을 그대로 두면 StoreContent 가 false 가 되어 업그레이드만으로 원문 보관이 꺼진다.
func TestMigrateV3AddsLocalDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	v3 := `{
  "state_schema_version": 3,
  "installation_id": "inst_legacy",
  "server_url": "https://enroll.example.com",
  "config_revision": 5,
  "installer_version": "0.0.9",
  "installed_at": "2026-01-02T03:04:05Z",
  "targets": [{"tool":"claude","path":"/x","managed_keys":["k"]}]
}`
	if err := os.WriteFile(path, []byte(v3), 0o600); err != nil {
		t.Fatal(err)
	}

	got, migrated, err := LoadStateMigrated(path)
	if err != nil {
		t.Fatalf("LoadStateMigrated: %v", err)
	}
	if !migrated {
		t.Fatal("migrated = false, want true")
	}
	if got.StateSchemaVersion != StateSchemaVersion {
		t.Errorf("state_schema_version = %d, want %d", got.StateSchemaVersion, StateSchemaVersion)
	}
	if got.InstallationID != "inst_legacy" || got.ConfigRevision != 5 || len(got.Targets) != 1 {
		t.Errorf("기존 필드를 잃었다: %+v", got)
	}
	if want := DefaultLocal(); got.Local != want {
		t.Errorf("Local = %+v, want %+v", got.Local, want)
	}
	if !got.Local.StoreContent {
		t.Error("Local.StoreContent = false — 업그레이드만으로 원문 보관이 꺼졌다")
	}
	if got.Local.Enabled {
		t.Error("Local.Enabled = true — 재배선은 opt-in 이어야 한다")
	}
}

// 이미 4 인 파일은 손대지 않는다. 특히 사용자가 끈 StoreContent 를 되켜면 안 된다.
func TestMigrateLeavesCurrentSchemaAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	optedOut := State{
		StateSchemaVersion: StateSchemaVersion,
		InstallationID:     "inst_1",
		Local:              Local{Enabled: true, ListenPort: 4319, RetentionDays: 7, StoreContent: false},
	}
	if err := SaveState(path, &optedOut); err != nil {
		t.Fatal(err)
	}
	got, migrated, err := LoadStateMigrated(path)
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Error("migrated = true, want false")
	}
	if got.Local != optedOut.Local {
		t.Errorf("Local = %+v, want %+v (사용자 설정이 덮였다)", got.Local, optedOut.Local)
	}
}

// 신버전이 쓴 파일을 구버전이 열었을 때 필드를 지우고 되쓰면 안 된다.
func TestMigrateIgnoresFutureSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	future := State{StateSchemaVersion: StateSchemaVersion + 3, InstallationID: "inst_future"}
	if err := SaveState(path, &future); err != nil {
		t.Fatal(err)
	}
	got, migrated, err := LoadStateMigrated(path)
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Error("미래 버전을 마이그레이션했다")
	}
	if got.StateSchemaVersion != StateSchemaVersion+3 {
		t.Errorf("state_schema_version = %d, want 그대로 유지", got.StateSchemaVersion)
	}
}

// LoadState 는 읽기 명령(status)도 쓰므로 디스크를 건드리면 안 된다.
func TestLoadStateDoesNotWriteBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	raw := []byte(`{"state_schema_version":3,"installation_id":"inst_1"}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(path); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(raw) {
		t.Errorf("LoadState 가 파일을 바꿨다:\n got = %s\nwant = %s", after, raw)
	}
}
