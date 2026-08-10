// Package credential 은 로컬 비밀의 OS 키링(Windows Credential Manager·macOS
// Keychain·Linux Secret Service) 보관소다. 디스크에 평문 파일로 남지 않는다.
// 대표 항목인 설치 자격증명(installation_token)은 telemetry_token 재발급 시에만
// 사용한다. Claude/Codex 설정에는 이 토큰이 아니라 교체 가능한 telemetry_token을
// 기록하고, state.json에도 비밀을 두지 않는다.
package credential

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// keyringService 는 모든 항목이 공유하는 키링 서비스 이름이다. 이미 설치된 사용자의
// 항목을 계속 읽어야 하므로 바꾸면 안 된다.
const keyringService = "pulsemetry"

// Account 는 키링 항목 하나를 가리키는 계정 이름이다. 비밀 종류가 늘어나도 임의
// 문자열을 받지 않고 아래 상수만 쓰게 해서, 오타가 조용히 다른 항목을 읽거나
// 덮어쓰는 일을 막는다.
type Account string

const (
	// AccountInstallation 은 enrollment 가 발급한 설치 자격증명(Credential JSON)이다.
	// PROJ-11 부터 이 이름으로 저장돼 왔으므로 기존 설치 호환을 위해 절대 바꾸지 않는다.
	AccountInstallation Account = "installation"

	// AccountLocalIngest 는 로컬 수신기의 loopback ingest 토큰이다. 토큰 생성과 사용은
	// 수신기가 들어올 때 붙고, 여기서는 계정 이름만 확정한다 (PROJ-36).
	AccountLocalIngest Account = "local-ingest"
)

// knownAccounts 는 Set/Get/Delete 가 허용하는 계정 전체다.
var knownAccounts = map[Account]struct{}{
	AccountInstallation: {},
	AccountLocalIngest:  {},
}

// resolve 는 Account 를 키링 조회 좌표로 바꾼다. Account 는 string 기반이라
// credential.Account("instalation") 같은 값이 만들어질 수 있는데, 그대로 키링에
// 넘기면 조용히 빈 항목을 읽는다. 정의되지 않은 계정은 여기서 거른다.
func (a Account) resolve() (service, account string, err error) {
	if _, ok := knownAccounts[a]; !ok {
		return "", "", fmt.Errorf("알 수 없는 키링 계정 %q", string(a))
	}
	return keyringService, string(a), nil
}

// Set 은 account 항목에 secret 을 저장한다.
func Set(account Account, secret string) error {
	service, name, err := account.resolve()
	if err != nil {
		return err
	}
	if secret == "" {
		return fmt.Errorf("키링 저장 %s: 비밀은 비울 수 없음", name)
	}
	if err := keyring.Set(service, name, secret); err != nil {
		return fmt.Errorf("키링 저장 실패 %s: %w", name, err)
	}
	return nil
}

// Get 은 account 항목을 읽는다. 항목이 없으면 found=false 이며 오류가 아니다.
func Get(account Account) (secret string, found bool, err error) {
	service, name, err := account.resolve()
	if err != nil {
		return "", false, err
	}
	s, err := keyring.Get(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("키링 조회 실패 %s: %w", name, err)
	}
	return s, true, nil
}

// Delete 는 account 항목을 지운다. 이미 없으면 성공으로 본다.
func Delete(account Account) error {
	service, name, err := account.resolve()
	if err != nil {
		return err
	}
	if err := keyring.Delete(service, name); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("키링 삭제 실패 %s: %w", name, err)
	}
	return nil
}

// Credential 은 AccountInstallation 항목에 JSON 으로 저장되는 설치 자격증명이다.
type Credential struct {
	InstallationID    string `json:"installation_id"`
	InstallationToken string `json:"installation_token"`
}

// SaveInstallation 은 설치 자격증명을 키링에 저장한다.
func SaveInstallation(cred *Credential) error {
	if cred == nil || cred.InstallationID == "" || cred.InstallationToken == "" {
		return fmt.Errorf("자격증명 저장: installation_id·installation_token 은 비울 수 없음")
	}
	b, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	return Set(AccountInstallation, string(b))
}

// LoadInstallation 은 설치 자격증명을 읽는다. 항목이 없으면 (nil, nil) 을 반환한다.
func LoadInstallation() (*Credential, error) {
	s, found, err := Get(AccountInstallation)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	var cred Credential
	if err := json.Unmarshal([]byte(s), &cred); err != nil {
		return nil, fmt.Errorf("자격증명 파싱 실패: %w", err)
	}
	return &cred, nil
}

// DeleteInstallation 은 설치 자격증명을 지운다.
func DeleteInstallation() error {
	return Delete(AccountInstallation)
}
