package task

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/actor"
	"github.com/floatinginbits/nabu/internal/project"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
	maxTitleLen     = 500
)

// ValidationError reports invalid caller input; the HTTP layer translates it
// to a 422 with code VALIDATION_ERROR.
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return e.Msg }

// Projects is the slice of project.Service the task domain needs: resolving a
// project inside the session's org, so a client-supplied projectId is
// validated rather than trusted.
type Projects interface {
	GetByID(ctx context.Context, id uuid.UUID) (project.Project, error)
}

type Service struct {
	repo     Repository
	projects Projects
}

func NewService(repo Repository, projects Projects) *Service {
	return &Service{repo: repo, projects: projects}
}

func (s *Service) Create(ctx context.Context, projectID uuid.UUID, title string) (Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Task{}, &ValidationError{Msg: "title is required"}
	}
	if utf8.RuneCountInString(title) > maxTitleLen {
		return Task{}, &ValidationError{Msg: fmt.Sprintf("title exceeds %d characters", maxTitleLen)}
	}
	if _, err := s.projects.GetByID(ctx, projectID); err != nil {
		if errors.Is(err, project.ErrNotFound) {
			return Task{}, &ValidationError{Msg: "unknown project"}
		}
		return Task{}, fmt.Errorf("resolving project: %w", err)
	}

	t, err := s.repo.Create(ctx, projectID, title)
	if err != nil {
		return Task{}, fmt.Errorf("creating task: %w", err)
	}
	return t, nil
}

type ListParams struct {
	ProjectID *uuid.UUID
	Status    *Status
	Cursor    string
	PageSize  int
}

type ListResult struct {
	Tasks      []Task
	NextCursor string // "" on the last page
}

func (s *Service) List(ctx context.Context, p ListParams) (ListResult, error) {
	a, ok := actor.FromContext(ctx)
	if !ok {
		return ListResult{}, actor.ErrNoActor
	}
	if p.Status != nil && !p.Status.Valid() {
		return ListResult{}, &ValidationError{Msg: fmt.Sprintf("unknown status %q", *p.Status)}
	}
	size := p.PageSize
	if size <= 0 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}

	// Fetch one extra row: its presence means another page exists, without a
	// second COUNT query.
	filter := ListFilter{
		Scope:  Scope{OrgID: a.OrgID, ProjectID: p.ProjectID},
		Status: p.Status,
		Limit:  size + 1,
	}
	if p.Cursor != "" {
		c, err := decodeCursor(p.Cursor)
		if err != nil {
			return ListResult{}, &ValidationError{Msg: "invalid cursor"}
		}
		filter.After = &c
	}

	tasks, err := s.repo.List(ctx, filter)
	if err != nil {
		return ListResult{}, fmt.Errorf("listing tasks: %w", err)
	}

	res := ListResult{Tasks: tasks}
	if len(tasks) > size {
		res.Tasks = tasks[:size]
		last := res.Tasks[size-1]
		res.NextCursor = encodeCursor(Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return res, nil
}

// cursorPayload is the wire shape inside the opaque cursor. Clients never
// construct or decode cursors (api-contract.md), so this can change freely.
type cursorPayload struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

func encodeCursor(c Cursor) string {
	b, _ := json.Marshal(cursorPayload(c))
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("decoding cursor: %w", err)
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return Cursor{}, fmt.Errorf("decoding cursor: %w", err)
	}
	if p.ID == uuid.Nil || p.CreatedAt.IsZero() {
		return Cursor{}, fmt.Errorf("decoding cursor: missing fields")
	}
	return Cursor(p), nil
}
