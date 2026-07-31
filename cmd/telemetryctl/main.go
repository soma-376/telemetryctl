// Command telemetryctl 은 조직의 OpenTelemetry 설정을 Codex·Claude Code 에
// 안전하게 적용하는 CLI 다. 현재는 로컬 manifest 파일을 입력으로 받는다
// (Enrollment API 연동은 후속 티켓).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/your-org/telemetryctl/internal/installer"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "install":
		os.Exit(cmdInstall(os.Args[2:]))
	case "status":
		os.Exit(cmdStatus(os.Args[2:]))
	case "version", "--version", "-v":
		fmt.Println("telemetryctl", installer.Version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "알 수 없는 명령: %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `telemetryctl — Codex·Claude Code OTel 설정 도구

사용법:
  telemetryctl install --manifest <path> [--force]   manifest 를 읽어 설정 적용
  telemetryctl status                                 현재 설치 상태 표시
  telemetryctl version                                버전 출력
`)
}

func cmdInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "", "적용할 manifest(JSON) 파일 경로 (필수)")
	force := fs.Bool("force", false, "다른 OTel endpoint 가 있어도 강제 교체 (§4.2)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *manifestPath == "" {
		fmt.Fprintln(os.Stderr, "오류: --manifest 는 필수입니다")
		return 2
	}

	opts, err := installer.DefaultPaths(*manifestPath, *force)
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		return 1
	}
	rep, err := installer.Install(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "설치 실패:", err)
		return 1
	}

	// 토큰은 출력하지 않는다 (§4.5).
	fmt.Printf("설치 완료 · installation_id=%s · config_revision=%d\n", rep.InstallationID, rep.ConfigRevision)
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
	return 0
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
	return 0
}
