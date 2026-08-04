package enrollment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/your-org/pulsemetry/internal/contract"
)

type PostgresRepository struct {
	db     *sql.DB
	pepper []byte
}

func (r *PostgresRepository) CreateEnrollment(ctx context.Context, params CreateEnrollmentParams) (_ *EnrollmentRecord, retErr error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()

	var (
		invitationID string
		tenantID     string
		memberID     string
		usedAt       *time.Time
		expiresAt    time.Time
		revokedAt    *time.Time
	)
	err = tx.QueryRowContext(ctx,
		`SELECT id, tenant_id, target_member_id, used_at, expires_at, revoked_at
		 FROM enrollment.invitations
		 WHERE code_hash = $1
		 FOR UPDATE`,
		hashCode(r.pepper, params.Code),
	).Scan(&invitationID, &tenantID, &memberID, &usedAt, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCodeNotFound
	}
	if err != nil {
		return nil, err
	}
	if revokedAt != nil {
		return nil, ErrCodeRevoked
	}
	if usedAt != nil {
		return nil, ErrCodeExhausted
	}
	if !expiresAt.After(params.Now) {
		return nil, ErrCodeExpired
	}

	var manifestID string
	var manifestJSON []byte
	err = tx.QueryRowContext(ctx,
		`SELECT id, manifest
		 FROM enrollment.manifests
		 WHERE tenant_id = $1 AND is_active = true
		 ORDER BY version DESC
		 LIMIT 1`,
		tenantID,
	).Scan(&manifestID, &manifestJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrManifestNotFound
	}
	if err != nil {
		return nil, err
	}
	var manifest contract.Manifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, fmt.Errorf("decode active manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate active manifest: %w", err)
	}

	var installationID string
	err = tx.QueryRowContext(ctx,
		`INSERT INTO enrollment.installations
		   (tenant_id, member_id, invitation_id, hostname, platform, architecture, client_version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		tenantID, memberID, invitationID, nullableString(params.Hostname), params.Platform,
		nullableString(params.Architecture), nullableString(params.ClientVersion),
	).Scan(&installationID)
	if err != nil {
		return nil, err
	}

	if _, err = tx.ExecContext(ctx,
		`INSERT INTO enrollment.installation_credentials (installation_id, credential_hash, issued_at)
		 VALUES ($1, $2, $3)`,
		installationID, params.CredentialHash, params.Now,
	); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO enrollment.installation_manifest_assignments (installation_id, manifest_id, assigned_at)
		 VALUES ($1, $2, $3)`,
		installationID, manifestID, params.Now,
	); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE enrollment.invitations SET used_at = $1 WHERE id = $2`,
		params.Now, invitationID,
	); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &EnrollmentRecord{InstallationID: installationID, Manifest: manifest}, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func NewPostgresRepository(db *sql.DB, pepper string) *PostgresRepository {
	return &PostgresRepository{db: db, pepper: []byte(pepper)}
}

func (r *PostgresRepository) FindInvitationByCode(ctx context.Context, code string) (*Invitation, error) {
	var invitation Invitation
	err := r.db.QueryRowContext(ctx,
		`SELECT used_at, expires_at, revoked_at
		 FROM enrollment.invitations
		 WHERE code_hash = $1`,
		hashCode(r.pepper, code),
	).Scan(&invitation.UsedAt, &invitation.ExpiresAt, &invitation.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCodeNotFound
	}
	if err != nil {
		return nil, err
	}
	return &invitation, nil
}

func hashCode(pepper []byte, code string) string {
	h := hmac.New(sha256.New, pepper)
	_, _ = h.Write([]byte(code))
	return hex.EncodeToString(h.Sum(nil))
}
