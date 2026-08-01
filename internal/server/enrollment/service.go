// Package enrollment 은 enrollment 서버의 등록 로직이다. 초대 코드를 검증해 installation 을 만들고
// 설치별 토큰과 설정 봉투(manifest)를 발급한다 (docs/enrollment-server-spec.md §4.1).
package enrollment

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/server/store"
)

// Service 는 저장소에 기대어 등록을 수행한다.
type Service struct {
	store *store.Memory
}

func NewService(s *store.Memory) *Service { return &Service{store: s} }

// Enroll 은 초대 코드를 검증해 installation 을 만들고 토큰+봉투를 발급한다.
// 반환하는 Enrollment 봉투는 클라이언트가 그대로 ParseEnrollment 로 소비하는 공유 계약이다.
func (s *Service) Enroll(req contract.EnrollRequest) (*contract.Enrollment, error) {
	if req.Invite == "" {
		return nil, store.ErrInviteNotFound
	}
	tenantID, err := s.store.ClaimInvite(req.Invite)
	if err != nil {
		return nil, err
	}
	cfg, err := s.store.TenantConfig(tenantID)
	if err != nil {
		return nil, err
	}

	instID := "ins_" + randStr(9)
	token := "inst_" + randStr(24)
	s.store.AddInstallation(store.Installation{ID: instID, TenantID: tenantID, DeviceID: req.DeviceID})
	s.store.AddTokenHash(hashToken(token), instID) // 서버는 해시만 보관 (§6)

	return &contract.Enrollment{
		InstallationID:    instID,
		InstallationToken: token,
		Manifest:          cfg,
	}, nil
}

// randStr 은 암호학적 난수 n바이트를 URL-safe base64 로 반환한다.
func randStr(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken 은 토큰을 SHA-256 으로 해시한다(저장·검증용).
func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}
