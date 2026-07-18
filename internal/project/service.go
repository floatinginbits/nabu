package project

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]Project, error) {
	ps, err := s.repo.List(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	return ps, nil
}

// GetByID resolves a project inside orgID. A project in another org is
// indistinguishable from one that does not exist: both are ErrNotFound.
func (s *Service) GetByID(ctx context.Context, id, orgID uuid.UUID) (Project, error) {
	p, err := s.repo.GetByID(ctx, id, orgID)
	if err != nil {
		return Project{}, fmt.Errorf("getting project: %w", err)
	}
	return p, nil
}
