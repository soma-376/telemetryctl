// Command pulsemetry 은 조직의 OpenTelemetry 설정을 Codex·Claude Code 에 안전하게 적용하는 CLI 다.
// 초대 코드로 enrollment 서버에 등록(enroll)해 설정 봉투를 받아 적용한다.
// 보통은 한 줄 설치 부트스트랩(irm <server>/windows | iex)이 이 바이너리를 받아 enroll 을 실행한다.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

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
	case "stats":
		os.Exit(cmdStats(os.Args[2:]))
	case "sessions":
		os.Exit(cmdSessions(os.Args[2:]))
	case "purge":
		os.Exit(cmdPurge(os.Args[2:]))
	case "local":
		os.Exit(cmdLocal(os.Args[2:]))
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
  pulsemetry status                                              현재 설치·로컬 파이프라인 상태 표시
  pulsemetry daemon [옵션]                                       foreground 데몬 실행
  pulsemetry stats [옵션]                                        로컬 집계 조회
  pulsemetry sessions [옵션]                                     로컬 세션 목록 조회
  pulsemetry purge --content [옵션]                              보관된 프롬프트·툴 원문 삭제
  pulsemetry local enable|disable [옵션]                         벤더 설정을 로컬 수신기로 재배선/해제
  pulsemetry version                                             버전 출력

daemon 옵션:
  --listen <localhost:4318>   수신기 주소. 명시하면 포트 폴백 없이 하드 실패
  --data-dir <경로>           SQLite·runtime.json 위치 (기본 ~/.pulsemetry)
  --no-receiver               로컬 OTLP 수신기를 띄우지 않음
  --no-forward                회사 Collector 전달 없이 수신·로컬 집계만
  --no-store-content          프롬프트·툴 원문을 로컬에 저장하지 않음
  --retention-days <일>       이벤트·원문·툴 타임라인 보존일 (기본 30)
  --interval <30s>            세션 마감·롤업 저장 주기

stats 옵션:
  --since <7d>                조회 구간. 지금부터 거슬러 올라간다 (7d·24h·90m, 최대 400d)
  --group <vendor>            집계 축 (vendor|model|tool|project|day)
  --limit <20>                표시할 행 수 (1~500). --group day 에는 적용되지 않음
  --json                      기계 판독용 JSON. 시각은 전부 UTC unix 초

sessions 옵션:
  --since <7d>                조회 구간 (started_at 기준)
  --status <값>               running|completed|abandoned|handoff (쉼표로 여러 개)
  --limit <50>                표시할 세션 수 (1~1000)
  --json                      기계 판독용 JSON. 시각은 전부 UTC unix 초

purge 옵션:
  --content                   보관된 프롬프트·툴 원문을 지운다 (필수)
  --before <2026-07-01>       이 시각(로컬) 이전 원문만 지운다. 없으면 전체
  --yes                       전체 삭제 확인을 건너뛴다 (스크립트용)

local 옵션:
  --port <4318>               enable 전용. 재배선이 가리킬 로컬 수신 포트
                              (미지정 시 상태 파일 설정 → 데몬이 실제로 듣는 포트 → 4318)

  local enable 은 Claude Code·Codex 설정의 endpoint 를 http://localhost:<포트> 로 돌리고
  프롬프트·tool details 수집을 강제로 켭니다. 회사로 나가는 데이터는 그대로입니다 —
  데몬이 manifest Privacy 기준으로 제거한 뒤 전달합니다. local disable 로 정확히 되돌립니다.

stats·sessions·purge·local·status 공통:
  --data-dir <경로>           데이터 디렉터리 (미지정 시 상태 파일 설정 → ~/.pulsemetry)
  --state <경로>              설치 상태 파일 경로

보통은 한 줄 설치로 실행됩니다:  irm <server>/windows | iex
`)
}

func cmdDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	interval := fs.Duration("interval", daemon.DefaultInterval, "세션 마감·롤업 저장 주기")
	statePath := fs.String("state", "", "설치 상태 파일 경로")
	listen := fs.String("listen", "", "수신기 주소 (localhost:4318 또는 4318). 명시하면 포트 폴백 없이 실패한다")
	dataDir := fs.String("data-dir", "", "데이터 디렉터리 (미지정 시 상태 파일 설정 → ~/.pulsemetry)")
	noReceiver := fs.Bool("no-receiver", false, "로컬 OTLP 수신기를 띄우지 않는다")
	noForward := fs.Bool("no-forward", false, "회사 Collector 전달을 하지 않는다 (수신·로컬 집계만)")
	noStoreContent := fs.Bool("no-store-content", false, "프롬프트·툴 원문을 로컬에 저장하지 않는다")
	retentionDays := fs.Int("retention-days", 0, "이벤트·원문·툴 타임라인 보존일 (미지정 시 상태 파일 설정 → 30)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	port, fixed, err := parseListen(*listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
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
	if err := daemon.Run(ctx, daemon.Options{
		StatePath:       path,
		Logger:          logger,
		Interval:        *interval,
		DataDir:         *dataDir,
		ListenPort:      port,
		FixedPort:       fixed,
		DisableReceiver: *noReceiver,
		DisableForward:  *noForward,
		NoStoreContent:  *noStoreContent,
		RetentionDays:   *retentionDays,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "daemon 실패:", err)
		return 1
	}
	return 0
}

// parseListen 은 --listen 값을 포트로 옮긴다. 값이 있으면 fixed=true 이고, 그때는
// 그 포트를 잡지 못해도 임의 포트로 폴백하지 않는다 (계획서 「리스크」).
//
// loopback 이 아닌 호스트는 여기서 거부한다. 수신기는 어차피 127.0.0.1 과 [::1] 만
// 바인딩하므로, 다른 호스트를 받아 조용히 무시하면 사용자가 왜 안 되는지 알 수 없다.
func parseListen(v string) (port int, fixed bool, err error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false, nil
	}
	hostPart, portPart := "", v
	if strings.Contains(v, ":") {
		hostPart, portPart, err = net.SplitHostPort(v)
		if err != nil {
			return 0, false, fmt.Errorf("--listen 값을 해석할 수 없습니다 %q: %w", v, err)
		}
	}
	switch hostPart {
	case "", "localhost", "127.0.0.1", "::1":
	default:
		return 0, false, fmt.Errorf("--listen 은 loopback 만 받습니다 (localhost·127.0.0.1·[::1]): %q", hostPart)
	}
	port, err = strconv.Atoi(portPart)
	if err != nil || port <= 0 || port > 65535 {
		return 0, false, fmt.Errorf("--listen 포트가 올바르지 않습니다: %q", portPart)
	}
	return port, true, nil
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

func cmdStatus(args []string) int { return runStatus(os.Stdout, os.Stderr, args) }

// runStatus 는 설치 상태와 로컬 파이프라인 상태를 함께 보여 준다 (PROJ-36 11단계).
//
// status 는 진단 명령이라 어떤 상태에서도 동작해야 한다. 미설치여도 로컬 블록은 출력한다 —
// enroll 전에 데몬만 띄워 본 사용자에게 "미설치" 한 줄만 주면 아무것도 진단할 수 없다.
func runStatus(stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dataDir := fs.String("data-dir", "", "데이터 디렉터리 (미지정 시 상태 파일 설정 → ~/.pulsemetry)")
	statePath := fs.String("state", "", "설치 상태 파일 경로")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	target, err := resolveLocalTarget(*dataDir, *statePath)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 1
	}
	// 상태 파일 파싱 실패는 설치가 깨졌다는 뜻이라 조용히 넘기지 않는다.
	if target.StateErr != nil {
		fmt.Fprintln(stderr, "오류:", target.StateErr)
		return 1
	}

	if st := target.State; st == nil {
		fmt.Fprintf(stdout, "미설치 (상태 파일 없음: %s)\n", target.StatePath)
	} else {
		fmt.Fprintf(stdout, "installation_id=%s · config_revision=%d · installer=%s · installed_at=%s\n",
			st.InstallationID, st.ConfigRevision, st.InstallerVersion, st.InstalledAt)
		for _, t := range st.Targets {
			fmt.Fprintf(stdout, "  - [%s] %s (관리 키 %d개)\n", t.Tool, t.Path, len(t.ManagedKeys))
		}
		printCredentialStatus(stdout)
	}
	printLocalStatus(stdout, target)
	return 0
}

// printCredentialStatus 는 키링에 저장된 자격증명의 존재·조회 가능 여부만 표시한다.
// 토큰 값은 출력하지 않는다 (§4.5).
func printCredentialStatus(w io.Writer) {
	cred, err := credential.LoadInstallation()
	switch {
	case err != nil:
		fmt.Fprintf(w, "  자격증명: 읽기 실패 (%v)\n", err)
	case cred == nil:
		fmt.Fprintf(w, "  자격증명: 없음 (OS 키링) — 구버전 설치이거나 유실됨, 재enroll 필요\n")
	default:
		fmt.Fprintf(w, "  자격증명: 정상 (OS 키링)\n")
	}
}
