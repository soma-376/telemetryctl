package installer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/config"
	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/credential"
)

func TestSaveManagedRegistrationOwnership(t *testing.T) {
	for _, scenario := range []string{"reregister", "foreign", "orphan"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			target := Target{Tool: "codex", Path: filepath.Join(filepath.Dir(path), "config.toml")}
			if scenario != "orphan" {
				if err := SaveState(path, &State{InstallationID: "old", Targets: []Target{target}}); err != nil {
					t.Fatal(err)
				}
			}
			id := "old"
			if scenario != "reregister" {
				id = "foreign"
			}
			oldHook := config.ManagedEntry{Event: "SessionEnd", Digest: "old-hook"}
			before, err := json.Marshal(ManagedSettings{Version: 1, InstallationID: id,
				Targets: []ManagedTarget{{Tool: target.Tool, Path: target.Path, Entries: []config.ManagedEntry{oldHook}}}})
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(ManagedPath(path), before, 0o600); err != nil {
				t.Fatal(err)
			}
			entry := config.ManagedEntry{Path: []string{"otel", "environment"}, Group: "otel.environment", Digest: "new-value"}
			target.pendingManaged = []config.ManagedEntry{entry}
			target.preserveManagedHooks = true
			state := &State{InstallationID: "new", Targets: []Target{target}}
			if scenario == "foreign" {
				if err = SaveState(path, state); err == nil {
					t.Fatal("다른 설치의 관리 기록을 인수했다")
				}
				got, err := os.ReadFile(ManagedPath(path))
				if err != nil || !bytes.Equal(got, before) {
					t.Fatalf("검증 실패 후 관리 기록 변경: %v", err)
				}
				previous, err := LoadState(path)
				if err != nil || previous == nil || previous.InstallationID != "old" {
					t.Fatalf("검증 실패 후 설치 상태 변경: %v", err)
				}
				return
			}
			if scenario == "orphan" {
				undo, err := saveManaged(path, state)
				if err != nil {
					t.Fatal(err)
				}
				if err = undo(); err != nil {
					t.Fatal(err)
				}
				restored, err := os.ReadFile(ManagedPath(path))
				if err != nil || !bytes.Equal(restored, before) {
					t.Fatalf("고아 기록 원문 복구 실패: %v", err)
				}
			}
			if err = SaveState(path, state); err != nil {
				t.Fatal(err)
			}
			got, err := LoadManaged(path, state)
			if err != nil {
				t.Fatal(err)
			}
			want := []config.ManagedEntry{entry}
			if scenario == "reregister" {
				want = append(want, oldHook)
			}
			if !reflect.DeepEqual(got.entries(target), want) {
				t.Fatalf("새 설치 관리 항목 = %#v, 기대값 = %#v", got.entries(target), want)
			}
			stored, err := LoadState(path)
			if err != nil || stored == nil || stored.InstallationID != "new" {
				t.Fatalf("새 설치 상태 저장 실패: %v", err)
			}
		})
	}
}

// 재등록이 실패해도 이전 설정·상태·자격증명이 함께 남아야 한다.
func TestFailedReenrollKeepsPreviousInstallation(t *testing.T) {
	for _, scenario := range []string{"foreign_record", "corrupt_state"} {
		t.Run(scenario, func(t *testing.T) {
			f, _ := newEnrollFixture(t, httpManifest(), ingestToken)
			beforeClaude := mustRead(t, f.claudePath)
			beforeCodex := mustRead(t, f.codexPath)
			beforeState := mustRead(t, f.statePath)
			beforeManaged := mustRead(t, ManagedPath(f.statePath))
			beforeCred, hadCred, err := credential.Get(credential.AccountInstallation)
			if err != nil || !hadCred {
				t.Fatalf("첫 설치 자격증명 없음: %v", err)
			}
			beforeStash, hadStash, err := credential.Get(credential.AccountTelemetry)
			if err != nil {
				t.Fatal(err)
			}

			// 두 번째 설치가 설정과 키링을 갱신한 뒤 상태 저장에서 실패하도록 만든다.
			if scenario == "foreign_record" {
				var m ManagedSettings
				if err = json.Unmarshal(beforeManaged, &m); err != nil {
					t.Fatal(err)
				}
				m.InstallationID = "inst_foreign"
				broken, err := json.MarshalIndent(m, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(ManagedPath(f.statePath), append(broken, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = os.WriteFile(f.statePath, []byte(`{"installation_id":`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// 주입한 손상까지 포함해 재등록 직전 원문이 보존되는지 비교한다.
			beforeState = mustRead(t, f.statePath)
			beforeManaged = mustRead(t, ManagedPath(f.statePath))

			if _, err = Apply(&contract.Enrollment{
				InstallationID: "inst_second", InstallationToken: "pit_second",
				TelemetryToken: "ptt_second", Manifest: httpManifest(),
			}, Options{ClaudePath: f.claudePath, CodexPath: f.codexPath, StatePath: f.statePath,
				BackupDir: f.backupDir, ServerURL: "https://enroll.example.com", IngestToken: "second-ingest-token"}); err == nil {
				t.Fatal("손상된 설치 기록을 인수했다")
			} else if !strings.Contains(err.Error(), "관리 기록 저장 실패") {
				t.Fatalf("상태 저장 이전의 다른 경로에서 실패했다: %v", err)
			}

			if !bytes.Equal(beforeClaude, mustRead(t, f.claudePath)) || !bytes.Equal(beforeCodex, mustRead(t, f.codexPath)) {
				t.Fatal("실패한 설치가 벤더 설정을 남겼다")
			}
			if !bytes.Equal(beforeState, mustRead(t, f.statePath)) {
				t.Fatal("실패한 설치가 설치 상태를 바꿨다")
			}
			if !bytes.Equal(beforeManaged, mustRead(t, ManagedPath(f.statePath))) {
				t.Fatal("실패한 설치가 관리 기록을 바꿨다")
			}
			if got, found, err := credential.Get(credential.AccountInstallation); err != nil || !found || got != beforeCred {
				t.Fatalf("이전 설치 자격증명 유실: found=%v err=%v", found, err)
			}
			if got, found, err := credential.Get(credential.AccountTelemetry); err != nil || found != hadStash || got != beforeStash {
				t.Fatalf("이전 회사 토큰 대피본 유실: found=%v err=%v", found, err)
			}
		})
	}
}
