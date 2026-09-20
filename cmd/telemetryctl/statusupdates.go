package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/your-org/pulsemetry/internal/localapi"
	"github.com/your-org/pulsemetry/internal/updatecheck"
)

// printUpdateStatus는 DB 상태와 독립적으로 업데이트 확인 결과를 표시한다.
func printUpdateStatus(w io.Writer, target localTarget) {
	ctx, cancel := context.WithTimeout(context.Background(), healthTimeout)
	defer cancel()
	snapshot, err := localapi.NewClient(target.DataDir).Updates(ctx)
	if err != nil {
		// --data-dir와 --state는 독립적이다. 실행 데몬이 있으면 그 상태가 정본이며,
		// 로컬 좌표도 없을 때만 CLI가 읽은 설치 상태로 미등록을 설명한다.
		if errors.Is(err, localapi.ErrDaemonNotRunning) && (target.State == nil || target.State.ServerURL == "") {
			fmt.Fprintln(w, "  데몬 업데이트: 비활성 (등록 서버 없음)")
			return
		}
		fmt.Fprintln(w, "  데몬 업데이트: 확인 불가 (데몬 연결 또는 로컬 API 조회 실패)")
		return
	}
	printUpdateSnapshot(w, snapshot)
}

func printUpdateSnapshot(w io.Writer, snapshot updatecheck.Snapshot) {
	state := "확인 불가"
	switch snapshot.Status {
	case updatecheck.StatusDisabled:
		state = "비활성 (등록 서버 없음)"
	case updatecheck.StatusChecking:
		state = "확인 중"
	case updatecheck.StatusReady:
		if snapshot.UpdateAvailable != nil {
			state = "최신 버전"
			if *snapshot.UpdateAvailable {
				state = "새 버전 있음"
			}
		}
	case updatecheck.StatusUnsupported:
		state = "서버에서 업데이트 확인을 지원하지 않음"
	case updatecheck.StatusError:
		state = "서버 확인 실패"
	}
	fmt.Fprintf(w, "  데몬 업데이트: %s · 데몬 버전 %s\n", state, orDash(snapshot.CurrentVersion))
	if snapshot.LastSuccessAt != "" && snapshot.UpdateAvailable != nil {
		result := "업데이트 없음"
		if *snapshot.UpdateAvailable {
			result = "업데이트 가능"
		}
		fmt.Fprintf(w, "    마지막 성공 결과: %s · 서버 최신 버전 %s · 확인 %s\n", result, snapshot.LatestVersion, snapshot.LastSuccessAt)
	}
	if snapshot.LastAttemptAt != "" && snapshot.Status != updatecheck.StatusReady {
		fmt.Fprintf(w, "    마지막 확인 시도: %s\n", snapshot.LastAttemptAt)
	}
}
