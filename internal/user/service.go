package user

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	maxEmailLen       = 254
	maxDisplayNameLen = 200
	minPasswordLen    = 8
	// bcrypt ignores bytes past 72; reject rather than silently truncate.
	maxPasswordBytes = 72

	bcryptCost = 12
)

// dummyHash is compared against when the email is unknown, so Authenticate
// takes roughly the same time whether or not the account exists.
const dummyHash = "$2a$12$p3T1VS.dyMV2H6TVrwmrjevU4cfO8.zYthO1p3P9qI9juQoz76W3m"

// ValidationError reports invalid caller input; the HTTP layer translates it
// to a 422 with code VALIDATION_ERROR.
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return e.Msg }

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, email, displayName, password string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if err := validateEmail(email); err != nil {
		return User{}, err
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return User{}, &ValidationError{Msg: "display name is required"}
	}
	if utf8.RuneCountInString(displayName) > maxDisplayNameLen {
		return User{}, &ValidationError{Msg: fmt.Sprintf("display name exceeds %d characters", maxDisplayNameLen)}
	}
	if err := validatePassword(password); err != nil {
		return User{}, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return User{}, fmt.Errorf("hashing password: %w", err)
	}
	u, err := s.repo.Create(ctx, email, displayName, string(hash))
	if err != nil {
		return User{}, fmt.Errorf("creating user: %w", err)
	}
	return u, nil
}

func (s *Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	u, err := s.repo.GetByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("looking up user: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return User{}, ErrInvalidCredentials
	}
	return u, nil
}

func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (User, error) {
	u, err := s.repo.GetByID(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return User{}, fmt.Errorf("getting user: %w", err)
	}
	return u, err
}

// EnsureInitialAdmin creates the first account from deploy config when the
// users table is empty. On an instance that already has users it does
// nothing, so the env vars can stay set across restarts.
//
// "Admin" is aspirational until the rbac domain exists: this creates a plain
// user row, and the rbac migration must backfill an org-level admin role for
// instances seeded before it (tracked in TASKS.md, M2 RBAC slice).
func (s *Service) EnsureInitialAdmin(ctx context.Context, email, password string) (bool, error) {
	n, err := s.repo.Count(ctx)
	if err != nil {
		return false, fmt.Errorf("counting users: %w", err)
	}
	if n > 0 {
		return false, nil
	}
	if _, err := s.Create(ctx, email, "Admin", password); err != nil {
		// A concurrently starting replica may win the race between Count and
		// Create; the unique index turns that into ErrEmailTaken, which means
		// bootstrap already happened — not a startup failure.
		if errors.Is(err, ErrEmailTaken) {
			return false, nil
		}
		return false, fmt.Errorf("creating initial admin: %w", err)
	}
	return true, nil
}

func validateEmail(email string) error {
	if email == "" {
		return &ValidationError{Msg: "email is required"}
	}
	if len(email) > maxEmailLen {
		return &ValidationError{Msg: fmt.Sprintf("email exceeds %d characters", maxEmailLen)}
	}
	// ParseAddress also accepts "Name <a@b>" forms; requiring the parsed
	// address to round-trip to the input rejects those.
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return &ValidationError{Msg: "invalid email address"}
	}
	return nil
}

func validatePassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordLen {
		return &ValidationError{Msg: fmt.Sprintf("password must be at least %d characters", minPasswordLen)}
	}
	if len(password) > maxPasswordBytes {
		return &ValidationError{Msg: fmt.Sprintf("password exceeds %d bytes", maxPasswordBytes)}
	}
	return nil
}
