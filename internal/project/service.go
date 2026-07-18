package project

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/actor"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// List returns the projects of the session's organization. The org is read
// from the context here rather than accepted as a parameter, so no caller can
// widen the scope by passing a different one.
func (s *Service) List(ctx context.Context) ([]Project, error) {
	a, ok := actor.FromContext(ctx)
	if !ok {
		return nil, actor.ErrNoActor
	}
	ps, err := s.repo.List(ctx, a.OrgID)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	return ps, nil
}

// GetByID resolves a project inside the session's organization. A project in
// another org is indistinguishable from one that does not exist: both are
// ErrNotFound.
func (s *Service) GetByID(ctx context.Context, id uuid.UUID) (Project, error) {
	a, ok := actor.FromContext(ctx)
	if !ok {
		return Project{}, actor.ErrNoActor
	}
	p, err := s.repo.GetByID(ctx, id, a.OrgID)
	if err != nil {
		return Project{}, fmt.Errorf("getting project: %w", err)
	}
	return p, nil
}
