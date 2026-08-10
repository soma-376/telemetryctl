// Command pulsemetry 은 조직의 OpenTelemetry 설정을 Codex·Claude Code 에 안전하게 적용하는 CLI 다.
// 초대 코드로 enrollment 서버에 등록(enroll)해 설정 봉투를 받아 적용한다.
// 보통은 한 줄 설치 부트스트랩(irm <server>/windows | iex)이 이 바이너리를 받아 enroll 을 실행한다.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/daemon"
	"github.com/your-org/pulsemetry/internal/enrollment"
	"github.com/your-org/pulsemetry/internal/installer"
)

// defaultServer 는 enrollment 서버 URL 의 빌드 기본값이다. 릴리스 빌드에서 주입한다:
//
//	go build -ldflags "-X main.defaultServer=https://get.your-service.com" ./cmd/telemetryctl
//
// 개발 중에는 --server 또는 PULSEMETRY_SERVER 로 지정한다(하드코딩된 localhost 기본값은 두지 않는다).
var defaultServer = ""

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "enroll":
		os.Exit(cmdEnroll(os.Args[2:]))
	case "status":
		os.Exit(cmdStatus(os.Args[2:]))
	case "reconnect":
		os.Exit(cmdReconnect(os.Args[2:]))
	case "daemon":
		os.Exit(cmdDaemon(os.Args[2:]))
	case "version", "--version", "-v":
		fmt.Println("pulsemetry", installer.Version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "알 수 없는 명령: %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `pulsemetry — Codex·Claude Code OTel 설정 도구

사용법:
  pulsemetry enroll --invite <code> [--server <url>] [--force]   초대 코드로 등록 후 설정 적용
  pulsemetry reconnect [--server <url>]                          저장된 설치 자격증명으로 텔레메트리 토큰 재발급
  pulsemetry status                                             현재 설치 상태 표시
  pulsemetry daemon [--interval 30s]                            foreground 데몬 실행
  pulsemetry version                                            버전 출력

보통은 한 줄 설치로 실행됩니다:  irm <server>/windows | iex
`)
}

func cmdDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	interval := fs.Duration("interval", 30*time.Second, "주기 작업 실행 간격")
	statePath := fs.String("state", "", "설치 상태 파일 경로")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path := *statePath
	if path == "" {
		var err error
		path, err = installer.DefaultStatePath()
		if err != nil {
			fmt.Fprintln(os.Stderr, "오류:", err)
			return 1
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := log.New(os.Stdout, "pulsemetry: ", log.LstdFlags|log.LUTC)
	if err := daemon.Run(ctx, daemon.Options{StatePath: path, Interval: *interval, Logger: logger}); err != nil {
		fmt.Fprintln(os.Stderr, "daemon 실패:", err)
		return 1
	}
	return 0
}

func cmdEnroll(args []string) int {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)

	invite := fs.String("invite", "", "초대 코드 (필수)")
	server := fs.String("server", "", "enrollment 서버 URL (미지정 시 PULSEMETRY_SERVER/빌드 기본값)")
	force := fs.Bool("force", false, "다른 OTel endpoint 가 있어도 강제 교체")
	quiet := fs.Bool("quiet", false, "상세 출력 억제(백업 경로만 표시)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *invite == "" {
		fmt.Fprintln(os.Stderr, "오류: --invite 는 필수입니다")
		return 2
	}

	srv := resolveServer(*server)
	if srv == "" {
		fmt.Fprintln(os.Stderr, "오류: enrollment 서버 URL 이 없습니다. --server 또는 PULSEMETRY_SERVER 를 지정하세요.")
		return 2
	}

	hostname, _ := os.Hostname()
	enr, err := enrollment.Enroll(srv, contract.EnrollRequest{
		Code:          *invite,
		Platform:      runtime.GOOS,
		Architecture:  runtime.GOARCH,
		Hostname:      hostname,
		ClientVersion: installer.Version,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "enroll 실패:", err)
		return 1
	}

	opts, err := installer.DefaultPaths(*force)
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		return 1
	}
	opts.ServerURL = srv
	rep, err := installer.Apply(enr, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "설정 적용 실패:", err)
		return 1
	}
	if *quiet {
		printBackups(rep)
	} else {
		printReport(rep)
	}
	return 0
}

func cmdReconnect(args []string) int {
	fs := flag.NewFlagSet("reconnect", flag.ContinueOnError)
	server := fs.String("server", "", "enrollment 서버 URL (미지정 시 기존 설치 상태/PULSEMETRY_SERVER/빌드 기본값)")
	statePath := fs.String("state", "", "설치 상태 파일 경로")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path := *statePath
	if path == "" {
		var err error
		path, err = installer.DefaultStatePath()
		if err != nil {
			fmt.Fprintln(os.Stderr, "오류:", err)
			return 1
		}
	}

	serverOverride := resolveServer(*server)
	rep, err := installer.Reconnect(path, serverOverride)
	if err != nil {
		fmt.Fprintln(os.Stderr, "재연결 실패:", err)
		return 1
	}
	fmt.Printf("재연결 완료 · installation_id=%s\n", rep.InstallationID)
	for _, target := range rep.Targets {
		fmt.Printf("  - %s\n", target.Path)
	}
	return 0
}

// resolveServer 는 서버 URL 을 flag > PULSEMETRY_SERVER env > 빌드 기본값 순으로 결정한다.
func resolveServer(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("PULSEMETRY_SERVER"); v != "" {
		return v
	}
	return defaultServer
}

// printReport 는 적용 결과를 출력한다. 토큰은 출력하지 않는다 (§4.5).
func printReport(rep *installer.Report) {
	fmt.Printf("완료 · installation_id=%s · config_revision=%d\n", rep.InstallationID, rep.ConfigRevision)
	for _, t := range rep.Targets {
		action := "수정"
		if t.Created {
			action = "생성"
		}
		fmt.Printf("  - %s (%s)\n", t.Path, action)
		if t.BackupPath != "" {
			fmt.Printf("    백업: %s\n", t.BackupPath)
		}
		fmt.Printf("    관리 키: %v\n", t.ManagedKeys)
	}
}

// printBackups 는 quiet 모드에서 백업이 생성된 대상의 백업 경로만 출력한다.
func printBackups(rep *installer.Report) {
	for _, t := range rep.Targets {
		if t.BackupPath != "" {
			fmt.Printf("  백업: %s\n", t.BackupPath)
		}
	}
}

func cmdStatus(_ []string) int {
	path, err := installer.DefaultStatePath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		return 1
	}
	st, err := installer.LoadState(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		return 1
	}
	if st == nil {
		fmt.Printf("미설치 (상태 파일 없음: %s)\n", path)
		return 0
	}
	fmt.Printf("installation_id=%s · config_revision=%d · installer=%s · installed_at=%s\n",
		st.InstallationID, st.ConfigRevision, st.InstallerVersion, st.InstalledAt)
	for _, t := range st.Targets {
		fmt.Printf("  - [%s] %s (관리 키 %d개)\n", t.Tool, t.Path, len(t.ManagedKeys))
	}
	printCredentialStatus()
	return 0
}

// printCredentialStatus 는 키링에 저장된 자격증명의 존재·조회 가능 여부만 표시한다.
// 토큰 값은 출력하지 않는다 (§4.5).
func printCredentialStatus() {
	cred, err := credential.LoadInstallation()
	switch {
	case err != nil:
		fmt.Printf("  자격증명: 읽기 실패 (%v)\n", err)
	case cred == nil:
		fmt.Printf("  자격증명: 없음 (OS 키링) — 구버전 설치이거나 유실됨, 재enroll 필요\n")
	default:
		fmt.Printf("  자격증명: 정상 (OS 키링)\n")
	}
}
