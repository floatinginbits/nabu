// Package auth issues and validates Nabu's session credentials: a short-lived
// HS256 access token verified statelessly on every request, and an opaque,
// server-stored refresh token that supports rotation, reuse detection, and
// revocation. The design and its rationale are recorded in ADR-0003.
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/user"
)

var (
	// ErrInvalidCredentials is a failed login (wrong email or password),
	// undifferentiated so callers cannot probe which accounts exist.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrInvalidToken covers every rejected access or refresh token: absent,
	// malformed, expired, revoked, or reused.
	ErrInvalidToken = errors.New("invalid or expired token")
)

// TokenPair is the result of a login or refresh; the HTTP layer turns each
// half into its cookie (ADR-0003).
type TokenPair struct {
	Access        string
	AccessExpiry  time.Time
	Refresh       string
	RefreshExpiry time.Time
}

// RefreshToken is a stored refresh-token row mapped out of the repository.
// The token secret itself is never held here — only its hash lives in the DB.
type RefreshToken struct {
	ID         uuid.UUID
	FamilyID   uuid.UUID
	UserID     uuid.UUID
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	ReplacedAt *time.Time
}

// RotateOutcome reports how the repository resolved a rotation attempt.
type RotateOutcome int

const (
	// RotateInvalid: the presented token was not found, expired, or revoked.
	RotateInvalid RotateOutcome = iota
	// RotateOK: a successor was minted (the happy path, or a sibling issued
	// for a concurrent refresh within the grace window).
	RotateOK
	// RotateReuse: an already-rotated token was presented outside the grace
	// window — the stolen-token signal; the whole family was revoked.
	RotateReuse
)

// UserAuthenticator is the slice of the user domain that login depends on.
type UserAuthenticator interface {
	Authenticate(ctx context.Context, email, password string) (user.User, error)
}

// RefreshRepository is the refresh-token data-access boundary. The atomic
// rotation state machine lives behind Rotate so its transaction and row lock
// stay inside the repository.
type RefreshRepository interface {
	// Create stores a fresh token — a new family, minted at login.
	Create(ctx context.Context, familyID, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) (RefreshToken, error)

	// Rotate atomically consumes presentedHash and mints the successor
	// described by newHash/newExpiry within the same family. graceWindow
	// governs the concurrent-refresh race and now is the transaction clock
	// (both from the caller so behavior is testable). When err is nil: on
	// RotateOK it returns the new row; on RotateReuse the presented row (for
	// logging); on RotateInvalid a zero token. When err is non-nil the
	// returned token and outcome carry no meaning.
	Rotate(ctx context.Context, presentedHash, newHash []byte, newExpiry time.Time, graceWindow time.Duration, now time.Time) (RefreshToken, RotateOutcome, error)

	// RevokeFamilyByHash revokes every live token in the family the presented
	// token belongs to; a missing hash is a no-op, so logout is idempotent.
	RevokeFamilyByHash(ctx context.Context, tokenHash []byte) error
}
