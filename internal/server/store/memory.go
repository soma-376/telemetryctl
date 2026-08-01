// Package store 는 enrollment 서버의 저장소다. MVP 는 인메모리 구현으로 시작하고,
// 이후 Postgres 로 교체한다 (docs/enrollment-server-spec.md §8). 저장소는 스레드 안전하다.
package store

import (
	"errors"
	"sync"

	"github.com/your-org/pulsemetry/internal/contract"
)

var (
	ErrInviteNotFound  = errors.New("invite not found")
	ErrInviteRevoked   = errors.New("invite revoked")
	ErrInviteExhausted = errors.New("invite exhausted")
	ErrTenantNotFound  = errors.New("tenant config not found")
)

// Invite 는 관리자가 발급한 org 초대다 (spec §3.2).
type Invite struct {
	Code      string
	TenantID  string
	MaxUses   int
	UsedCount int
	Revoked   bool
}

// Installation 은 enroll 로 생성된 설치 단위다.
type Installation struct {
	ID       string
	TenantID string
	DeviceID string
}

// Memory 는 스레드 안전한 인메모리 저장소다. 인터페이스는 최소로 두어 Postgres 로 교체하기 쉽게 한다.
type Memory struct {
	mu            sync.Mutex
	invites       map[string]*Invite
	tenantConfigs map[string]contract.Manifest
	installations map[string]*Installation
	tokenHashes   map[string]string // tokenHash -> installationID (평문 토큰은 저장 안 함, §6)
}

func NewMemory() *Memory {
	return &Memory{
		invites:       map[string]*Invite{},
		tenantConfigs: map[string]contract.Manifest{},
		installations: map[string]*Installation{},
		tokenHashes:   map[string]string{},
	}
}

// Seed 는 로컬 개발용 데모 데이터를 넣는다: 초대 코드 TEST-1234 → tenant "acme".
func (m *Memory) Seed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tenantConfigs["acme"] = contract.Manifest{
		SchemaVersion:      1,
		ConfigRevision:     1,
		OTLP:               contract.OTLP{Endpoint: "https://telemetry.acme.example.com", Protocol: "http/protobuf"},
		Signals:            contract.Signals{Logs: true, Metrics: true, Traces: false},
		Privacy:            contract.Privacy{}, // 전부 false (§4.6)
		ResourceAttributes: map[string]string{"deployment.environment": "production"},
	}
	m.invites["TEST-1234"] = &Invite{Code: "TEST-1234", TenantID: "acme", MaxUses: 100}
}

// ClaimInvite 는 초대를 원자적으로 검증하고 사용횟수를 늘린 뒤 tenantID 를 반환한다.
func (m *Memory) ClaimInvite(code string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inv, ok := m.invites[code]
	if !ok {
		return "", ErrInviteNotFound
	}
	if inv.Revoked {
		return "", ErrInviteRevoked
	}
	if inv.UsedCount >= inv.MaxUses {
		return "", ErrInviteExhausted
	}
	inv.UsedCount++
	return inv.TenantID, nil
}

// TenantConfig 는 tenant 의 설정 manifest 를 반환한다(값 복사).
func (m *Memory) TenantConfig(tenantID string) (contract.Manifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, ok := m.tenantConfigs[tenantID]
	if !ok {
		return contract.Manifest{}, ErrTenantNotFound
	}
	return cfg, nil
}

// AddInstallation 은 새 설치를 저장한다.
func (m *Memory) AddInstallation(inst Installation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.installations[inst.ID] = &inst
}

// AddTokenHash 는 토큰 해시 → 설치 매핑을 저장한다(토큰 검증용). 평문 토큰은 저장하지 않는다 (§6).
func (m *Memory) AddTokenHash(hash, installationID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokenHashes[hash] = installationID
}
