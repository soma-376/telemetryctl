package installer

import (
	"fmt"

	"github.com/your-org/pulsemetry/internal/config"
	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/enrollment"
)

// Reconnect exchanges the installation credential stored in the OS keyring
// for a new telemetry token and rewrites the managed exporter settings. The
// installation credential is never written to vendor configs.
func Reconnect(statePath, serverOverride string) (*Report, error) {
	state, err := LoadState(statePath)
	if err != nil {
		return nil, err
	}
	if state == nil || state.InstallationID == "" {
		return nil, fmt.Errorf("pulsemetry is not enrolled: state file not found at %s", statePath)
	}
	if err := state.Manifest.Validate(); err != nil {
		return nil, fmt.Errorf("local state cannot reconnect; enroll once with the updated client: %w", err)
	}
	if len(state.Targets) == 0 {
		return nil, fmt.Errorf("local state has no managed config targets")
	}

	serverURL := serverOverride
	if serverURL == "" {
		serverURL = state.ServerURL
	}
	if serverURL == "" {
		return nil, fmt.Errorf("enrollment server URL is missing; pass --server")
	}

	cred, err := credential.LoadInstallation()
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("installation credential is missing from the OS keyring; enroll again")
	}
	if cred.InstallationID != state.InstallationID {
		return nil, fmt.Errorf("installation credential does not match local state")
	}

	issued, err := enrollment.RefreshTelemetryToken(serverURL, cred.InstallationToken)
	if err != nil {
		return nil, err
	}
	if issued.InstallationID != state.InstallationID {
		return nil, fmt.Errorf("server returned telemetry token for a different installation")
	}

	report := &Report{
		InstallationID: state.InstallationID,
		ConfigRevision: state.ConfigRevision,
		LocalEnabled:   state.Local.Enabled,
		Endpoint:       state.Manifest.OTLP.Endpoint,
	}

	// 로컬로 배선된 설치는 벤더 설정을 건드리지 않는다 (PROJ-45).
	//
	// 벤더 설정에 적혀 있는 것은 로컬 ingest 토큰이다. 여기에 회사 토큰을 쓰면 endpoint 도
	// 회사 것으로 함께 돌아가 **재배선이 조용히 풀린다** — 게다가 state.Local.Enabled 는
	// true 로 남아 상태와 현실이 갈린다. 새 회사 토큰은 대피본에만 갱신하면 되고,
	// 그것이 `local disable` 이 나중에 쓸 값이다.
	if state.Local.Enabled {
		if err := credential.Set(credential.AccountTelemetry, issued.TelemetryToken); err != nil {
			return report, fmt.Errorf("회사 토큰 대피본 갱신 실패: %w", err)
		}
		report.Endpoint = LocalEndpoint(localPortOrDefault(state))
		state.ServerURL = serverURL
		if err := SaveState(statePath, state); err != nil {
			return report, err
		}
		return report, nil
	}

	for _, target := range state.Targets {
		var result config.Result
		switch target.Tool {
		case "claude":
			result, err = config.MergeClaude(target.Path, &state.Manifest, issued.TelemetryToken, false)
		case "codex":
			result, err = config.MergeCodex(target.Path, &state.Manifest, issued.TelemetryToken, false)
		default:
			continue
		}
		if err != nil {
			return report, fmt.Errorf("update %s telemetry token: %w", target.Tool, err)
		}
		report.Targets = append(report.Targets, result)
	}

	state.ServerURL = serverURL
	if err := SaveState(statePath, state); err != nil {
		return report, err
	}
	return report, nil
}
