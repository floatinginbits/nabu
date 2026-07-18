package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/audit"
	"github.com/floatinginbits/nabu/internal/audit/audittest"
	"github.com/floatinginbits/nabu/internal/user"
)

var testSecret = []byte("test-signing-secret-at-least-32-bytes")

// testNow anchors the injected service clock. It is real time rather than a
// fixed date on purpose: VerifyAccessToken validates expiry against the wall
// clock (jwt/v5 gets no clock from us), so tokens issued at a far-off base
// would never verify. Lifetimes are still asserted by arithmetic off this
// base, never by sleeping.
var testNow = time.Now().UTC().Truncate(time.Second)

// errDBDown stands in for any non-credential repository failure.
var errDBDown = errors.New("connection refused")

type createCall struct {
	familyID  uuid.UUID
	userID    uuid.UUID
	tokenHash []byte
	expiresAt time.Time
}

type rotateCall struct {
	presentedHash []byte
	newHash       []byte
	newExpiry     time.Time
	graceWindow   time.Duration
	now           time.Time
}

// fakeUsers is the UserAuthenticator slice of the user domain.
type fakeUsers struct {
	user  user.User
	err   error
	calls int
}

func (f *fakeUsers) Authenticate(_ context.Context, _, _ string) (user.User, error) {
	f.calls++
	if f.err != nil {
		return user.User{}, f.err
	}
	return f.user, nil
}

// fakeRefresh records what reaches the repository and replays a scripted
// rotation outcome. The real rotation state machine is a database concern and
// is covered against Postgres in postgres_test.go.
type fakeRefresh struct {
	createErr error
	creates   []createCall

	rotateRow     RefreshToken
	rotateOutcome RotateOutcome
	rotateErr     error
	rotates       []rotateCall

	revokeErr    error
	revokeUserID uuid.NullUUID
	revokeHashes [][]byte
}

func (f *fakeRefresh) Create(_ context.Context, familyID, userID uuid.UUID, tokenHash []byte, expiresAt time.Time) (RefreshToken, error) {
	f.creates = append(f.creates, createCall{familyID: familyID, userID: userID, tokenHash: tokenHash, expiresAt: expiresAt})
	if f.createErr != nil {
		return RefreshToken{}, f.createErr
	}
	return RefreshToken{ID: uuid.New(), FamilyID: familyID, UserID: userID, ExpiresAt: expiresAt}, nil
}

func (f *fakeRefresh) Rotate(_ context.Context, presentedHash, newHash []byte, newExpiry time.Time, grace time.Duration, now time.Time) (RefreshToken, RotateOutcome, error) {
	f.rotates = append(f.rotates, rotateCall{
		presentedHash: presentedHash,
		newHash:       newHash,
		newExpiry:     newExpiry,
		graceWindow:   grace,
		now:           now,
	})
	if f.rotateErr != nil {
		return RefreshToken{}, RotateInvalid, f.rotateErr
	}
	return f.rotateRow, f.rotateOutcome, nil
}

func (f *fakeRefresh) RevokeFamilyByHash(_ context.Context, tokenHash []byte) (uuid.NullUUID, error) {
	f.revokeHashes = append(f.revokeHashes, tokenHash)
	if f.revokeErr != nil {
		return uuid.NullUUID{}, f.revokeErr
	}
	return f.revokeUserID, nil
}

// testOrgID stands in for the org the deployment resolves at startup; auth runs
// unauthenticated, so it comes from wiring rather than from a session actor.
var testOrgID = uuid.New()

// newTestService wires a Service on the fakes with the clock pinned to testNow.
// Its audit recorder is checked against the secret denylist when the test ends
// (ADR-0004), so every entry any case in this package produces is covered.
func newTestService(t *testing.T, users *fakeUsers, refresh *fakeRefresh) *Service {
	t.Helper()
	return newTestServiceWithAudit(t, users, refresh, audittest.New(t))
}

func newTestServiceWithAudit(t *testing.T, users *fakeUsers, refresh *fakeRefresh, recorder *audittest.Recorder) *Service {
	t.Helper()
	s := NewService(users, refresh, recorder, testSecret, testOrgID, slog.New(slog.DiscardHandler))
	s.now = func() time.Time { return testNow }
	return s
}

func outcomeName(o RotateOutcome) string {
	switch o {
	case RotateInvalid:
		return "RotateInvalid"
	case RotateOK:
		return "RotateOK"
	case RotateReuse:
		return "RotateReuse"
	default:
		return fmt.Sprintf("RotateOutcome(%d)", int(o))
	}
}

func TestServiceLogin(t *testing.T) {
	ctx := context.Background()
	seeded := user.User{
		ID:           uuid.New(),
		Email:        "alice@example.com",
		DisplayName:  "Alice",
		PasswordHash: "$2a$12$not-a-real-hash",
	}

	t.Run("issues a pair and stores only the refresh hash", func(t *testing.T) {
		repo := &fakeRefresh{}
		svc := newTestService(t, &fakeUsers{user: seeded}, repo)

		pair, got, err := svc.Login(ctx, "alice@example.com", "open sesame 123")
		if err != nil {
			t.Fatalf("Login() error: %v", err)
		}
		if got.ID != seeded.ID || got.Email != seeded.Email {
			t.Errorf("Login() user = %+v, want %+v", got, seeded)
		}

		subject, err := svc.VerifyAccessToken(pair.Access)
		if err != nil {
			t.Fatalf("VerifyAccessToken() on a freshly issued token: %v", err)
		}
		if subject != seeded.ID {
			t.Errorf("access token subject = %v, want %v", subject, seeded.ID)
		}
		if !pair.AccessExpiry.Equal(testNow.Add(accessTTL)) {
			t.Errorf("AccessExpiry = %v, want %v", pair.AccessExpiry, testNow.Add(accessTTL))
		}
		if !pair.RefreshExpiry.Equal(testNow.Add(refreshTTL)) {
			t.Errorf("RefreshExpiry = %v, want %v", pair.RefreshExpiry, testNow.Add(refreshTTL))
		}
		if pair.Refresh == "" {
			t.Fatal("Login() returned an empty refresh token")
		}

		if len(repo.creates) != 1 {
			t.Fatalf("repo.Create called %d times, want 1", len(repo.creates))
		}
		c := repo.creates[0]
		if !bytes.Equal(c.tokenHash, hashToken(pair.Refresh)) {
			t.Errorf("stored hash = %x, want the SHA-256 of the issued token %x", c.tokenHash, hashToken(pair.Refresh))
		}
		if bytes.Contains(c.tokenHash, []byte(pair.Refresh)) {
			t.Error("the refresh token plaintext reached the repository; only its hash may be stored")
		}
		if c.userID != seeded.ID {
			t.Errorf("stored user_id = %v, want %v", c.userID, seeded.ID)
		}
		if c.familyID == uuid.Nil {
			t.Error("stored family_id is the zero UUID, want a fresh family per login")
		}
		if !c.expiresAt.Equal(pair.RefreshExpiry) {
			t.Errorf("stored expires_at = %v, want the returned RefreshExpiry %v", c.expiresAt, pair.RefreshExpiry)
		}
	})

	t.Run("each login starts its own family", func(t *testing.T) {
		repo := &fakeRefresh{}
		svc := newTestService(t, &fakeUsers{user: seeded}, repo)

		first, _, err := svc.Login(ctx, "alice@example.com", "open sesame 123")
		if err != nil {
			t.Fatalf("first Login() error: %v", err)
		}
		second, _, err := svc.Login(ctx, "alice@example.com", "open sesame 123")
		if err != nil {
			t.Fatalf("second Login() error: %v", err)
		}
		if repo.creates[0].familyID == repo.creates[1].familyID {
			t.Error("both logins share a family_id; a second login must not join the first session's family")
		}
		if first.Refresh == second.Refresh {
			t.Error("both logins returned the same refresh token")
		}
	})

	t.Run("errors", func(t *testing.T) {
		const email = "alice@example.com"
		const password = "open sesame 123"

		tests := []struct {
			name string
			// The credential path must collapse to the bare sentinel; other
			// failures wrap their cause.
			users       *fakeUsers
			repo        *fakeRefresh
			wantIs      error
			wantExact   bool // err == ErrInvalidCredentials, no added context
			wantCreates int
		}{
			{
				name:      "unknown email",
				users:     &fakeUsers{err: user.ErrInvalidCredentials},
				repo:      &fakeRefresh{},
				wantIs:    ErrInvalidCredentials,
				wantExact: true,
			},
			{
				name:      "wrong password",
				users:     &fakeUsers{err: user.ErrInvalidCredentials},
				repo:      &fakeRefresh{},
				wantIs:    ErrInvalidCredentials,
				wantExact: true,
			},
			{
				name:      "wrapped invalid credentials",
				users:     &fakeUsers{err: fmt.Errorf("looking up user: %w", user.ErrInvalidCredentials)},
				repo:      &fakeRefresh{},
				wantIs:    ErrInvalidCredentials,
				wantExact: true,
			},
			{
				name:   "authenticator failure",
				users:  &fakeUsers{err: errDBDown},
				repo:   &fakeRefresh{},
				wantIs: errDBDown,
			},
			{
				name:        "refresh store failure",
				users:       &fakeUsers{user: seeded},
				repo:        &fakeRefresh{createErr: errDBDown},
				wantIs:      errDBDown,
				wantCreates: 1,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				svc := newTestService(t, tt.users, tt.repo)

				pair, got, err := svc.Login(ctx, email, password)
				if !errors.Is(err, tt.wantIs) {
					t.Fatalf("Login() error = %v, want errors.Is(_, %v)", err, tt.wantIs)
				}
				if tt.wantExact {
					// Deliberately an identity check, not errors.Is: any wrapping
					// here would be context about why the login failed.
					if err != ErrInvalidCredentials {
						t.Errorf("Login() error = %#v, want the bare ErrInvalidCredentials with no added context", err)
					}
					if errors.Is(err, errDBDown) {
						t.Error("Login() leaked the underlying cause on a credential failure")
					}
				}
				if pair != (TokenPair{}) {
					t.Errorf("Login() returned a non-zero pair on error: %+v", pair)
				}
				if got != (user.User{}) {
					t.Errorf("Login() returned a user on error: %+v", got)
				}
				// A failed login must not reveal whether the account exists.
				if strings.Contains(err.Error(), email) || strings.Contains(err.Error(), password) {
					t.Errorf("Login() error %q echoes the submitted credentials", err)
				}
				if len(tt.repo.creates) != tt.wantCreates {
					t.Errorf("repo.Create called %d times, want %d", len(tt.repo.creates), tt.wantCreates)
				}
			})
		}
	})
}

func TestServiceRefresh(t *testing.T) {
	ctx := context.Background()
	const presented = "presented-refresh-token"
	userID := uuid.New()
	familyID := uuid.New()

	t.Run("rotates to a new pair", func(t *testing.T) {
		repo := &fakeRefresh{
			rotateOutcome: RotateOK,
			rotateRow: RefreshToken{
				ID:        uuid.New(),
				FamilyID:  familyID,
				UserID:    userID,
				ExpiresAt: testNow.Add(refreshTTL),
			},
		}
		svc := newTestService(t, &fakeUsers{}, repo)

		pair, err := svc.Refresh(ctx, presented)
		if err != nil {
			t.Fatalf("Refresh() error: %v", err)
		}

		subject, err := svc.VerifyAccessToken(pair.Access)
		if err != nil {
			t.Fatalf("VerifyAccessToken() on the refreshed token: %v", err)
		}
		if subject != userID {
			t.Errorf("access token subject = %v, want the row's user %v", subject, userID)
		}
		if pair.Refresh == presented {
			t.Error("Refresh() returned the presented refresh token; rotation must issue a new one")
		}
		if !pair.AccessExpiry.Equal(testNow.Add(accessTTL)) {
			t.Errorf("AccessExpiry = %v, want %v", pair.AccessExpiry, testNow.Add(accessTTL))
		}
		if !pair.RefreshExpiry.Equal(testNow.Add(refreshTTL)) {
			t.Errorf("RefreshExpiry = %v, want a fresh sliding expiry %v", pair.RefreshExpiry, testNow.Add(refreshTTL))
		}

		if len(repo.rotates) != 1 {
			t.Fatalf("repo.Rotate called %d times, want 1", len(repo.rotates))
		}
		r := repo.rotates[0]
		if !bytes.Equal(r.presentedHash, hashToken(presented)) {
			t.Errorf("presented hash = %x, want the SHA-256 of the presented token %x", r.presentedHash, hashToken(presented))
		}
		if bytes.Contains(r.presentedHash, []byte(presented)) {
			t.Error("the presented plaintext reached the repository, want only its hash")
		}
		if !bytes.Equal(r.newHash, hashToken(pair.Refresh)) {
			t.Errorf("new hash = %x, want the SHA-256 of the returned token %x", r.newHash, hashToken(pair.Refresh))
		}
		if r.graceWindow != graceWindow {
			t.Errorf("graceWindow = %v, want %v", r.graceWindow, graceWindow)
		}
		if !r.now.Equal(testNow) {
			t.Errorf("rotation clock = %v, want the service clock %v", r.now, testNow)
		}
		if !r.newExpiry.Equal(testNow.Add(refreshTTL)) {
			t.Errorf("new expiry = %v, want %v", r.newExpiry, testNow.Add(refreshTTL))
		}
	})

	t.Run("rejections", func(t *testing.T) {
		row := RefreshToken{ID: uuid.New(), FamilyID: familyID, UserID: userID, ExpiresAt: testNow.Add(refreshTTL)}

		tests := []struct {
			name    string
			repo    *fakeRefresh
			token   string
			wantIs  error
			wantErr bool // an infrastructure failure wraps rather than mapping to ErrInvalidToken
		}{
			{
				name:   "reuse of an already-rotated token",
				repo:   &fakeRefresh{rotateOutcome: RotateReuse, rotateRow: row},
				token:  presented,
				wantIs: ErrInvalidToken,
			},
			{
				name:   "unknown, expired, or revoked token",
				repo:   &fakeRefresh{rotateOutcome: RotateInvalid},
				token:  presented,
				wantIs: ErrInvalidToken,
			},
			{
				name:   "empty token",
				repo:   &fakeRefresh{rotateOutcome: RotateInvalid},
				token:  "",
				wantIs: ErrInvalidToken,
			},
			{
				name:    "repository failure",
				repo:    &fakeRefresh{rotateErr: errDBDown},
				token:   presented,
				wantIs:  errDBDown,
				wantErr: true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				svc := newTestService(t, &fakeUsers{}, tt.repo)

				pair, err := svc.Refresh(ctx, tt.token)
				if !errors.Is(err, tt.wantIs) {
					t.Fatalf("Refresh() error = %v, want errors.Is(_, %v)", err, tt.wantIs)
				}
				if !tt.wantErr && errors.Is(err, errDBDown) {
					t.Error("Refresh() leaked an infrastructure cause to the caller")
				}
				if tt.wantErr && errors.Is(err, ErrInvalidToken) {
					t.Error("Refresh() reported a repository failure as an invalid token")
				}
				if pair != (TokenPair{}) {
					t.Errorf("Refresh() returned a non-zero pair on rejection: %+v", pair)
				}
			})
		}
	})
}

func TestServiceLogout(t *testing.T) {
	ctx := context.Background()

	t.Run("empty token is a no-op", func(t *testing.T) {
		repo := &fakeRefresh{}
		svc := newTestService(t, &fakeUsers{}, repo)

		if err := svc.Logout(ctx, ""); err != nil {
			t.Fatalf("Logout(\"\") error = %v, want nil", err)
		}
		if len(repo.revokeHashes) != 0 {
			t.Errorf("repo.RevokeFamilyByHash called %d times for an absent cookie, want 0", len(repo.revokeHashes))
		}
	})

	t.Run("revokes by hash, never by plaintext", func(t *testing.T) {
		const plaintext = "some-refresh-token"
		repo := &fakeRefresh{}
		svc := newTestService(t, &fakeUsers{}, repo)

		if err := svc.Logout(ctx, plaintext); err != nil {
			t.Fatalf("Logout() error: %v", err)
		}
		if len(repo.revokeHashes) != 1 {
			t.Fatalf("repo.RevokeFamilyByHash called %d times, want 1", len(repo.revokeHashes))
		}
		got := repo.revokeHashes[0]
		if !bytes.Equal(got, hashToken(plaintext)) {
			t.Errorf("revoked by %x, want the SHA-256 of the presented token %x", got, hashToken(plaintext))
		}
		if bytes.Contains(got, []byte(plaintext)) {
			t.Error("the plaintext reached the repository; lookup must be by hash only")
		}
		if len(got) != 32 {
			t.Errorf("hash length = %d bytes, want 32 (SHA-256)", len(got))
		}
	})

	t.Run("repository failure wraps", func(t *testing.T) {
		repo := &fakeRefresh{revokeErr: errDBDown}
		svc := newTestService(t, &fakeUsers{}, repo)

		err := svc.Logout(ctx, "some-refresh-token")
		if !errors.Is(err, errDBDown) {
			t.Fatalf("Logout() error = %v, want errors.Is(_, errDBDown)", err)
		}
	})
}

// Auth events are recorded with an explicitly supplied actor: login and logout
// run unauthenticated, so there is no actor in the context to read one from.
func TestServiceAuthAudit(t *testing.T) {
	ctx := context.Background()
	seeded := user.User{
		ID:           uuid.New(),
		Email:        "alice@example.com",
		DisplayName:  "Alice",
		PasswordHash: "$2a$12$not-a-real-hash",
	}
	const password = "open sesame 123"

	tests := []struct {
		name    string
		call    func(t *testing.T, svc *Service) error
		users   *fakeUsers
		repo    *fakeRefresh
		want    audit.Entry
		wantNil bool // no entry at all
	}{
		{
			name:  "login",
			users: &fakeUsers{user: seeded},
			repo:  &fakeRefresh{},
			call: func(_ *testing.T, svc *Service) error {
				_, _, err := svc.Login(ctx, seeded.Email, password)
				return err
			},
			want: audit.Entry{
				ActorID:    uuid.NullUUID{UUID: seeded.ID, Valid: true},
				OrgID:      testOrgID,
				Action:     "auth.login",
				EntityType: "user",
				EntityID:   seeded.ID,
				Metadata:   map[string]any{"email": seeded.Email},
			},
		},
		{
			// Failed logins are deliberately unaudited until a login rate limit
			// exists: the endpoint is public, so an attacker-driven loop would
			// otherwise append a row per attempt forever (ADR-0004).
			name:    "failed login records nothing",
			users:   &fakeUsers{err: user.ErrInvalidCredentials},
			repo:    &fakeRefresh{},
			wantNil: true,
			call: func(_ *testing.T, svc *Service) error {
				_, _, err := svc.Login(ctx, "mallory@example.com", "wrong")
				return err
			},
		},
		{
			name:  "logout names the revoked session's user",
			users: &fakeUsers{},
			repo:  &fakeRefresh{revokeUserID: uuid.NullUUID{UUID: seeded.ID, Valid: true}},
			call: func(_ *testing.T, svc *Service) error {
				return svc.Logout(ctx, "some-refresh-token")
			},
			want: audit.Entry{
				ActorID:    uuid.NullUUID{UUID: seeded.ID, Valid: true},
				OrgID:      testOrgID,
				Action:     "auth.logout",
				EntityType: "user",
				EntityID:   seeded.ID,
			},
		},
		{
			// Nothing was revoked, so there is no session to report ending —
			// otherwise every stale cookie a browser replays writes an
			// actorless row.
			name:    "logout of an unknown token records nothing",
			users:   &fakeUsers{},
			repo:    &fakeRefresh{},
			call:    func(_ *testing.T, svc *Service) error { return svc.Logout(ctx, "unknown") },
			wantNil: true,
		},
		{
			name:    "a login that fails after authentication records nothing",
			users:   &fakeUsers{user: seeded},
			repo:    &fakeRefresh{createErr: errDBDown},
			wantNil: true,
			call: func(_ *testing.T, svc *Service) error {
				_, _, err := svc.Login(ctx, seeded.Email, password)
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := audittest.New(t)
			svc := newTestServiceWithAudit(t, tt.users, tt.repo, rec)
			// The call's own error is asserted by the tests above; here only
			// the resulting audit entry matters.
			_ = tt.call(t, svc)

			entries := rec.Entries()
			if tt.wantNil {
				if len(entries) != 0 {
					t.Fatalf("recorded %d audit entries, want 0: %+v", len(entries), entries)
				}
				return
			}
			if len(entries) != 1 {
				t.Fatalf("recorded %d audit entries, want 1", len(entries))
			}
			if !reflect.DeepEqual(entries[0], tt.want) {
				t.Errorf("audit entry = %+v, want %+v", entries[0], tt.want)
			}
		})
	}
}
