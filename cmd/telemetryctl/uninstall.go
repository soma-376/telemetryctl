package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/your-org/pulsemetry/internal/autostart"
	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/localapi"
)

func cmdUninstall(args []string) int { return runUninstall(os.Stdin, os.Stdout, os.Stderr, args, nil) }

// stop 주입으로 테스트가 실제 서비스 관리자·데몬을 건드리지 않게 한다.
func runUninstall(stdin io.Reader, stdout, stderr io.Writer, args []string, stop func(context.Context, string) error) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	statePath := fs.String("state", "", "설치 상태 파일 경로")
	dataDir := fs.String("data-dir", "", "데이터 디렉터리")
	dry := fs.Bool("dry-run", false, "제거·보존 예정 항목만 표시")
	yes := fs.Bool("yes", false, "설치 해제 확인 생략 (DB 삭제 동의와는 별개)")
	deleteData := fs.Bool("delete-data", false, "로컬 DB·WAL·SHM도 삭제 (복구 불가)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	target, err := resolveLocalTarget(*dataDir, *statePath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if target.StateErr != nil {
		fmt.Fprintln(stderr, target.StateErr)
		return 1
	}
	absState, err := filepath.Abs(target.StatePath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	absData, err := filepath.Abs(target.DataDir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if stop == nil {
		stop = func(ctx context.Context, data string) error { return stopForUninstall(ctx, absState, data) }
	}
	p, err := installer.PrepareUninstall(installer.UninstallOptions{StatePath: absState, DataDir: absData, DeleteData: *deleteData,
		Stop: func(ctx context.Context) error { return stop(ctx, absData) }})
	if err != nil {
		fmt.Fprintln(stderr, "제거 계획 실패:", err)
		return 1
	}
	if p.AlreadyRemoved {
		fmt.Fprintln(stdout, "설치 상태가 없습니다. 추가 삭제 없이 종료합니다.")
		return 0
	}
	fmt.Fprintln(stdout, "자동 시작 해제·데몬 정상 종료 후 Pulsemetry 설정을 제거합니다. 벤더 도구도 먼저 종료하세요.")
	fmt.Fprintln(stdout, "키링 제거: installation, telemetry, local-ingest, local-control (서버 토큰 폐기는 아님)")
	for _, e := range p.Settings {
		for _, c := range e.Checks {
			fmt.Fprintf(stdout, "설정: %s · %s (%s)\n", e.Path, c.Key, c.Status)
		}
	}
	for _, path := range p.Files {
		fmt.Fprintln(stdout, "제거 파일:", path)
	}
	for _, warning := range p.Warnings {
		fmt.Fprintln(stdout, warning)
	}
	if *dry {
		fmt.Fprintln(stdout, "dry-run: 변경하지 않았습니다.")
		return 0
	}
	if !*yes {
		fmt.Fprint(stdout, "위 작업을 진행할까요? [y/N] ")
		answer, _ := bufio.NewReader(stdin).ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(answer), "y") {
			fmt.Fprintln(stdout, "취소했습니다.")
			return 0
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err = p.Execute(ctx); err != nil {
		fmt.Fprintln(stderr, "제거 중단:", err)
		return 1
	}
	fmt.Fprintln(stdout, "Pulsemetry 설치 설정·자격증명 정리를 완료했습니다. 보존 안내 항목을 확인하세요.")
	if *deleteData {
		fmt.Fprintln(stdout, "로컬 DB를 삭제했습니다. 별도 백업이 없으면 복구할 수 없습니다.")
	}
	return 0
}

func stopForUninstall(ctx context.Context, statePath, dataDir string) error {
	m, err := defaultManagerFactory("", nil)
	if err != nil && !errors.Is(err, autostart.ErrUnsupportedPlatform) {
		return err
	}
	if err == nil {
		defaultState, err := installer.DefaultStatePath()
		if err != nil {
			return err
		}
		if !strings.EqualFold(filepath.Clean(statePath), filepath.Clean(defaultState)) {
			status, err := m.Status(ctx)
			if err != nil {
				return err
			}
			if status.Registered {
				return errors.New("사용자 지정 상태의 제거는 기본 자동 시작 서비스를 해제하지 않는다. 해당 서비스를 수동 해제한 뒤 다시 실행하라")
			}
			return localapi.StopDaemon(ctx, dataDir)
		}
		if _, err = m.Disable(ctx); err != nil {
			return err
		}
	}
	stopCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return localapi.StopDaemon(stopCtx, dataDir)
}
