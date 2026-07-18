package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/audit"
	"github.com/floatinginbits/nabu/internal/user"
)

// Service orchestrates login, refresh, and logout over the user domain and the
// refresh-token repository. It holds the signing secret and issues access
// tokens; it never touches the database on the access-token verification path.
type Service struct {
	users   UserAuthenticator
	refresh RefreshRepository
	audit   audit.Recorder
	signing []byte
	// orgID scopes the audit rows this service writes. Login and logout run
	// unauthenticated, so there is no actor in the context to read it from; it
	// comes from the same server-side resolution that stamps every session's
	// org in requireAuth, never from the request (ADR-0005).
	orgID uuid.UUID
	log   *slog.Logger
	// now is the clock, injectable so token lifetimes are testable.
	now func() time.Time
}

// NewService wires the auth service. signingSecret is the HS256 key; callers
// validate its length at config load (ADR-0003 requires >= 32 bytes).
func NewService(users UserAuthenticator, refresh RefreshRepository, recorder audit.Recorder, signingSecret []byte, orgID uuid.UUID, log *slog.Logger) *Service {
	return &Service{
		users:   users,
		refresh: refresh,
		audit:   recorder,
		signing: signingSecret,
		orgID:   orgID,
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
		// Deliberately not audited: /auth/login is reachable unauthenticated and
		// nothing rate-limits it, so recording failures would hand an anonymous
		// caller an unbounded append into a table with no retention (ADR-0004).
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

	s.audit.Record(ctx, audit.Entry{
		ActorID:    uuid.NullUUID{UUID: u.ID, Valid: true},
		OrgID:      s.orgID,
		Action:     "auth.login",
		EntityType: "user",
		EntityID:   u.ID,
		Metadata:   loginAuditMetadata(u.Email),
	})
	return pair, u, nil
}

// loginAuditMetadata is the auth domain's allowlist. Only the address of the
// user who just authenticated is recorded — never the submitted address, never
// the password, never a token or its hash (ADR-0004).
func loginAuditMetadata(email string) map[string]any {
	return map[string]any{"email": email}
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
	userID, err := s.refresh.RevokeFamilyByHash(ctx, hashToken(refreshPlaintext))
	if err != nil {
		return fmt.Errorf("revoking session: %w", err)
	}
	if !userID.Valid {
		// Nothing was revoked — an unknown or already-revoked token. Recording
		// it would put a row with no actor in the trail for every stale cookie
		// a browser presents.
		return nil
	}
	s.audit.Record(ctx, audit.Entry{
		ActorID:    userID,
		OrgID:      s.orgID,
		Action:     "auth.logout",
		EntityType: "user",
		EntityID:   userID.UUID,
	})
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
