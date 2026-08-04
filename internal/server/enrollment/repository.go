package enrollment

import "context"

// Repository provides persistence required by enrollment.
type Repository interface {
	FindInvitationByCode(ctx context.Context, code string) (*Invitation, error)
	CreateEnrollment(ctx context.Context, params CreateEnrollmentParams) (*EnrollmentRecord, error)
}
