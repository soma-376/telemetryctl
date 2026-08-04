package enrollment

import (
	"errors"
	"time"

	"github.com/your-org/pulsemetry/internal/contract"
)

var (
	ErrCodeNotFound     = errors.New("enrollment code not found")
	ErrCodeRevoked      = errors.New("enrollment code revoked")
	ErrCodeExhausted    = errors.New("enrollment code exhausted")
	ErrCodeExpired      = errors.New("enrollment code expired")
	ErrManifestNotFound = errors.New("active manifest not found")
	ErrInvalidPlatform  = errors.New("invalid platform")
)

// Invitation contains the state needed to validate an enrollment code.
type Invitation struct {
	UsedAt    *time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

type EnrollCommand struct {
	Code          string
	Platform      string
	Architecture  string
	Hostname      string
	ClientVersion string
}

type CreateEnrollmentParams struct {
	Code           string
	CredentialHash string
	Platform       string
	Architecture   string
	Hostname       string
	ClientVersion  string
	Now            time.Time
}

type EnrollmentRecord struct {
	InstallationID string
	Manifest       contract.Manifest
}
