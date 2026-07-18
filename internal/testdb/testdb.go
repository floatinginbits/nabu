// Package testdb provides the shared Postgres bootstrap for integration
// tests: one container per test binary, migrated and pooled. Without it every
// package that touches the database starts its own container, and a full
// `go test ./...` pays that cost once per package.
package testdb

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/floatinginbits/nabu/internal/store"
)

type options struct {
	tracer pgx.QueryTracer
	seed   func(ctx context.Context, pool *pgxpool.Pool) error
}

// Option configures Main.
type Option func(*options)

// WithTracer attaches a pgx tracer to the pool's connections, for tests that
// need to observe the SQL a repository actually sends.
func WithTracer(tracer pgx.QueryTracer) Option {
	return func(o *options) { o.tracer = tracer }
}

// WithSeed runs fn once after migrations, before any test. A failure aborts
// the run rather than leaving tests to fail against a half-seeded database.
func WithSeed(fn func(ctx context.Context, pool *pgxpool.Pool) error) Option {
	return func(o *options) { o.seed = fn }
}

// Main runs m against a freshly migrated Postgres container, assigning the
// connection pool to *pool before the first test, and exits the process with
// m's status. Under -short no container starts and *pool stays nil, so tests
// must guard with SkipIfShort.
//
// It takes **pgxpool.Pool rather than returning the pool so each package keeps
// its own package-level testPool and the standard TestMain shape stays a
// one-liner.
func Main(m *testing.M, pool **pgxpool.Pool, opts ...Option) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	code, err := run(m, pool, opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M, pool **pgxpool.Pool, opts []Option) (int, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("nabu_test"),
		tcpostgres.WithUsername("nabu"),
		tcpostgres.WithPassword("nabu"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return 0, fmt.Errorf("starting postgres container: %w", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(container) }()

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return 0, fmt.Errorf("getting connection string: %w", err)
	}
	if err := store.Migrate(ctx, dsn); err != nil {
		return 0, fmt.Errorf("migrating test database: %w", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return 0, fmt.Errorf("parsing pool config: %w", err)
	}
	cfg.ConnConfig.Tracer = o.tracer
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return 0, fmt.Errorf("creating pool: %w", err)
	}
	defer p.Close()
	*pool = p

	if o.seed != nil {
		if err := o.seed(ctx, p); err != nil {
			return 0, fmt.Errorf("seeding test database: %w", err)
		}
	}

	return m.Run(), nil
}

// SkipIfShort skips an integration test when -short is set, where Main has
// left the pool nil.
func SkipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test; -short set")
	}
}

// Truncate empties tables between tests that share the one container. All
// tables go in one statement: Postgres rejects truncating a table another
// table still references, so truncating a foreign-key group one statement at
// a time fails regardless of the order they're passed in.
func Truncate(ctx context.Context, t *testing.T, pool *pgxpool.Pool, tables ...string) {
	t.Helper()
	if len(tables) == 0 {
		return
	}
	quoted := make([]string, len(tables))
	for i, table := range tables {
		// A table name can't be a bind parameter; Sanitize quotes it instead.
		quoted[i] = pgx.Identifier{table}.Sanitize()
	}
	if _, err := pool.Exec(ctx, "TRUNCATE "+strings.Join(quoted, ", ")); err != nil {
		t.Fatalf("truncating %s: %v", strings.Join(tables, ", "), err)
	}
}
