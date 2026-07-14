// Package task is the task domain: domain types, the repository boundary,
// and the service holding business logic. Persistence details (sqlc-generated
// code) stay inside the repository implementation.
package task

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
)

func (s Status) Valid() bool {
	switch s {
	case StatusTodo, StatusInProgress, StatusDone:
		return true
	}
	return false
}

type Task struct {
	ID        uuid.UUID
	Title     string
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Cursor identifies a position in the (created_at DESC, id DESC) scan order
// that list pagination pages through.
type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

// ListFilter is what the repository needs to produce one page; translating
// API-level inputs (opaque cursor string, page-size defaults) into it is the
// service's job.
type ListFilter struct {
	Status *Status
	After  *Cursor
	Limit  int
}

// Repository is the task data-access boundary. The service depends on this
// interface so it is testable without a database.
type Repository interface {
	Create(ctx context.Context, title string) (Task, error)
	List(ctx context.Context, f ListFilter) ([]Task, error)
}
