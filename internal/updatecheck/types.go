// Package updatecheck 는 등록 서버의 업데이트 정보를 조회하고 마지막 성공값을 보관한다.
package updatecheck

import "context"

const (
	StatusDisabled    = "disabled"
	StatusChecking    = "checking"
	StatusReady       = "ready"
	StatusUnsupported = "unsupported"
	StatusError       = "error"
)

// Snapshot 은 데몬이 소유하고 CLI·GUI가 읽는 업데이트 상태다.
// UpdateAvailable이 nil이면 아직 성공한 조회가 없다. 시각은 UTC RFC3339이며 미확인은 빈 문자열이다.
type Snapshot struct {
	Status          string `json:"status"`
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable *bool  `json:"update_available"`
	LastAttemptAt   string `json:"last_attempt_at"`
	LastSuccessAt   string `json:"last_success_at"`
}

// Result 는 서버가 판정한 결과다. 클라이언트가 버전을 다시 비교하지 않는다.
type Result struct {
	LatestVersion   string
	UpdateAvailable bool
}

// Checker 는 HTTP 조회와 데몬 주기 작업 사이의 테스트 경계다.
type Checker interface {
	Check(context.Context) (Result, error)
}
