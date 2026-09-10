package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/your-org/pulsemetry/internal/config"
	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/instancelock"
)

func TestUninstallDryPlanAndExecution(t *testing.T) {
	f, _ := newEnrollFixture(t, httpManifest(), ingestToken)
	before := mustRead(t, f.claudePath)
	dir := filepath.Join(filepath.Dir(f.statePath), "data")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "pulsemetry.db")
	if err := os.WriteFile(db, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	stopped := false
	deleted := 0
	opts := UninstallOptions{StatePath: f.statePath, DataDir: dir, Stop: func(context.Context) error { stopped = true; return nil }, DeleteCredential: func(credential.Account) error { deleted++; return nil }}
	p, err := PrepareUninstall(opts)
	if err != nil {
		t.Fatal(err)
	}
	if stopped || deleted != 0 || !bytes.Equal(before, mustRead(t, f.claudePath)) {
		t.Fatal("계획이 상태를 변경했다")
	}
	if err = p.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !stopped || deleted != 4 {
		t.Fatalf("종료·키링 단계 미실행 %v %d", stopped, deleted)
	}
	if _, err = os.Stat(db); err != nil {
		t.Fatal("기본 모드에서 DB를 지웠다")
	}
	if b := mustRead(t, f.claudePath); !bytes.Contains(b, []byte("MY_OWN_KEY")) || bytes.Contains(b, []byte(ingestToken)) {
		t.Fatal("사용자 보존·관리 설정 제거 실패")
	}
	p, err = PrepareUninstall(opts)
	if err != nil || !p.AlreadyRemoved {
		t.Fatal("재실행이 멱등이지 않다")
	}
}

func TestUninstallWriteFailureRestoresEarlierFile(t *testing.T) {
	f, _ := newEnrollFixture(t, httpManifest(), ingestToken)
	before := mustRead(t, f.claudePath)
	deleted := false
	p, err := PrepareUninstall(UninstallOptions{StatePath: f.statePath, DataDir: filepath.Join(filepath.Dir(f.statePath), "data"), Stop: func(context.Context) error { return nil }, DeleteCredential: func(credential.Account) error { deleted = true; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	p.applySetting = func(e *config.ManagedEdit) error {
		calls++
		if calls == 2 {
			return errors.New("디스크 쓰기 실패")
		}
		return e.Apply()
	}
	if err = p.Execute(context.Background()); err == nil {
		t.Fatal("쓰기 실패를 숨겼다")
	}
	if deleted {
		t.Fatal("설정 실패 후 키링 삭제")
	}
	if !bytes.Equal(before, mustRead(t, f.claudePath)) {
		t.Fatal("먼저 쓴 설정을 직전 원문으로 복구하지 않았다")
	}
}

func TestUninstallFailureNeverDeletesDataOrCredentials(t *testing.T) {
	for _, failure := range []string{"stop", "concurrent", "lock", "parse"} {
		t.Run(failure, func(t *testing.T) {
			f, _ := newEnrollFixture(t, httpManifest(), ingestToken)
			dir := filepath.Join(filepath.Dir(f.statePath), "data")
			_ = os.MkdirAll(dir, 0o700)
			db := filepath.Join(dir, "pulsemetry.db")
			_ = os.WriteFile(db, []byte("db"), 0o600)
			deleted := false
			opts := UninstallOptions{StatePath: f.statePath, DataDir: dir, DeleteData: true, DeleteCredential: func(credential.Account) error { deleted = true; return nil }, Stop: func(context.Context) error { return nil }}
			if failure == "parse" {
				_ = os.WriteFile(f.codexPath, []byte("invalid=["), 0o600)
			}
			p, err := PrepareUninstall(opts)
			if failure == "parse" {
				if err == nil {
					t.Fatal("파싱 실패를 허용했다")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "stop":
				p.options.Stop = func(context.Context) error { return errors.New("stop failed") }
			case "concurrent":
				_ = os.WriteFile(f.codexPath, []byte("model='user'"), 0o600)
			case "lock":
				l, err := instancelock.Acquire(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer l.Close()
			}
			if p.Execute(context.Background()) == nil {
				t.Fatal("실패가 성공 처리됐다")
			}
			if deleted {
				t.Fatal("실패 후 키링 삭제")
			}
			if _, err = os.Stat(db); err != nil {
				t.Fatal("실패 후 DB 삭제")
			}
		})
	}
}

func TestManagedRecordPreservesHooksOnOTelOnlyMerge(t *testing.T) {
	f, _ := newEnrollFixture(t, httpManifest(), ingestToken)
	s, _ := LoadState(f.statePath)
	before, err := LoadManaged(f.statePath, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = EnableLocal(LocalOptions{StatePath: f.statePath, BackupDir: f.backupDir, Port: 54321, IngestToken: ingestToken, PreserveCodexHooks: true}); err != nil {
		t.Fatal(err)
	}
	s, _ = LoadState(f.statePath)
	after, err := LoadManaged(f.statePath, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range s.Targets {
		if target.Tool == "codex" {
			for _, e := range before.entries(target) {
				if e.Event != "" {
					found := false
					for _, a := range after.entries(target) {
						if a.Event == e.Event && a.Digest == e.Digest {
							found = true
						}
					}
					if !found {
						t.Fatal("보존한 훅 지문을 변경했다")
					}
				}
			}
		}
	}
	drift, err := InspectManaged(f.statePath, s)
	if err != nil || len(drift) != 0 {
		t.Fatalf("자체 재배선이 drift로 표시됨: %+v %v", drift, err)
	}
}

func TestUninstallExplicitDataDeletionAndKeyringRetry(t *testing.T) {
	f, _ := newEnrollFixture(t, httpManifest(), ingestToken)
	dir := filepath.Join(filepath.Dir(f.statePath), "data")
	_ = os.MkdirAll(dir, 0o700)
	db := filepath.Join(dir, "pulsemetry.db")
	_ = os.WriteFile(db, []byte("db"), 0o600)
	user := filepath.Join(dir, "user.txt")
	_ = os.WriteFile(user, []byte("keep"), 0o600)
	opts := UninstallOptions{StatePath: f.statePath, DataDir: dir, DeleteData: true, Stop: func(context.Context) error { return nil }, DeleteCredential: func(credential.Account) error { return errors.New("locked") }}
	p, err := PrepareUninstall(opts)
	if err != nil {
		t.Fatal(err)
	}
	if p.Execute(context.Background()) == nil {
		t.Fatal("키링 오류를 숨겼다")
	}
	if _, err = os.Stat(f.statePath); err != nil {
		t.Fatal("재시도 기록 유실")
	}
	opts.DeleteCredential = func(credential.Account) error { return nil }
	p, err = PrepareUninstall(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(db); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("DB 미삭제")
	}
	if _, err = os.Stat(user); err != nil {
		t.Fatal("사용자 파일 삭제")
	}
}

func TestManagedPersistenceFailureRollsBackVendorConfigs(t *testing.T) {
	f, _ := newEnrollFixture(t, httpManifest(), ingestToken)
	beforeClaude := mustRead(t, f.claudePath)
	beforeCodex := mustRead(t, f.codexPath)
	_ = os.WriteFile(ManagedPath(f.statePath), []byte("invalid-record"), 0o600)
	_, err := EnableLocal(LocalOptions{StatePath: f.statePath, BackupDir: f.backupDir, Port: 54321, IngestToken: ingestToken, PreserveCodexHooks: true})
	if err == nil {
		t.Fatal("관리 기록 오류 무시")
	}
	if !bytes.Equal(beforeClaude, mustRead(t, f.claudePath)) || !bytes.Equal(beforeCodex, mustRead(t, f.codexPath)) {
		t.Fatal("기록 저장 실패 후 설정 미복구")
	}
}

func TestUninstallRejectsSymlinkAndPathCollision(t *testing.T) {
	f, _ := newEnrollFixture(t, httpManifest(), ingestToken)
	dir := filepath.Join(filepath.Dir(f.statePath), "data")
	_ = os.MkdirAll(dir, 0o700)
	s, _ := LoadState(f.statePath)
	s.Targets[0].Path = f.statePath
	if err := SaveState(f.statePath, s); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareUninstall(UninstallOptions{StatePath: f.statePath, DataDir: dir}); err == nil {
		t.Fatal("상태 파일과 설정 파일 충돌을 허용했다")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Dir(f.statePath), link); err != nil {
		t.Skip("symlink 권한 없음")
	}
	if err := safeUninstallPath(filepath.Join(link, "state.json")); err == nil {
		t.Fatal("링크 경로 허용")
	}
}

// 관리 기록 유실을 정상 제거로 처리하면 벤더 배선을 남긴 채 토큰만 삭제할 수 있다.
func TestUninstallRejectsMissingManagedRecords(t *testing.T) {
	for _, mode := range []string{"file", "target", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newEnrollFixture(t, httpManifest(), ingestToken)
			before := mustRead(t, f.claudePath)
			path := ManagedPath(f.statePath)
			switch mode {
			case "file":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "target":
				var m ManagedSettings
				if err := json.Unmarshal(mustRead(t, path), &m); err != nil {
					t.Fatal(err)
				}
				m.Targets = nil
				b, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, b, 0o600); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			loaded, err := LoadState(f.statePath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = InspectManaged(f.statePath, loaded); err == nil {
				t.Fatal("관리 기록 오류를 숨김")
			}
			plan, err := PrepareUninstall(UninstallOptions{StatePath: f.statePath, DataDir: filepath.Join(filepath.Dir(f.statePath), "data")})
			if err == nil || plan != nil {
				t.Fatal("관리 기록 오류인데 제거 계획 생성")
			}
			if !bytes.Equal(before, mustRead(t, f.claudePath)) {
				t.Fatal("설정이 변경됨")
			}
		})
	}
}
