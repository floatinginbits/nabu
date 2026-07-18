package project

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/floatinginbits/nabu/internal/project/sqlcgen"
)

type PostgresRepository struct {
	q *sqlcgen.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{q: sqlcgen.New(pool)}
}

func (r *PostgresRepository) List(ctx context.Context, orgID uuid.UUID) ([]Project, error) {
	rows, err := r.q.ListProjects(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("querying projects: %w", err)
	}
	ps := make([]Project, len(rows))
	for i, row := range rows {
		ps[i] = fromRow(row)
	}
	return ps, nil
}

func (r *PostgresRepository) GetByID(ctx context.Context, id, orgID uuid.UUID) (Project, error) {
	row, err := r.q.GetProjectByID(ctx, sqlcgen.GetProjectByIDParams{ID: id, OrgID: orgID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("querying project by id: %w", err)
	}
	return fromRow(row), nil
}

// SingletonOrgID returns the one organization row's id. v1 is single-org
// (ARCHITECTURE.md) and the schema enforces that with a unique index on a
// constant, so main.go resolves this once at startup and every session is
// scoped to it. It is deliberately off Repository: services take the org as a
// parameter and never resolve it themselves.
func (r *PostgresRepository) SingletonOrgID(ctx context.Context) (uuid.UUID, error) {
	id, err := r.q.GetSingletonOrgID(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("querying organization: %w", err)
	}
	return id, nil
}

func fromRow(row sqlcgen.Project) Project {
	return Project{
		ID:        row.ID,
		OrgID:     row.OrgID,
		Key:       row.Key,
		Name:      row.Name,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}
