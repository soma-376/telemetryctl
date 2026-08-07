// Package credential 은 enrollment 로 발급받은 설치 자격증명(installation_token)의
// 로컬 보관소다. OS 키링(Windows Credential Manager·macOS Keychain·Linux Secret
// Service)에 저장하므로 디스크에 평문 파일로 남지 않는다. 이 자격증명은
// telemetry_token 재발급 시에만 사용한다. Claude/Codex 설정에는 이 토큰이 아니라
// 교체 가능한 telemetry_token을 기록하고, state.json에도 비밀을 두지 않는다.
package credential

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// 키링 조회 키. 사용자당 설치 하나를 가정하므로 account 는 고정값이다.
const (
	keyringService = "pulsemetry"
	keyringAccount = "installation"
)

type Credential struct {
	InstallationID    string `json:"installation_id"`
	InstallationToken string `json:"installation_token"`
}

func SaveInstallationToken(cred *Credential) error {
	if cred == nil || cred.InstallationID == "" || cred.InstallationToken == "" {
		return fmt.Errorf("자격증명 저장: installation_id·installation_token 은 비울 수 없음")
	}
	b, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	if err := keyring.Set(keyringService, keyringAccount, string(b)); err != nil {
		return fmt.Errorf("자격증명 저장 실패(키링): %w", err)
	}
	return nil
}

func LoadInstallationToken() (*Credential, error) {
	s, err := keyring.Get(keyringService, keyringAccount)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("자격증명 조회 실패(키링): %w", err)
	}
	var cred Credential
	if err := json.Unmarshal([]byte(s), &cred); err != nil {
		return nil, fmt.Errorf("자격증명 파싱 실패: %w", err)
	}
	return &cred, nil
}

func DeleteInstallationToken() error {
	if err := keyring.Delete(keyringService, keyringAccount); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("자격증명 삭제 실패(키링): %w", err)
	}
	return nil
}
