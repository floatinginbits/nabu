package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/user"
)

// Service orchestrates login, refresh, and logout over the user domain and the
// refresh-token repository. It holds the signing secret and issues access
// tokens; it never touches the database on the access-token verification path.
type Service struct {
	users   UserAuthenticator
	refresh RefreshRepository
	signing []byte
	log     *slog.Logger
	// now is the clock, injectable so token lifetimes are testable.
	now func() time.Time
}

// NewService wires the auth service. signingSecret is the HS256 key; callers
// validate its length at config load (ADR-0003 requires >= 32 bytes).
func NewService(users UserAuthenticator, refresh RefreshRepository, signingSecret []byte, log *slog.Logger) *Service {
	return &Service{
		users:   users,
		refresh: refresh,
		signing: signingSecret,
		log:     log,
		now:     time.Now,
	}
}

// Login verifies credentials and starts a new session (a fresh token family).
// It returns the authenticated user so the caller can render a profile without
// a second lookup; the caller is responsible for never serializing the user's
// password hash.
func (s *Service) Login(ctx context.Context, email, password string) (TokenPair, user.User, error) {
	u, err := s.users.Authenticate(ctx, email, password)
	if errors.Is(err, user.ErrInvalidCredentials) {
		return TokenPair{}, user.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return TokenPair{}, user.User{}, fmt.Errorf("authenticating: %w", err)
	}

	now := s.now()
	plaintext, hash, err := generateRefreshToken()
	if err != nil {
		return TokenPair{}, user.User{}, err
	}
	refreshExpiry := now.Add(refreshTTL)
	if _, err := s.refresh.Create(ctx, uuid.New(), u.ID, hash, refreshExpiry); err != nil {
		return TokenPair{}, user.User{}, fmt.Errorf("storing refresh token: %w", err)
	}
	pair, err := s.pairFor(u.ID, now, plaintext, refreshExpiry)
	if err != nil {
		return TokenPair{}, user.User{}, err
	}
	return pair, u, nil
}

// Refresh rotates a refresh token: it returns a new pair, detects reuse of an
// already-rotated token (revoking the family), and tolerates the concurrent
// two-tab refresh via the repository's grace window.
func (s *Service) Refresh(ctx context.Context, refreshPlaintext string) (TokenPair, error) {
	now := s.now()
	newPlaintext, newHash, err := generateRefreshToken()
	if err != nil {
		return TokenPair{}, err
	}
	refreshExpiry := now.Add(refreshTTL)

	row, outcome, err := s.refresh.Rotate(ctx, hashToken(refreshPlaintext), newHash, refreshExpiry, graceWindow, now)
	if err != nil {
		return TokenPair{}, fmt.Errorf("rotating refresh token: %w", err)
	}
	switch outcome {
	case RotateOK:
		return s.pairFor(row.UserID, now, newPlaintext, refreshExpiry)
	case RotateReuse:
		// The whole family is now revoked; this is the strongest signal we get
		// that a refresh token leaked, so it is worth a warning.
		s.log.WarnContext(ctx, "refresh token reuse detected; family revoked",
			slog.String("user_id", row.UserID.String()),
			slog.String("family_id", row.FamilyID.String()),
		)
		return TokenPair{}, ErrInvalidToken
	default:
		return TokenPair{}, ErrInvalidToken
	}
}

// Logout revokes the family the presented refresh token belongs to. It is
// best-effort and idempotent: an unknown or already-revoked token is not an
// error, so the client can always clear its cookies.
func (s *Service) Logout(ctx context.Context, refreshPlaintext string) error {
	if refreshPlaintext == "" {
		return nil
	}
	if err := s.refresh.RevokeFamilyByHash(ctx, hashToken(refreshPlaintext)); err != nil {
		return fmt.Errorf("revoking session: %w", err)
	}
	return nil
}

func (s *Service) pairFor(userID uuid.UUID, now time.Time, refreshPlaintext string, refreshExpiry time.Time) (TokenPair, error) {
	access, accessExpiry, err := s.issueAccess(userID, now)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		Access:        access,
		AccessExpiry:  accessExpiry,
		Refresh:       refreshPlaintext,
		RefreshExpiry: refreshExpiry,
	}, nil
}
