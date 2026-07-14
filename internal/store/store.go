// Package store owns shared database setup: the pgx connection pool and the
// goose migrations runner. Domain packages own their queries; nothing else
// belongs here.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"
)

// Migrations are embedded so a deployed binary migrates itself at startup —
// no separate migration artifact to ship. This directory is also sqlc's
// schema source (ADR-0001).
//
//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("opening migration connection: %w", err)
	}
	defer func() { _ = db.Close() }()

	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("reading embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		return fmt.Errorf("creating migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return nil
}

// NewPool creates the shared connection pool and verifies connectivity, so a
// bad DATABASE_URL fails at startup rather than on the first request.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return pool, nil
}
