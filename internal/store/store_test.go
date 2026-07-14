package store

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Every migration's Down section must actually work (ADR-0001: an untested
// rollback is not a rollback), so roll the full set up, all the way down,
// and back up.
func TestMigrateUpDownUp(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; -short set")
	}
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("nabu_test"),
		tcpostgres.WithUsername("nabu"),
		tcpostgres.WithPassword("nabu"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("starting postgres container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("getting connection string: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening connection: %v", err)
	}
	defer func() { _ = db.Close() }()

	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatalf("reading embedded migrations: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		t.Fatalf("creating provider: %v", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatalf("down to 0: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up after down: %v", err)
	}
}
