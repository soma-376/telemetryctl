package credential

import (
	"testing"

	"github.com/zalando/go-keyring"
)

// 이미 설치된 사용자의 키링 항목을 계속 읽으려면 (service, account) 문자열이 고정이어야
// 한다. 여기가 조용히 바뀌면 기존 설치가 재enroll 을 강요당하므로 값 자체를 못박는다.
func TestAccountResolveKeyringCoordinates(t *testing.T) {
	tests := []struct {
		name        string
		account     Account
		wantService string
		wantAccount string
		wantErr     bool
	}{
		{
			name:        "설치 자격증명은 PROJ-11 이후 계정 이름을 유지한다",
			account:     AccountInstallation,
			wantService: "pulsemetry",
			wantAccount: "installation",
		},
		{
			name:        "로컬 ingest 토큰은 별도 계정을 쓴다",
			account:     AccountLocalIngest,
			wantService: "pulsemetry",
			wantAccount: "local-ingest",
		},
		{
			name:    "정의되지 않은 계정은 거부한다",
			account: Account("instalation"),
			wantErr: true,
		},
		{
			name:    "빈 계정은 거부한다",
			account: Account(""),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, account, err := tt.account.resolve()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolve(%q): 오류를 기대했으나 %q/%q 를 반환", string(tt.account), service, account)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve(%q): 예상 밖 오류 %v", string(tt.account), err)
			}
			if service != tt.wantService || account != tt.wantAccount {
				t.Errorf("resolve(%q) = %q/%q, want %q/%q",
					string(tt.account), service, account, tt.wantService, tt.wantAccount)
			}
		})
	}
}

// 계정이 서로 다른 키에 매핑되지 않으면 수신기 토큰이 설치 자격증명을 덮어쓴다.
func TestAccountsAreDistinct(t *testing.T) {
	_, installation, err := AccountInstallation.resolve()
	if err != nil {
		t.Fatal(err)
	}
	_, ingest, err := AccountLocalIngest.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if installation == ingest {
		t.Fatalf("계정 이름이 겹침: %q", installation)
	}
}

// keyring.MockInit 은 라이브러리가 제공하는 인메모리 provider 라서 headless CI 에서도
// 안전하다. 전역 provider 를 갈아끼우므로 이 파일의 테스트는 병렬 실행하지 않는다.
func TestSetGetDeleteRoundTrip(t *testing.T) {
	keyring.MockInit()

	if _, found, err := Get(AccountLocalIngest); err != nil || found {
		t.Fatalf("빈 키링 조회 = (found=%v, err=%v), want (false, nil)", found, err)
	}
	if err := Set(AccountLocalIngest, "ingest-secret"); err != nil {
		t.Fatal(err)
	}
	secret, found, err := Get(AccountLocalIngest)
	if err != nil || !found || secret != "ingest-secret" {
		t.Fatalf("Get = (%q, %v, %v), want (\"ingest-secret\", true, nil)", secret, found, err)
	}
	if err := Delete(AccountLocalIngest); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Get(AccountLocalIngest); err != nil || found {
		t.Fatalf("삭제 후 조회 = (found=%v, err=%v), want (false, nil)", found, err)
	}
	// 없는 항목 삭제는 uninstall 을 실패시키지 않아야 한다.
	if err := Delete(AccountLocalIngest); err != nil {
		t.Fatalf("없는 항목 삭제: %v", err)
	}
}

// 두 계정을 같은 서비스에 저장해도 서로를 덮어쓰지 않는지 확인한다.
func TestAccountsDoNotOverwriteEachOther(t *testing.T) {
	keyring.MockInit()

	if err := SaveInstallation(&Credential{InstallationID: "inst_1", InstallationToken: "pit_secret"}); err != nil {
		t.Fatal(err)
	}
	if err := Set(AccountLocalIngest, "ingest-secret"); err != nil {
		t.Fatal(err)
	}

	cred, err := LoadInstallation()
	if err != nil {
		t.Fatal(err)
	}
	if cred == nil || cred.InstallationID != "inst_1" || cred.InstallationToken != "pit_secret" {
		t.Fatalf("LoadInstallation = %+v, 로컬 ingest 저장에 덮어쓰였음", cred)
	}

	// 설치 자격증명 삭제가 다른 계정을 건드리면 안 된다.
	if err := DeleteInstallation(); err != nil {
		t.Fatal(err)
	}
	secret, found, err := Get(AccountLocalIngest)
	if err != nil || !found || secret != "ingest-secret" {
		t.Fatalf("DeleteInstallation 후 ingest = (%q, %v, %v), want 유지", secret, found, err)
	}
}

func TestLoadInstallationMissingReturnsNil(t *testing.T) {
	keyring.MockInit()

	cred, err := LoadInstallation()
	if err != nil {
		t.Fatalf("미설치 상태 조회: %v", err)
	}
	if cred != nil {
		t.Fatalf("LoadInstallation = %+v, want nil", cred)
	}
}

// 불완전한 자격증명이 키링에 들어가면 재연결 시점에야 발견된다. 저장 전에 막는다.
func TestSaveInstallationRejectsIncomplete(t *testing.T) {
	tests := []struct {
		name string
		cred *Credential
	}{
		{name: "nil", cred: nil},
		{name: "installation_id 없음", cred: &Credential{InstallationToken: "pit_secret"}},
		{name: "installation_token 없음", cred: &Credential{InstallationID: "inst_1"}},
		{name: "둘 다 없음", cred: &Credential{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyring.MockInit()
			if err := SaveInstallation(tt.cred); err == nil {
				t.Fatal("불완전한 자격증명 저장이 성공했음")
			}
			cred, err := LoadInstallation()
			if err != nil {
				t.Fatal(err)
			}
			if cred != nil {
				t.Fatalf("거부됐는데 키링에 %+v 가 남음", cred)
			}
		})
	}
}

func TestSetRejectsEmptySecret(t *testing.T) {
	keyring.MockInit()

	if err := Set(AccountLocalIngest, ""); err == nil {
		t.Fatal("빈 비밀 저장이 성공했음")
	}
}

// 정의되지 않은 계정은 키링에 닿기 전에 막혀야 한다.
func TestUnknownAccountNeverTouchesKeyring(t *testing.T) {
	keyring.MockInit()

	const typo = Account("Installation") // 대문자 오타
	if err := Set(typo, "secret"); err == nil {
		t.Fatal("알 수 없는 계정에 저장이 성공했음")
	}
	if _, _, err := Get(typo); err == nil {
		t.Fatal("알 수 없는 계정 조회가 성공했음")
	}
	if err := Delete(typo); err == nil {
		t.Fatal("알 수 없는 계정 삭제가 성공했음")
	}
}
