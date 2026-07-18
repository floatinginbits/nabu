// Package project is the project domain: projects group tasks inside an
// organization. Every read is org-scoped — the org comes from the session
// actor, never from the request (security-baseline.md).
package project

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("project not found")

type Project struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Key       string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Repository is the project data-access boundary. The service depends on this
// interface so it is testable without a database.
//
// There is no Create: creating a project is admin-only, and RBAC does not
// exist yet.
type Repository interface {
	List(ctx context.Context, orgID uuid.UUID) ([]Project, error)
	GetByID(ctx context.Context, id, orgID uuid.UUID) (Project, error)
}
