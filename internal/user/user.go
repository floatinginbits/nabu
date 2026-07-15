// Package user is the user domain: account records, credential hashing and
// verification, and the repository boundary. Role assignments are the rbac
// domain's concern — a user row carries no role of its own.
package user

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound   = errors.New("user not found")
	ErrEmailTaken = errors.New("email already registered")
	// ErrInvalidCredentials covers both unknown email and wrong password, so
	// callers cannot tell which accounts exist.
	ErrInvalidCredentials = errors.New("invalid credentials")
)

type User struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	// PasswordHash is the bcrypt hash. It never leaves the backend; keep it
	// out of any DTO mapping.
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Repository is the user data-access boundary. The service depends on this
// interface so it is testable without a database.
type Repository interface {
	Create(ctx context.Context, email, displayName, passwordHash string) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
	GetByID(ctx context.Context, id uuid.UUID) (User, error)
	Count(ctx context.Context) (int64, error)
}
