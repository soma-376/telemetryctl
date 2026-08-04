// Package enrollment contains enrollment code validation logic.
package enrollment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/your-org/pulsemetry/internal/contract"
)

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

// ValidateCode verifies that a code exists and can still be used.
// Validation does not consume the code.
func (s *Service) ValidateCode(ctx context.Context, code string) error {
	invitation, err := s.repository.FindInvitationByCode(ctx, code)
	if err != nil {
		return err
	}
	if invitation.RevokedAt != nil {
		return ErrCodeRevoked
	}
	if invitation.UsedAt != nil {
		return ErrCodeExhausted
	}
	if !invitation.ExpiresAt.After(s.now()) {
		return ErrCodeExpired
	}

	return nil
}

func (s *Service) Enroll(ctx context.Context, command EnrollCommand) (*contract.Enrollment, error) {
	platform, err := normalizePlatform(command.Platform)
	if err != nil {
		return nil, err
	}
	token, err := newInstallationToken()
	if err != nil {
		return nil, fmt.Errorf("generate installation token: %w", err)
	}
	sum := sha256.Sum256([]byte(token))
	record, err := s.repository.CreateEnrollment(ctx, CreateEnrollmentParams{
		Code:           command.Code,
		CredentialHash: hex.EncodeToString(sum[:]),
		Platform:       platform,
		Architecture:   command.Architecture,
		Hostname:       command.Hostname,
		ClientVersion:  command.ClientVersion,
		Now:            s.now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	return &contract.Enrollment{
		InstallationID:    record.InstallationID,
		InstallationToken: token,
		Manifest:          record.Manifest,
	}, nil
}

func normalizePlatform(platform string) (string, error) {
	switch platform {
	case "windows", "linux", "macos":
		return platform, nil
	case "darwin":
		return "macos", nil
	default:
		return "", ErrInvalidPlatform
	}
}

func newInstallationToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "pit_" + base64.RawURLEncoding.EncodeToString(b), nil
}
