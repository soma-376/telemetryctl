package installer

// 로컬 endpoint 재배선 opt-in (PROJ-36 12단계).
//
// 이 파일은 이 저장소에서 **처음으로 기존 사용자 설정을 바꾸는 코드**다. 0~11단계는
// 한 글자도 건드리지 않았다. 그래서 여기서 지키는 불변식이 유난히 많다.
//
// # 불변식 1 — 회사로 나가는 데이터는 재배선 전후로 동일하다
//
// 재배선은 벤더 설정에서 OTEL_LOG_USER_PROMPTS·OTEL_LOG_TOOL_DETAILS 를 1 로 강제한다
// (ADR 0003). 그러지 않으면 작업 제목·파일 변경 목록·툴 타임라인이 만들어지지 않는다.
// 이 강제가 안전한 이유는 오직 하나 — **포워더가 회사 manifest 의 Privacy 기준으로
// 제거한 뒤 상위로 보내기 때문**이다 (internal/forward). 그 전제가 성립하려면 회사
// manifest 원본이 절대 오염되지 않아야 한다. localManifest 가 깊은 사본을 만드는 것도,
// 이 파일이 state.Manifest 를 포인터로 넘기지 않는 것도 전부 그 하나 때문이다.
//
// # 불변식 2 — 127.0.0.1 은 쓰지 않는다
//
// contract.validOTLPEndpoint 와 contracts/enrollment-manifest.schema.json 은 http:// 를
// 리터럴 호스트 localhost 에만 허용한다. 수신기가 실제로 듣는 주소는 127.0.0.1 과 [::1]
// 이지만 설정에 적히는 이름은 언제나 localhost 다 (계획서 제약 §1, ADR 0001).
//
// # 불변식 3 — disable 은 네트워크 없이 정확히 되돌린다
//
// MergeClaude·MergeCodex 는 관리 키를 전부 지우고 (manifest, token) 으로 다시 쓰는
// 권위적 병합이다. 따라서 같은 (회사 manifest, 회사 token) 을 다시 넣으면 결과는
// 재배선 전과 바이트 단위로 같다. 회사 manifest 는 state.json 에 있고, 회사 token 은
// enable 이 키링(credential.AccountTelemetry)으로 대피시켜 둔다.

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/your-org/pulsemetry/internal/config"
	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/hostenv"
)

// DefaultLocalPort 는 재배선이 기본으로 가리키는 loopback 포트다.
//
// receiver.DefaultPort 와 같은 값이어야 하지만 여기에 따로 둔다 — installer 가 receiver 를
// import 하면 enroll·status 경로까지 protobuf 디코더를 끌고 들어온다. DefaultRetentionDays 와
// 같은 이유이고, 두 값이 어긋나면 cmd/telemetryctl 의 테스트가 잡는다.
const DefaultLocalPort = 4318

// ErrGRPCUnsupported 는 회사 manifest 가 grpc 일 때 재배선을 거부하는 오류다.
//
// 문구는 forward.ErrGRPCUnsupported 와 일부러 짝을 맞춘다. 두 곳에서 같은 제약을 말하는데
// 사용자가 보는 문장이 다르면 같은 문제라는 것을 알 수 없다. forward 를 import 해서
// 그 값을 재사용하지 않는 이유는 위 DefaultLocalPort 와 같다(의존성 방향).
var ErrGRPCUnsupported = errors.New(
	"grpc 상위 전달은 아직 지원하지 않는다: 회사 manifest 가 grpc 라 로컬 재배선을 켤 수 없다 " +
		"(기존 회사 Collector 직결은 그대로 동작한다)")

// ErrNotInstalled 는 설치 상태 파일이 없을 때의 오류다.
var ErrNotInstalled = errors.New("pulsemetry 가 설치되어 있지 않다")

// LocalOptions 는 EnableLocal·DisableLocal 의 입력이다.
type LocalOptions struct {
	// StatePath 는 설치 상태 파일 경로다. 필수.
	StatePath string
	// BackupDir 는 재배선 전 백업을 둘 곳이다. 비우면 hostenv 에서 파생한다.
	BackupDir string

	// Port 는 재배선이 가리킬 loopback 포트다. EnableLocal 전용이며 0 이면
	// state.Local.ListenPort → DefaultLocalPort 순으로 정한다.
	Port int
	// IngestToken 은 로컬 수신기의 loopback bearer 토큰이다. EnableLocal 에 필수다.
	// **회사 telemetry token 이 아니다** — receiver.EnsureToken 이 만들어 준다.
	IngestToken string
}

// LocalReport 는 재배선 결과다. 토큰은 어떤 필드에도 담기지 않는다 (§4.5).
type LocalReport struct {
	// Enabled 는 이 호출 이후의 상태다.
	Enabled bool
	// Endpoint 는 벤더 설정에 적힌 주소다 (enable 이면 http://localhost:<port>).
	Endpoint string
	// ListenPort 는 재배선이 가리키는 포트다. disable 이면 설정에 남아 있던 의도값이다.
	ListenPort int
	// Targets 는 다시 쓴 벤더 설정들이다.
	Targets []config.Result
	// AlreadyInState 는 이미 원하는 상태여서 설정을 건드리지 않았다는 뜻이다.
	AlreadyInState bool
	// TelemetryTokenStashed 는 회사 telemetry token 을 키링으로 대피시켰는지다.
	// false 면 disable 이 네트워크 없이 되돌리지 못한다 — 호출자가 경고해야 한다.
	TelemetryTokenStashed bool
}

// LocalEndpoint 는 포트를 벤더 설정에 적을 주소로 옮긴다.
//
// **반드시 localhost 표기다.** receiver.Server.Endpoint() 와 같은 문자열을 만들어야 한다
// (불변식 2). 127.0.0.1 을 쓰면 contract.Validate 가 거부한다.
func LocalEndpoint(port int) string {
	return "http://localhost:" + strconv.Itoa(port)
}

// localManifest 는 회사 manifest 의 **깊은 사본**을 만들어 로컬 수신기용으로 고친 것이다.
//
// 원본은 절대 건드리지 않는다. 회사 manifest 는 포워더가 "무엇을 지우고 보낼지" 판단하는
// 진실원이라, 여기서 Privacy 플래그를 켜면 포워더가 원문을 그대로 상위로 흘려보낸다 —
// ADR 0003 위반이자 이 티켓 전체를 무의미하게 만드는 버그다. Manifest 는 슬라이스와 맵
// 필드를 가지므로 값 복사만으로는 부족하고, cloneManifest 가 그 둘을 따로 복제한다.
//
// 바꾸는 것은 셋뿐이다.
//
//	otlp.endpoint                 → http://localhost:<port>
//	privacy.collect_user_prompts  → true   (작업 제목·요약의 원천)
//	privacy.collect_tool_details  → true   (파일 변경 목록·툴 타임라인의 원천)
//
// 나머지는 손대지 않는다. 특히 signals 를 켜지 않는다 — 회사가 끄기로 한 시그널을 로컬에서
// 켜면 포워더가 그 시그널을 상위로 새로 보내게 되고, 그것은 "회사로 나가는 데이터가
// 재배선 전후로 동일하다" 는 불변식 1 을 깬다. Privacy 의 나머지 네 항목도 같은 이유로
// 회사 값을 그대로 따른다.
func localManifest(company *contract.Manifest, port int) (contract.Manifest, error) {
	if company == nil {
		return contract.Manifest{}, errors.New("로컬 manifest: 회사 manifest 가 없다")
	}
	if port <= 0 || port > 65535 {
		return contract.Manifest{}, fmt.Errorf("로컬 수신 포트가 올바르지 않다: %d", port)
	}
	if company.OTLP.Protocol == "grpc" {
		return contract.Manifest{}, ErrGRPCUnsupported
	}

	local := cloneManifest(*company)
	local.OTLP.Endpoint = LocalEndpoint(port)
	// 수신기가 푸는 것은 identity 와 gzip 뿐이다 (receiver/body.go). 회사가 그 밖의
	// 압축을 지정했다면 로컬 구간에서만 끈다. 그대로 두면 모든 배치가 415 로 떨어져
	// 텔레메트리가 통째로 사라지는데, 그 실패는 사용자에게 보이지 않는다.
	if local.OTLP.Compression != "" && local.OTLP.Compression != "gzip" {
		local.OTLP.Compression = ""
	}
	local.Privacy.CollectUserPrompts = true
	local.Privacy.CollectToolDetails = true

	// 로컬 사본도 계약 검증을 통과해야 한다. localhost 표기를 쓰는지 여기서 확정된다.
	if err := local.Validate(); err != nil {
		return contract.Manifest{}, fmt.Errorf("로컬 manifest 검증 실패: %w", err)
	}
	return local, nil
}

// cloneManifest 는 Manifest 를 깊게 복사한다.
//
// 값 복사만 하면 RepositoryAllowlist(슬라이스)와 ResourceAttributes(맵)의 backing store 가
// 원본과 공유된다. 지금 이 파일이 그 둘을 수정하지 않더라도 공유 상태로 넘기면 안 된다 —
// 나중에 누가 로컬 사본의 resource attribute 하나를 고치는 순간 회사 manifest 가 조용히
// 함께 바뀐다.
func cloneManifest(m contract.Manifest) contract.Manifest {
	out := m
	if m.RepositoryAllowlist != nil {
		out.RepositoryAllowlist = append([]string(nil), m.RepositoryAllowlist...)
	}
	if m.ResourceAttributes != nil {
		out.ResourceAttributes = make(map[string]string, len(m.ResourceAttributes))
		for k, v := range m.ResourceAttributes {
			out.ResourceAttributes[k] = v
		}
	}
	return out
}

// EnableLocal 은 벤더 설정을 로컬 수신기로 돌린다.
//
// 이미 켜져 있을 때 다시 부르는 것이 정상 흐름이다 — 데몬이 포트 폴백을 하면 벤더 설정이
// 가리키는 주소가 어긋나는데, 그때 새 포트로 다시 부르는 것이 재병합 경로다
// (internal/daemon/runner.go 의 폴백 경고가 이 명령을 안내한다).
func EnableLocal(opts LocalOptions) (*LocalReport, error) {
	state, err := loadInstalledState(opts.StatePath)
	if err != nil {
		return nil, err
	}
	if opts.IngestToken == "" {
		return nil, errors.New("로컬 ingest 토큰이 비어 있다")
	}

	port := opts.Port
	if port <= 0 {
		port = state.Local.ListenPort
	}
	if port <= 0 {
		port = DefaultLocalPort
	}

	local, err := localManifest(&state.Manifest, port)
	if err != nil {
		return nil, err
	}

	report := &LocalReport{Enabled: true, Endpoint: local.OTLP.Endpoint, ListenPort: port}

	// 회사 토큰 대피는 **아직 켜지지 않았을 때만** 한다. 이미 켜져 있으면 벤더 설정에
	// 적힌 것은 로컬 ingest 토큰이고, 그것을 대피본으로 덮으면 disable 이 로컬 토큰을
	// 회사 설정에 써 넣는다. 되돌릴 수 없는 종류의 사고라 조건을 이중으로 건다.
	switch {
	case !state.Local.Enabled:
		stashed, err := stashTelemetryToken(state, opts.IngestToken)
		if err != nil {
			return nil, err
		}
		report.TelemetryTokenStashed = stashed
	default:
		_, found, err := credential.Get(credential.AccountTelemetry)
		if err != nil {
			return nil, err
		}
		report.TelemetryTokenStashed = found
	}

	results, restore, err := remergeTargets(state, &local, opts.IngestToken, opts.BackupDir)
	if err != nil {
		return nil, err
	}
	report.Targets = results

	previous := state.Local
	state.Local.Enabled = true
	state.Local.ListenPort = port
	if err := SaveState(opts.StatePath, state); err != nil {
		// 상태를 못 남기면 벤더 설정만 로컬을 가리킨 채 아무도 그 사실을 모르게 된다.
		// 설정을 되돌려 놓고 실패로 끝내는 편이 낫다.
		state.Local = previous
		if restoreErr := restore(); restoreErr != nil {
			return nil, fmt.Errorf("상태 저장 실패: %v; 설정 복구도 실패: %w", err, restoreErr)
		}
		return nil, err
	}
	return report, nil
}

// DisableLocal 은 벤더 설정을 회사 Collector 직결로 되돌린다. 사용자의 탈출구다.
func DisableLocal(opts LocalOptions) (*LocalReport, error) {
	state, err := loadInstalledState(opts.StatePath)
	if err != nil {
		return nil, err
	}

	report := &LocalReport{
		Enabled:    false,
		Endpoint:   state.Manifest.OTLP.Endpoint,
		ListenPort: state.Local.ListenPort,
	}
	if !state.Local.Enabled {
		// 이미 꺼져 있으면 아무것도 하지 않는다. 여기서 굳이 재병합하면 대피본이 없는
		// 정상 상태에서 실패하게 되고, 사용자는 멀쩡한 설치를 고장 난 것으로 오해한다.
		report.AlreadyInState = true
		return report, nil
	}

	token, found, err := credential.Get(credential.AccountTelemetry)
	if err != nil {
		return nil, err
	}
	if !found || token == "" {
		return nil, errors.New(
			"회사 telemetry token 대피본을 키링에서 찾지 못했다: 재배선을 정확히 되돌릴 수 없다. " +
				"`telemetryctl reconnect` 를 실행하면 서버에서 토큰을 재발급받아 회사 설정으로 되돌린다")
	}

	results, restore, err := remergeTargets(state, &state.Manifest, token, opts.BackupDir)
	if err != nil {
		return nil, err
	}
	report.Targets = results
	report.TelemetryTokenStashed = true

	previous := state.Local
	state.Local.Enabled = false
	if err := SaveState(opts.StatePath, state); err != nil {
		state.Local = previous
		if restoreErr := restore(); restoreErr != nil {
			return nil, fmt.Errorf("상태 저장 실패: %v; 설정 복구도 실패: %w", err, restoreErr)
		}
		return nil, err
	}

	// 대피본은 여기서 지운다. 회사 설정으로 되돌아간 뒤에는 벤더 설정 파일 자체가
	// 다시 진실원이므로 키링에 사본을 남겨 둘 이유가 없다.
	if err := credential.Delete(credential.AccountTelemetry); err != nil {
		return report, fmt.Errorf("재배선은 해제됐으나 토큰 대피본 삭제에 실패했다: %w", err)
	}

	// 로컬 ingest 토큰은 **지우지 않는다.** 데몬은 재배선 여부와 무관하게 수신기를 띄우고
	// 그 토큰으로 인증한다. 여기서 지우면 돌고 있는 데몬이 자기 토큰을 잃고, 다시 enable
	// 했을 때 이미 떠 있는 Claude Code 세션이 낡은 토큰으로 401 을 맞는다.
	// 완전 삭제는 uninstall 의 몫이다 (receiver.ClearToken).
	return report, nil
}

// loadInstalledState 는 상태 파일을 읽고 재배선이 가능한 설치인지 확인한다.
func loadInstalledState(statePath string) (*State, error) {
	if statePath == "" {
		return nil, errors.New("설치 상태 파일 경로가 비어 있다")
	}
	state, err := LoadState(statePath)
	if err != nil {
		return nil, err
	}
	if state == nil || state.InstallationID == "" {
		return nil, fmt.Errorf("%w: 상태 파일이 없다 (%s). 먼저 `telemetryctl enroll` 을 실행하라",
			ErrNotInstalled, statePath)
	}
	if err := state.Manifest.Validate(); err != nil {
		return nil, fmt.Errorf("로컬 상태의 manifest 가 유효하지 않다; 최신 클라이언트로 다시 enroll 하라: %w", err)
	}
	if len(state.Targets) == 0 {
		return nil, errors.New("설치 상태에 관리 중인 설정 대상이 없다")
	}
	return state, nil
}

// stashTelemetryToken 은 벤더 설정에 적힌 회사 telemetry token 을 키링으로 옮긴다.
// 옮길 토큰을 찾았으면 true 다.
func stashTelemetryToken(state *State, ingestToken string) (bool, error) {
	for _, target := range state.Targets {
		var (
			token string
			err   error
		)
		switch target.Tool {
		case "claude":
			token, err = config.ReadClaudeToken(target.Path)
		case "codex":
			token, err = config.ReadCodexToken(target.Path)
		default:
			continue
		}
		if err != nil {
			return false, fmt.Errorf("%s 설정에서 telemetry token 을 읽지 못했다: %w", target.Tool, err)
		}
		// 로컬 ingest 토큰을 회사 토큰으로 착각해 대피시키지 않는다. Enabled 플래그와
		// 값 비교 두 가지가 모두 틀려야 사고가 나도록 겹쳐 둔다.
		if token == "" || token == ingestToken {
			continue
		}
		if err := credential.Set(credential.AccountTelemetry, token); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// remergeTargets 는 상태에 기록된 모든 벤더 설정을 (manifest, token) 으로 다시 쓴다.
//
// Apply 와 같은 순서를 따른다: 전부 백업 → 순서대로 병합 → 하나라도 실패하면 이미 쓴 것을
// 역순으로 복구. 반환된 restore 는 호출자가 그 뒤 단계(상태 저장)에서 실패했을 때 쓴다.
func remergeTargets(state *State, m *contract.Manifest, token, backupDir string) ([]config.Result, func() error, error) {
	dir := backupDir
	if dir == "" {
		env, err := hostenv.Detect()
		if err != nil {
			return nil, nil, fmt.Errorf("백업 디렉터리 판별 실패: %w", err)
		}
		dir = BackupDir(env)
	}

	type step struct {
		index  int
		tool   string
		path   string
		backup config.Backup
	}

	backupTime := time.Now().UTC()
	prepared := make([]step, 0, len(state.Targets))
	for i, target := range state.Targets {
		if target.Path == "" {
			continue
		}
		switch target.Tool {
		case "claude", "codex":
		default:
			continue
		}
		backup, err := config.CreateBackup(target.Path, dir, target.Tool, backupTime)
		if err != nil {
			return nil, nil, fmt.Errorf("%s 백업 실패: %w", target.Tool, err)
		}
		prepared = append(prepared, step{index: i, tool: target.Tool, path: target.Path, backup: backup})
	}
	if len(prepared) == 0 {
		return nil, nil, errors.New("다시 쓸 벤더 설정이 없다")
	}

	applied := make([]step, 0, len(prepared))
	restore := func() error {
		var first error
		for i := len(applied) - 1; i >= 0; i-- {
			if err := config.RestoreBackup(applied[i].backup); err != nil && first == nil {
				first = err
			}
		}
		return first
	}

	results := make([]config.Result, 0, len(prepared))
	for _, s := range prepared {
		var (
			result config.Result
			err    error
		)
		switch s.tool {
		case "claude":
			result, err = config.MergeClaude(s.path, m, token, false)
		case "codex":
			result, err = config.MergeCodex(s.path, m, token, false)
		}
		if err != nil {
			if restoreErr := restore(); restoreErr != nil {
				return nil, nil, fmt.Errorf("%s 설정 적용 실패: %v; 복구도 실패: %w", s.tool, err, restoreErr)
			}
			return nil, nil, fmt.Errorf("%s 설정 적용 실패: %w", s.tool, err)
		}
		// 백업 경로는 상태의 것을 유지한다. state.Targets 의 BackupPath 는 enroll 직전의
		// **pulsemetry 이전 원본**이고, 재배선 백업이 그것을 덮으면 진짜 원본을 잃는다.
		result.BackupPath = s.backup.BackupPath
		result.OriginalSHA256 = s.backup.SHA256
		results = append(results, result)

		// 관리 키 목록은 갱신한다. 병합할 때마다 실제로 쓴 키가 여기 반영되어야
		// uninstall 이 잔재 없이 제거할 수 있다 (§5.2).
		state.Targets[s.index].ManagedKeys = result.ManagedKeys
		applied = append(applied, s)
	}
	return results, restore, nil
}
