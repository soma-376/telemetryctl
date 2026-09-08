package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/your-org/pulsemetry/internal/config"
	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/instancelock"
)

type UninstallOptions struct {
	StatePath  string
	DataDir    string
	DeleteData bool
	// Stop은 자동 시작 해제·해당 데몬 정상 종료를 수행한다. dry-run에서는 호출하지 않는다.
	Stop             func(context.Context) error
	DeleteCredential func(credential.Account) error
}

type UninstallPlan struct {
	Settings       []*config.ManagedEdit
	Files          []string
	Warnings       []string
	AlreadyRemoved bool
	options        UninstallOptions
	stateBefore    []byte
	managedBefore  []byte
	applySetting   func(*config.ManagedEdit) error
}

// safeUninstallPath는 심볼릭 링크·junction을 따라 사용자 파일을 수정하거나 지우지 않게 한다.
func safeUninstallPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) == filepath.VolumeName(path)+string(filepath.Separator) {
		return fmt.Errorf("안전하지 않은 제거 경로: %s", path)
	}
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("링크 경로는 제거하지 않는다: %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("일반 파일이 아닌 제거 대상: %s", path)
	}
	return nil
}

func PrepareUninstall(opts UninstallOptions) (*UninstallPlan, error) {
	p := &UninstallPlan{options: opts, applySetting: func(e *config.ManagedEdit) error { return e.Apply() }}
	if err := safeUninstallPath(opts.StatePath); err != nil {
		return nil, err
	}
	if err := safeUninstallPath(ManagedPath(opts.StatePath)); err != nil {
		return nil, err
	}
	s, err := LoadState(opts.StatePath)
	if err != nil {
		return nil, err
	}
	if s == nil {
		p.AlreadyRemoved = true
		return p, nil
	}
	if s.InstallationID == "" {
		return nil, errors.New("설치 ID가 없는 상태는 제거할 수 없다")
	}
	if !filepath.IsAbs(opts.DataDir) || filepath.Dir(filepath.Clean(opts.DataDir)) == filepath.Clean(opts.DataDir) {
		return nil, errors.New("데이터 디렉터리는 절대 경로여야 한다")
	}
	p.stateBefore, err = os.ReadFile(opts.StatePath)
	if err != nil {
		return nil, err
	}
	p.managedBefore, err = os.ReadFile(ManagedPath(opts.StatePath))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	m, err := LoadManaged(opts.StatePath, s)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{uninstallPathKey(opts.StatePath): true, uninstallPathKey(ManagedPath(opts.StatePath)): true}
	for _, target := range s.Targets {
		if err = safeUninstallPath(target.Path); err != nil {
			return nil, err
		}
		key := uninstallPathKey(target.Path)
		if seen[key] {
			return nil, errors.New("설정·설치 파일의 경로가 충돌한다")
		}
		seen[key] = true
		edit, err := config.PlanManagedRemoval(target.Tool, target.Path, m.entries(target), target.ManagedKeys)
		if err != nil {
			return nil, err
		}
		p.Settings = append(p.Settings, edit)
		for _, c := range edit.Checks {
			if c.Status == "changed" || c.Status == "unknown" || c.Status == "shared" {
				p.Warnings = append(p.Warnings, fmt.Sprintf("보존: %s · %s (%s)", target.Path, c.Key, c.Status))
			}
		}
	}
	// 부모 디렉터리는 재귀 삭제하지 않는다. 잠금 파일은 동시 실행 안전을 위해 남긴다.
	names := []string{"runtime.json", "hook-bridge.log"}
	if opts.DeleteData {
		names = append(names, "pulsemetry.db", "pulsemetry.db-wal", "pulsemetry.db-shm")
	}
	for _, name := range names {
		path := filepath.Join(opts.DataDir, name)
		if seen[uninstallPathKey(path)] {
			return nil, errors.New("데이터·설정 파일의 경로가 충돌한다")
		}
		if err = safeUninstallPath(path); err != nil {
			return nil, err
		}
		p.Files = append(p.Files, path)
	}
	p.Files = append(p.Files, ManagedPath(opts.StatePath), opts.StatePath)
	p.Warnings = append(p.Warnings, "실행 파일·기존 백업·사용자 추가 파일·잠금 파일은 보존한다. 실행 파일 제거는 설치 패키지에서 수행한다.")
	p.Warnings = append(p.Warnings, "설정 변경 직전 오류 복구용 백업: "+filepath.Join(filepath.Dir(opts.StatePath), "uninstall-backups"))
	if !opts.DeleteData {
		p.Warnings = append(p.Warnings, "로컬 DB는 보존한다 (--delete-data를 명시한 경우에만 삭제)")
	}
	return p, nil
}

func uninstallPathKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

// Execute는 설정 해제가 모두 끝나기 전에는 키링·데이터를 지우지 않는다.
func (p *UninstallPlan) Execute(ctx context.Context) error {
	if p.AlreadyRemoved {
		return nil
	}
	if p.options.Stop == nil {
		return errors.New("데몬 종료 확인 단계가 없다")
	}
	if p.options.DeleteCredential == nil {
		installed, err := credential.LoadInstallation()
		if err != nil {
			return err
		}
		state, err := LoadState(p.options.StatePath)
		if err != nil || state == nil {
			return errors.New("설치 상태를 확인할 수 없다")
		}
		if installed != nil && installed.InstallationID != state.InstallationID {
			return errors.New("키링이 다른 설치에 속한다. 제거하지 않는다")
		}
	}
	if err := p.verify(); err != nil {
		return err
	}
	if err := p.options.Stop(ctx); err != nil {
		return fmt.Errorf("데몬 종료/자동 시작 해제 실패 (일부 해제됐을 수 있음): %w", err)
	}
	lease, err := instancelock.Acquire(p.options.DataDir)
	if err != nil {
		return err
	}
	defer lease.Close()
	if err = p.verify(); err != nil {
		return err
	}
	for _, e := range p.Settings {
		if err = e.Verify(); err != nil {
			return err
		}
	}
	// 직전 백업은 이전 설치 복원용이 아니라 이번 작업의 오류 복구용이다.
	backupDir := filepath.Join(filepath.Dir(p.options.StatePath), "uninstall-backups")
	for i, e := range p.Settings {
		if bytes.Equal(e.Before, e.After) {
			continue
		}
		if err = safeUninstallPath(filepath.Join(backupDir, fmt.Sprintf("target-%d", i), "probe")); err != nil {
			return err
		}
		if _, err = config.CreateBackup(e.Path, backupDir, fmt.Sprintf("target-%d", i), time.Now()); err != nil {
			return err
		}
	}
	applied := []*config.ManagedEdit{}
	for _, e := range p.Settings {
		err = ctx.Err()
		if err == nil {
			err = p.applySetting(e)
		}
		if err != nil {
			for i := len(applied) - 1; i >= 0; i-- {
				if undoErr := applied[i].Restore(); undoErr != nil {
					err = errors.Join(err, undoErr)
				}
			}
			return fmt.Errorf("설정 해제 실패; 데이터·키링은 보존했다: %w", err)
		}
		if !bytes.Equal(e.Before, e.After) {
			applied = append(applied, e)
		}
	}
	removeCredential := p.options.DeleteCredential
	if removeCredential == nil {
		removeCredential = credential.Delete
	}
	for _, account := range []credential.Account{credential.AccountInstallation, credential.AccountTelemetry, credential.AccountLocalIngest, credential.AccountLocalControl} {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = removeCredential(account); err != nil {
			return fmt.Errorf("키링 정리 중단 (%s); 설치 기록을 남겼으므로 재실행 가능: %w", account, err)
		}
	}
	for _, path := range p.Files {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = safeUninstallPath(path); err != nil {
			return err
		}
		if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("파일 정리 실패 (%s): %w", path, err)
		}
	}
	return nil
}

func (p *UninstallPlan) verify() error {
	for _, path := range p.Files {
		if err := safeUninstallPath(path); err != nil {
			return err
		}
	}
	for _, e := range p.Settings {
		if err := safeUninstallPath(e.Path); err != nil {
			return err
		}
		if err := e.Verify(); err != nil {
			return err
		}
	}
	b, err := os.ReadFile(p.options.StatePath)
	if err != nil || !bytes.Equal(b, p.stateBefore) {
		return errors.New("검사 후 설치 상태가 변경됐다. 다시 실행하라")
	}
	b, err = os.ReadFile(ManagedPath(p.options.StatePath))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !bytes.Equal(b, p.managedBefore) {
		return errors.New("검사 후 관리 기록이 변경됐다. 다시 실행하라")
	}
	return nil
}

// DriftImpact는 값 원문 대신 영향받는 기능만 설명한다.
func DriftImpact(key string) string {
	if strings.HasPrefix(key, "hooks.") || key == "features.hooks" {
		return "세션 생명 주기 반영에 영향"
	}
	return "텔레메트리 수집·전송에 영향"
}
