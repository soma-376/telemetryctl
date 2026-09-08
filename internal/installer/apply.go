package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	ServerURL  string
	Force      bool
	// HookExecutable 은 Codex command hook이 실행할 설치 바이너리의 절대 경로다.
	// 로컬 배선에서 비어 있으면 PATH 의존 훅을 만들지 않고 실패한다.
	HookExecutable string

	// IngestToken 은 로컬 수신기의 loopback bearer 토큰이다. 비어 있으면 로컬 배선을
	// 건너뛰고 회사 Collector 직결로 설치한다 (PROJ-45 이전 동작).
	//
	// **회사 telemetry token 이 아니다.** installer 가 receiver.EnsureToken 을 직접 부르지
	// 않는 이유는 receiver 를 import 하면 enroll·status 경로까지 protobuf 디코더가 딸려
	// 오기 때문이다 — LocalOptions.IngestToken 과 같은 이유다 (local.go).
	IngestToken string
}

type Report struct {
	InstallationID string
	ConfigRevision int
	Targets        []config.Result
	// LocalEnabled 는 설치가 로컬 파이프라인으로 배선됐는지다.
	LocalEnabled bool
	// Endpoint 는 벤더 설정에 적힌 주소다. 로컬 배선이면 http://localhost:<port> 다.
	Endpoint string
}

type applyStep struct {
	tool   string
	path   string
	merge  func(string, *contract.Manifest, string, bool) (config.Result, error)
	backup config.Backup
}

// mergeCodexInstalled 는 Codex command hook이 셸 PATH에 의존하지 않게 한다. 설치·재배선·
// 재연결이 모두 같은 함수를 써야 이전 절대 경로 훅을 교체하거나 제거할 수 있다.
func mergeCodexInstalled(path string, manifest *contract.Manifest, token string, force bool, executable, dataDir string) (config.Result, error) {
	if isLocalEndpoint(manifest.OTLP.Endpoint) && executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return config.Result{}, fmt.Errorf("Pulsemetry 실행 경로 확인 실패: %w", err)
		}
		// installer를 직접 쓰는 GUI 경로도 go run 임시 파일을 영구 설정에 남기지 않는다.
		// Go 테스트 바이너리는 같은 디렉터리를 쓰므로 .test 실행 파일만 예외다.
		if isGoRunPath(executable) {
			if strings.Contains(strings.ToLower(filepath.Base(executable)), ".test") {
				// 테스트는 실제 파일을 실행하지 않고 설정 병합만 검증한다. Pulsemetry 이름을
				// 써야 disable의 예약 명령 판정도 운영과 같은 경로를 탄다.
				executable = filepath.Join(filepath.Dir(executable), "pulsemetry.exe")
			} else {
				return config.Result{}, errors.New("go run 임시 바이너리는 Codex hook에 등록할 수 없다")
			}
		}
	}
	return config.MergeCodexWithExecutable(path, manifest, token, force, executable, dataDir)
}

func isLocalEndpoint(endpoint string) bool {
	return strings.HasPrefix(endpoint, "http://localhost:")
}

func isGoRunPath(path string) bool {
	tempDir, err := filepath.Abs(os.TempDir())
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(tempDir, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if strings.HasPrefix(strings.ToLower(part), "go-build") {
			return true
		}
	}
	return false
}

// Apply backs up all existing vendor files, synchronizes managed OTel keys,
// and restores previously modified files if any later operation fails.
//
// # 로컬 파이프라인은 기본으로 배선된다 (PROJ-45, ADR 0006)
//
// opts.IngestToken 이 있으면 벤더 설정을 회사 endpoint 가 아니라 로컬 수신기로 돌린다.
// 사용자가 `local enable` 을 따로 실행할 필요가 없다는 뜻이고, 이것이 opt-in → opt-out
// 전환의 전부다. 되돌리는 것은 `local disable` 이다.
//
// 두 경우에 회사 직결로 강등한다. 둘 다 조용히 넘어가지 않고 Report 로 알린다.
//
//	IngestToken 이 비어 있다  — 호출자가 키링에서 토큰을 얻지 못했다.
//	회사 manifest 가 grpc 다 — forward 가 grpc 상위 전달을 못 한다. 배선하면 로컬에만
//	                           쌓이고 회사에는 아무것도 가지 않는다.
//
// 강등 상태에서는 포워더 Scrub 이 경로 밖이라 manifest privacy 집행이 벤더 설정 계층만으로
// 남는다 (Codex 는 log_user_prompt 만 manifest 에 연결된다) — 공백의 상세와 수정 방향은
// 허브 contracts/telemetry-ingest.md §5 M13.
func Apply(enrollment *contract.Enrollment, opts Options) (*Report, error) {
	manifest := &enrollment.Manifest
	report := &Report{
		InstallationID: enrollment.InstallationID,
		ConfigRevision: manifest.ConfigRevision,
		Endpoint:       manifest.OTLP.Endpoint,
	}

	// 벤더 설정에 무엇을 쓸지 정한다. 로컬 배선이면 (고정 프로필, ingest 토큰),
	// 아니면 (회사 manifest, 회사 telemetry token) 이다.
	mergeManifest, mergeToken := manifest, enrollment.TelemetryToken
	local := DefaultLocal()
	if opts.IngestToken != "" {
		profile, err := localProfile(manifest, DefaultLocalPort)
		switch {
		case errors.Is(err, ErrGRPCUnsupported):
			// grpc 테넌트는 회사 직결로 설치한다. enroll 자체를 실패시키면 로컬 파이프라인
			// 때문에 설치가 통째로 막히는 셈이라 훨씬 나쁘다.
		case err != nil:
			return report, fmt.Errorf("로컬 프로필 생성 실패: %w", err)
		default:
			mergeManifest, mergeToken = &profile, opts.IngestToken
			local.Enabled = true
			local.ListenPort = DefaultLocalPort
			report.LocalEnabled = true
			report.Endpoint = profile.OTLP.Endpoint
		}
	}

	state := &State{
		StateSchemaVersion: StateSchemaVersion,
		InstallationID:     enrollment.InstallationID,
		ServerURL:          opts.ServerURL,
		ConfigRevision:     manifest.ConfigRevision,
		InstallerVersion:   Version,
		InstalledAt:        time.Now().UTC().Format(time.RFC3339),
		// **회사 manifest 원본**을 저장한다. 고정 프로필이 아니다 — 데몬이 이 값으로
		// 포워더의 signals·privacy 집행 기준을 세우고, local disable 이 이 값으로
		// 회사 직결 설정을 복원한다.
		Manifest: *manifest,
		Local:    local,
	}

	mergeCodex := func(path string, manifest *contract.Manifest, token string, force bool) (config.Result, error) {
		return mergeCodexInstalled(path, manifest, token, force, opts.HookExecutable, local.DataDir)
	}
	steps := []applyStep{
		{tool: "claude", path: opts.ClaudePath, merge: config.MergeClaude},
		{tool: "codex", path: opts.CodexPath, merge: mergeCodex},
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
		result, err := step.merge(step.path, mergeManifest, mergeToken, opts.Force)
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
			pendingManaged: result.ManagedEntries,
			Tool:           step.tool,
			Path:           result.Path,
			BackupPath:     result.BackupPath,
			OriginalSHA256: result.OriginalSHA256,
			ManagedKeys:    result.ManagedKeys,
			Created:        result.Created,
		})
	}

	// 로컬로 배선했으면 회사 telemetry token 을 키링으로 대피시킨다.
	//
	// 벤더 설정에는 이제 로컬 ingest 토큰만 적히므로, 이것이 회사 토큰의 **유일한 사본**이
	// 된다. 빠뜨리면 `local disable` 이 "대피본을 찾지 못했다"로 실패하고 사용자는 서버
	// 왕복(`reconnect`) 없이는 회사 직결로 돌아갈 수 없다 — local.go 의 불변식 3 위반이다.
	//
	// `local enable` 은 같은 일을 벤더 파일에서 되읽어서 한다 (stashTelemetryToken).
	// 여기서는 enrollment 응답에 원본이 있으니 되읽을 이유가 없다.
	if report.LocalEnabled {
		if err := credential.Set(credential.AccountTelemetry, enrollment.TelemetryToken); err != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				return report, fmt.Errorf("회사 토큰 대피 실패: %v; 설정 복구도 실패: %w", err, rollbackErr)
			}
			return report, fmt.Errorf("회사 토큰 대피 실패: %w", err)
		}
	}

	// 아래 단계가 실패하면 대피본도 함께 걷어낸다. 설치가 롤백된 뒤에도 키링에 남으면
	// 다음 설치의 stashTelemetryToken 이 남의 토큰을 회사 토큰으로 착각한다.
	clearStash := func() {
		if report.LocalEnabled {
			_ = credential.Delete(credential.AccountTelemetry)
		}
	}

	// 장기 설치 자격증명은 OS 키링에만 저장한다. 벤더 설정에는 enrollment 응답의
	// 교체 가능한 telemetry_token만 기록되어 있다.
	if err := credential.SaveInstallation(&credential.Credential{
		InstallationID:    enrollment.InstallationID,
		InstallationToken: enrollment.InstallationToken,
	}); err != nil {
		clearStash()
		if rollbackErr := rollback(); rollbackErr != nil {
			return report, fmt.Errorf("save credential: %v; rollback failed: %w", err, rollbackErr)
		}
		return report, err
	}

	if err := SaveState(opts.StatePath, state); err != nil {
		clearStash()
		_ = credential.DeleteInstallation()
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
