// Package desktopinstall은 GUI·OS 제거 프로그램이 공유하는 사용자별 설치 해제를 담당한다.
package desktopinstall

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/your-org/pulsemetry/internal/autostart"
	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/hostenv"
	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/localapi"
	"github.com/your-org/pulsemetry/internal/store"
)

// Options의 콜백은 테스트가 실제 사용자 서비스와 키링에 접근하지 않게 한다.
type Options struct {
	StatePath        string
	DataDir          string
	DeleteData       bool
	Stop             func(context.Context) error
	DeleteCredential func(credential.Account) error
}

type Preview struct {
	Installed  bool     `json:"installed"`
	DeleteData bool     `json:"delete_data"`
	Warnings   []string `json:"warnings"`
}

func Prepare(opts Options) (*installer.UninstallPlan, Preview, error) {
	env, err := hostenv.Detect()
	if err != nil {
		return nil, Preview{}, err
	}
	if opts.StatePath == "" {
		opts.StatePath = installer.StatePath(env)
	}
	state, err := installer.LoadState(opts.StatePath)
	if err != nil {
		return nil, Preview{}, err
	}
	if opts.DataDir == "" {
		opts.DataDir = store.DefaultDataDir(env)
		if state != nil && state.Local.DataDir != "" {
			opts.DataDir = state.Local.DataDir
		}
	}
	opts.StatePath, err = filepath.Abs(opts.StatePath)
	if err != nil {
		return nil, Preview{}, err
	}
	opts.DataDir, err = filepath.Abs(opts.DataDir)
	if err != nil {
		return nil, Preview{}, err
	}
	if opts.Stop == nil {
		opts.Stop = func(ctx context.Context) error { return Stop(ctx, opts.StatePath, opts.DataDir) }
	}
	plan, err := installer.PrepareUninstall(installer.UninstallOptions{
		StatePath: opts.StatePath, DataDir: opts.DataDir, DeleteData: opts.DeleteData,
		Stop: opts.Stop, DeleteCredential: opts.DeleteCredential,
	})
	if err != nil {
		return nil, Preview{}, err
	}
	warnings := []string{}
	for _, warning := range plan.Warnings {
		if !strings.HasPrefix(warning, "실행 파일·") {
			warnings = append(warnings, warning)
		}
	}
	return plan, Preview{Installed: !plan.AlreadyRemoved, DeleteData: opts.DeleteData, Warnings: warnings}, nil
}

// Stop은 PID 추측 없이 사용자 서비스 해제와 인증된 데몬 종료를 수행한다.
func Stop(ctx context.Context, statePath, dataDir string) error {
	env, err := hostenv.Detect()
	if err != nil {
		return err
	}
	m, err := autostart.New(autostart.Options{Env: env})
	if err != nil && !errors.Is(err, autostart.ErrUnsupportedPlatform) {
		return err
	}
	if err == nil {
		if !strings.EqualFold(filepath.Clean(statePath), filepath.Clean(installer.StatePath(env))) {
			status, err := m.Status(ctx)
			if err != nil {
				return err
			}
			if status.Registered {
				return errors.New("사용자 지정 상태의 제거는 기본 자동 시작 서비스를 해제하지 않는다")
			}
		} else if _, err = m.Disable(ctx); err != nil {
			return fmt.Errorf("자동 시작 해제: %w", err)
		} else {
			status, err := m.Status(ctx)
			if err != nil {
				return err
			}
			if status.Registered || status.Loaded || status.Running {
				return errors.New("자동 시작 서비스가 아직 남아 있습니다. 종료 후 다시 시도하세요")
			}
		}
	}
	stopCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return localapi.StopDaemon(stopCtx, dataDir)
}
