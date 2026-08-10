package main

// telemetryctl purge --content — 로컬에 보관된 프롬프트·툴 원문 삭제 (계획서 「CLI 변경」).
//
// # 안전장치
//
// 이 명령은 되돌릴 수 없다. 그래서 세 가지를 지킨다.
//
//  1. 무엇이 사라지는지 **먼저** 말한다 (보관 행 수·경계·남는 것).
//  2. --before 없이 전체를 지울 때는 확인을 받는다. 사고의 크기가 다르다 —
//     구간 삭제는 오래된 것을 지우지만 전체 삭제는 방금 만든 세션의 원문까지 지운다.
//  3. 스크립트에서 쓸 수 있게 --yes 로 확인을 건너뛸 수 있다. 다만 확인 입력을 받을 수
//     없는 환경(파이프·CI)에서 --yes 가 없으면 **멈춰 서지 않고 거부** 한다.
//     프롬프트를 띄워 놓고 영원히 기다리는 것이 가장 나쁜 결과다.

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/your-org/pulsemetry/internal/store"
)

// purgeTimeout 은 삭제 한 번의 상한이다. 데몬이 쓰는 중이면 busy_timeout(5초)만큼
// 기다릴 수 있으므로 그보다 넉넉하게 둔다.
const purgeTimeout = 60 * time.Second

func cmdPurge(args []string) int {
	return runPurge(os.Stdout, os.Stderr, os.Stdin, stdinIsTerminal(), args)
}

func runPurge(stdout, stderr io.Writer, stdin io.Reader, canPrompt bool, args []string) int {
	fs := flag.NewFlagSet("purge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	content := fs.Bool("content", false, "보관된 프롬프트·툴 원문을 지운다 (필수)")
	before := fs.String("before", "", "이 시각 이전 원문만 지운다 (2026-07-01 또는 RFC3339)")
	yes := fs.Bool("yes", false, "확인 없이 진행 (스크립트용)")
	dataDir := fs.String("data-dir", "", "데이터 디렉터리 (미지정 시 상태 파일 설정 → ~/.pulsemetry)")
	statePath := fs.String("state", "", "설치 상태 파일 경로")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if !*content {
		// 지금은 대상이 하나뿐이지만 그래도 명시를 요구한다. `purge` 만 쳐서 무언가
		// 지워지는 명령은 있으면 안 된다.
		fmt.Fprintln(stderr, "오류: 지울 대상을 지정해야 합니다: purge --content")
		return 2
	}
	cutoff, err := parseBefore(*before)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 2
	}

	target, err := resolveLocalTarget(*dataDir, *statePath)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 1
	}
	warnStateErr(stderr, target)

	if _, err := os.Stat(target.DBPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// DB 가 없다고 만들면 안 된다. store.Open 은 파일을 생성하므로 먼저 확인한다.
			fmt.Fprintf(stdout, "로컬 DB 가 없습니다: %s\n지울 원문이 없습니다.\n", target.DBPath)
			return 0
		}
		fmt.Fprintln(stderr, "오류: DB 파일 확인 실패:", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), purgeTimeout)
	defer cancel()

	// 먼저 read-only 로 현황을 읽어 무엇이 사라지는지 말한다. 쓰기 핸들을 열기 전에
	// 닫아 두면 데몬이 도는 중에도 잡고 있는 핸들이 하나뿐이다.
	if err := printPurgePlan(ctx, stdout, target, cutoff); err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 1
	}

	if cutoff.IsZero() && !*yes {
		ok, err := confirm(stdout, stdin, canPrompt)
		if err != nil {
			fmt.Fprintln(stderr, "오류:", err)
			return 2
		}
		if !ok {
			fmt.Fprintln(stdout, "취소했습니다. 아무것도 지우지 않았습니다.")
			return 1
		}
	}

	db, err := store.Open(ctx, target.DBPath)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 1
	}
	defer db.Close() //nolint:errcheck // 삭제는 커밋됐고 닫기 실패에 되돌릴 것이 없다

	n, err := db.PurgeContent(ctx, cutoff)
	if err != nil {
		fmt.Fprintln(stderr, "오류:", err)
		return 1
	}
	fmt.Fprintf(stdout, "원문 %s행을 지웠습니다.\n", formatInt(n))
	return 0
}

// printPurgePlan 은 지우기 **전에** 현황과 경계를 알린다.
func printPurgePlan(ctx context.Context, w io.Writer, t localTarget, cutoff time.Time) error {
	reader, err := openReader(t)
	if err != nil {
		return err
	}
	defer reader.Close() //nolint:errcheck // 조회 전용 핸들이다

	st, err := reader.Status(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "DB: %s\n", st.DatabasePath)
	fmt.Fprintf(w, "보관 중인 원문: %s행 (이벤트 %s건)\n",
		formatInt(st.Counts.EventContent), formatInt(st.Counts.Events))
	if st.Daemon.Found && st.Daemon.Running {
		// WAL 이라 동시 접근 자체는 안전하다. 그래도 알려야 하는 이유는 결과가 달라
		// 보이기 때문이다 — 지운 직후에도 새 원문이 계속 쌓인다.
		fmt.Fprintf(w, "데몬이 실행 중입니다 (pid %d). 동시 삭제는 안전하지만 이후 도착하는 원문은 계속 쌓입니다.\n",
			st.Daemon.PID)
	}
	if cutoff.IsZero() {
		fmt.Fprintln(w, "대상: 보관된 원문 전체 (구간 제한 없음)")
	} else {
		fmt.Fprintf(w, "대상: %s (%s) 이전 원문\n",
			cutoff.In(time.Local).Format(timeLayout), zoneLabel(cutoff))
	}
	fmt.Fprintln(w, "이벤트 수치와 롤업은 남고 원문 검색만 불가능해집니다. 되돌릴 수 없습니다.")
	return nil
}

// parseBefore 는 --before 값을 시각으로 옮긴다. 빈 값은 제로값(= 전체 삭제)이다.
//
// 날짜만 주면 **로컬 자정** 으로 본다. UTC 자정으로 읽으면 사용자가 "7월 1일 이전" 이라고
// 말한 것보다 아홉 시간 더(또는 덜) 지워진다.
func parseBefore(v string) (time.Time, error) {
	s := strings.TrimSpace(v)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("--before 값을 해석할 수 없습니다 %q: 2026-07-01 또는 RFC3339 형식으로 쓰세요", v)
}

// confirm 은 "yes" 를 요구한다. y·Y 를 받지 않는 것은 의도다 — 되돌릴 수 없는 명령의
// 확인은 손가락이 미끄러져 통과되면 안 된다.
func confirm(w io.Writer, r io.Reader, canPrompt bool) (bool, error) {
	if !canPrompt {
		return false, errors.New("확인 입력을 받을 수 없습니다. 비대화 실행에서는 --yes 를 붙이거나 --before 로 구간을 지정하세요")
	}
	fmt.Fprint(w, "계속하려면 yes 를 입력하세요: ")
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	return strings.TrimSpace(line) == "yes", nil
}

// stdinIsTerminal 은 확인 프롬프트를 띄워도 되는지 본다. 파이프·리다이렉트·CI 에서는
// 아무도 답하지 않으므로 프롬프트 대신 --yes 를 요구해야 한다.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
