package task

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/floatinginbits/nabu/internal/task/sqlcgen"
)

type PostgresRepository struct {
	q *sqlcgen.Queries
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{q: sqlcgen.New(pool)}
}

func (r *PostgresRepository) Create(ctx context.Context, projectID uuid.UUID, title string) (Task, error) {
	row, err := r.q.CreateTask(ctx, sqlcgen.CreateTaskParams{ProjectID: projectID, Title: title})
	if err != nil {
		return Task{}, fmt.Errorf("inserting task: %w", err)
	}
	return Task{
		ID:        row.ID,
		ProjectID: row.ProjectID,
		Title:     row.Title,
		Status:    Status(row.Status),
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

func (r *PostgresRepository) List(ctx context.Context, f ListFilter) ([]Task, error) {
	params := sqlcgen.ListTasksParams{OrgID: f.OrgID, PageSize: int32(f.Limit)}
	if f.ProjectID != nil {
		params.ProjectID = uuid.NullUUID{UUID: *f.ProjectID, Valid: true}
	}
	if f.Status != nil {
		params.Status = sqlcgen.NullTaskStatus{TaskStatus: sqlcgen.TaskStatus(*f.Status), Valid: true}
	}
	if f.After != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: f.After.CreatedAt, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: f.After.ID, Valid: true}
	}

	rows, err := r.q.ListTasks(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("querying tasks: %w", err)
	}
	tasks := make([]Task, len(rows))
	for i, row := range rows {
		tasks[i] = Task{
			ID:        row.ID,
			ProjectID: row.ProjectID,
			Title:     row.Title,
			Status:    Status(row.Status),
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
		}
	}
	return tasks, nil
}
