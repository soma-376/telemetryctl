package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/your-org/telemetryctl/internal/configmerge"
	"github.com/your-org/telemetryctl/internal/hostenv"
	"github.com/your-org/telemetryctl/internal/manifest"
)

// Version 은 이 installer 빌드 버전이다. state.installer_version 에 기록된다 (§5.3).
var Version = "0.1.0"

// Options 는 한 번의 install 실행 입력이다. 설정/상태 경로를 명시적으로 받아
// 테스트에서 임시 홈을 주입할 수 있게 한다. main 은 DefaultPaths 로 이 값을 채운다.
type Options struct {
	ManifestPath string // 적용할 enroll 응답 봉투(JSON) 파일: {installation_id, installation_token, manifest}
	ClaudePath   string // Claude settings.json 경로 ("" 이면 건너뜀)
	CodexPath    string // Codex config.toml 경로 ("" 이면 건너뜀)
	StatePath    string // 설치 상태 저장 경로
	Force        bool   // 다른 endpoint 충돌 시 강제 교체 (§4.2)
}

// Report 는 install 결과 요약이다. 토큰 등 비밀은 담지 않는다 (§4.5).
type Report struct {
	InstallationID string
	ConfigRevision int
	Targets        []configmerge.Result
}

// Install 은 manifest 를 읽어 Claude·Codex 설정에 OTel 키를 병합하고 상태를 저장한다.
//
// 대상은 Claude Code, Codex 순으로 처리한다. 한 대상에서 실패(예: endpoint 충돌 §4.2)하면
// 거기서 중단하되, 그 이전에 적용된 대상은 상태에 기록해 uninstall 이 정리할 수 있게 한다.
func Install(opts Options) (*Report, error) {
	raw, err := os.ReadFile(opts.ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("enrollment 파일 읽기 실패: %w", err)
	}
	enr, err := manifest.ParseEnrollment(raw)
	if err != nil {
		return nil, err
	}
	m := &enr.Manifest

	rep := &Report{InstallationID: enr.InstallationID, ConfigRevision: m.ConfigRevision}
	st := &State{
		StateSchemaVersion: StateSchemaVersion,
		InstallationID:     enr.InstallationID,
		ConfigRevision:     m.ConfigRevision,
		InstallerVersion:   Version,
		InstalledAt:        time.Now().UTC().Format(time.RFC3339),
	}

	steps := []struct {
		tool string
		path string
		fn   func(string, *manifest.Manifest, string, bool) (configmerge.Result, error)
	}{
		{"claude", opts.ClaudePath, configmerge.MergeClaude},
		{"codex", opts.CodexPath, configmerge.MergeCodex},
	}
	for _, s := range steps {
		if s.path == "" {
			continue // 대상 경로가 없으면 건너뜀
		}
		res, err := s.fn(s.path, m, enr.InstallationToken, opts.Force)
		if err != nil {
			// 실패 시 중단. 이전에 적용된 대상이 있으면 상태를 저장해 두어야 uninstall 이
			// 그 키를 정리할 수 있다 (§5.2). 아무것도 적용 안 됐으면 상태 파일을 만들지 않는다.
			if len(st.Targets) > 0 {
				_ = SaveState(opts.StatePath, st)
			}
			return rep, fmt.Errorf("%s 설정 적용 실패: %w", s.tool, err)
		}
		rep.Targets = append(rep.Targets, res)
		st.Targets = append(st.Targets, Target{
			Tool:        s.tool,
			Path:        res.Path,
			BackupPath:  res.BackupPath,
			ManagedKeys: res.ManagedKeys,
			Created:     res.Created,
		})
	}

	if err := SaveState(opts.StatePath, st); err != nil {
		return rep, err
	}
	return rep, nil
}

// StatePath 는 설치 상태 파일 경로다 (~/.telemetryctl/state.json).
func StatePath(env hostenv.Env) string {
	return filepath.Join(env.HomeDir, ".telemetryctl", "state.json")
}

// DefaultPaths 는 실행 환경(hostenv)으로부터 기본 설정/상태 경로를 채운 Options 를 만든다.
// main 이 사용한다. 테스트는 이 함수를 쓰지 않고 경로를 직접 주입한다.
func DefaultPaths(manifestPath string, force bool) (Options, error) {
	env, err := hostenv.Detect()
	if err != nil {
		return Options{}, err
	}
	return Options{
		ManifestPath: manifestPath,
		ClaudePath:   env.ClaudeSettingsPath(),
		CodexPath:    env.CodexConfigPath(),
		StatePath:    StatePath(env),
		Force:        force,
	}, nil
}

// DefaultStatePath 는 실행 환경 기준 상태 파일 경로를 반환한다. status 등에서 쓴다.
func DefaultStatePath() (string, error) {
	env, err := hostenv.Detect()
	if err != nil {
		return "", err
	}
	return StatePath(env), nil
}
