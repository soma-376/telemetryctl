package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/your-org/pulsemetry/internal/config"
	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/hostenv"
)

var Version = "0.1.0"

type Options struct {
	ClaudePath string
	CodexPath  string
	StatePath  string
	BackupDir  string
	Force      bool
}

type Report struct {
	InstallationID string
	ConfigRevision int
	Targets        []config.Result
}

type applyStep struct {
	tool   string
	path   string
	merge  func(string, *contract.Manifest, string, bool) (config.Result, error)
	backup config.Backup
}

// Apply backs up all existing vendor files, synchronizes managed OTel keys,
// and restores previously modified files if any later operation fails.
func Apply(enrollment *contract.Enrollment, opts Options) (*Report, error) {
	manifest := &enrollment.Manifest
	report := &Report{InstallationID: enrollment.InstallationID, ConfigRevision: manifest.ConfigRevision}
	state := &State{
		StateSchemaVersion: StateSchemaVersion,
		InstallationID:     enrollment.InstallationID,
		ConfigRevision:     manifest.ConfigRevision,
		InstallerVersion:   Version,
		InstalledAt:        time.Now().UTC().Format(time.RFC3339),
	}

	steps := []applyStep{
		{tool: "claude", path: opts.ClaudePath, merge: config.MergeClaude},
		{tool: "codex", path: opts.CodexPath, merge: config.MergeCodex},
	}
	prepared := make([]applyStep, 0, len(steps))
	backupTime := time.Now().UTC()
	for _, step := range steps {
		if step.path == "" {
			continue
		}
		backup, err := config.CreateBackup(step.path, opts.BackupDir, step.tool, backupTime)
		if err != nil {
			return report, fmt.Errorf("%s backup failed: %w", step.tool, err)
		}
		step.backup = backup
		prepared = append(prepared, step)
	}

	applied := make([]applyStep, 0, len(prepared))
	rollback := func() error {
		var first error
		for i := len(applied) - 1; i >= 0; i-- {
			if err := config.RestoreBackup(applied[i].backup); err != nil && first == nil {
				first = err
			}
		}
		return first
	}

	for _, step := range prepared {
		result, err := step.merge(step.path, manifest, enrollment.InstallationToken, opts.Force)
		if err != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				return report, fmt.Errorf("%s config failed: %v; rollback failed: %w", step.tool, err, rollbackErr)
			}
			return report, fmt.Errorf("%s config failed: %w", step.tool, err)
		}
		result.BackupPath = step.backup.BackupPath
		result.OriginalSHA256 = step.backup.SHA256
		report.Targets = append(report.Targets, result)
		applied = append(applied, step)
		state.Targets = append(state.Targets, Target{
			Tool:           step.tool,
			Path:           result.Path,
			BackupPath:     result.BackupPath,
			OriginalSHA256: result.OriginalSHA256,
			ManagedKeys:    result.ManagedKeys,
			Created:        result.Created,
		})
	}

	// 자격증명 원본을 설정 파일보다 먼저 확정한다: settings.json 의 Authorization 헤더는
	// 키링의 이 값에서 언제든 다시 만들 수 있는 파생물이다.
	if err := credential.SaveInstallationToken(&credential.Credential{
		InstallationID:    enrollment.InstallationID,
		InstallationToken: enrollment.InstallationToken,
	}); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return report, fmt.Errorf("save credential: %v; rollback failed: %w", err, rollbackErr)
		}
		return report, err
	}

	if err := SaveState(opts.StatePath, state); err != nil {
		_ = credential.DeleteInstallationToken()
		if rollbackErr := rollback(); rollbackErr != nil {
			return report, fmt.Errorf("save state: %v; rollback failed: %w", err, rollbackErr)
		}
		return report, err
	}
	return report, nil
}

func StatePath(env hostenv.Env) string {
	return filepath.Join(env.HomeDir, ".pulsemetry", "state.json")
}

func BackupDir(env hostenv.Env) string {
	switch runtime.GOOS {
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "pulsemetry", "backups")
		}
		return filepath.Join(env.HomeDir, "AppData", "Local", "pulsemetry", "backups")
	case "darwin":
		return filepath.Join(env.HomeDir, "Library", "Application Support", "pulsemetry", "backups")
	default:
		return filepath.Join(env.HomeDir, ".local", "share", "pulsemetry", "backups")
	}
}

func DefaultPaths(force bool) (Options, error) {
	env, err := hostenv.Detect()
	if err != nil {
		return Options{}, err
	}
	return Options{
		ClaudePath: env.ClaudeSettingsPath(),
		CodexPath:  env.CodexConfigPath(),
		StatePath:  StatePath(env),
		BackupDir:  BackupDir(env),
		Force:      force,
	}, nil
}

func DefaultStatePath() (string, error) {
	env, err := hostenv.Detect()
	if err != nil {
		return "", err
	}
	return StatePath(env), nil
}

