package task

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/floatinginbits/nabu/internal/store"
)

// Shared across all integration tests in this package; set up in TestMain.
var (
	testPool    *pgxpool.Pool
	testCapture = &queryCapture{}
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	code, err := runWithPostgres(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

func runWithPostgres(m *testing.M) (int, error) {
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
	cfg.ConnConfig.Tracer = testCapture
	testPool, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return 0, fmt.Errorf("creating pool: %w", err)
	}
	defer testPool.Close()

	return m.Run(), nil
}

// queryCapture records the last SQL + args the repository sent, so the
// EXPLAIN test analyzes the exact query production runs — it cannot drift
// from queries.sql.
type queryCapture struct {
	mu   sync.Mutex
	sql  string
	args []any
}

func (c *queryCapture) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sql, c.args = data.SQL, data.Args
	return ctx
}

func (c *queryCapture) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (c *queryCapture) last() (string, []any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sql, c.args
}

// truncateTasks resets table state between tests (one shared container).
func truncateTasks(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test; -short set")
	}
	if _, err := testPool.Exec(context.Background(), "TRUNCATE tasks"); err != nil {
		t.Fatalf("truncating tasks: %v", err)
	}
}

func TestPostgresCreateAndList(t *testing.T) {
	truncateTasks(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)

	created, err := repo.Create(ctx, "first task")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.ID.String() == "" || created.Title != "first task" || created.Status != StatusTodo {
		t.Errorf("Create() = %+v, want todo task titled 'first task' with id", created)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Error("Create() timestamps are zero")
	}

	got, err := repo.List(ctx, ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(got) != 1 || got[0].ID != created.ID {
		t.Errorf("List() = %+v, want the created task", got)
	}
}

func TestPostgresListStatusFilter(t *testing.T) {
	truncateTasks(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)

	if _, err := repo.Create(ctx, "stays todo"); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	done, err := repo.Create(ctx, "gets done")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if _, err := testPool.Exec(ctx, "UPDATE tasks SET status = 'done' WHERE id = $1", done.ID); err != nil {
		t.Fatalf("updating status: %v", err)
	}

	status := StatusDone
	got, err := repo.List(ctx, ListFilter{Status: &status, Limit: 10})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(got) != 1 || got[0].ID != done.ID || got[0].Status != StatusDone {
		t.Errorf("List(done) = %+v, want only the done task", got)
	}
}

func TestPostgresCursorPagination(t *testing.T) {
	truncateTasks(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)
	svc := NewService(repo)

	// Explicit created_at spacing keeps ordering deterministic.
	for i := range 5 {
		if _, err := testPool.Exec(ctx,
			"INSERT INTO tasks (title, created_at, updated_at) VALUES ($1, now() - make_interval(mins => $2), now())",
			fmt.Sprintf("task %d", i), i); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}

	var seen []string
	cursor := ""
	pages := 0
	for {
		res, err := svc.List(ctx, ListParams{PageSize: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("List() page %d error: %v", pages, err)
		}
		for _, task := range res.Tasks {
			seen = append(seen, task.Title)
		}
		pages++
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}

	if pages != 3 || len(seen) != 5 {
		t.Fatalf("got %d pages with %d tasks, want 3 pages with 5 tasks", pages, len(seen))
	}
	// Newest first: task 0 has the most recent created_at.
	want := []string{"task 0", "task 1", "task 2", "task 3", "task 4"}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("page order = %v, want %v", seen, want)
		}
	}
}

func TestPostgresPaginationExactPageBoundary(t *testing.T) {
	truncateTasks(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)
	svc := NewService(repo)

	for i := range 4 {
		if _, err := testPool.Exec(ctx,
			"INSERT INTO tasks (title, created_at, updated_at) VALUES ($1, now() - make_interval(mins => $2), now())",
			fmt.Sprintf("task %d", i), i); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}

	first, err := svc.List(ctx, ListParams{PageSize: 2})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(first.Tasks) != 2 || first.NextCursor == "" {
		t.Fatalf("first page: %d tasks, cursor %q; want 2 tasks + cursor", len(first.Tasks), first.NextCursor)
	}

	second, err := svc.List(ctx, ListParams{PageSize: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(second.Tasks) != 2 || second.NextCursor != "" {
		t.Errorf("second page: %d tasks, cursor %q; want exactly 2 tasks and no cursor", len(second.Tasks), second.NextCursor)
	}
}

// ADR-0001 mitigation: EXPLAIN the real filtered task-list query on real
// Postgres so planner surprises fail a test, not production. Captures the SQL
// the repository actually executes (via the pgx tracer) and asserts the
// pagination path uses the (created_at, id) index rather than a Seq Scan.
// Caveat: EXPLAIN with bound values yields a custom plan; generic-plan
// behavior after statement reuse can differ (see backend-design.md).
func TestPostgresListQueryUsesIndex(t *testing.T) {
	truncateTasks(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)

	if _, err := testPool.Exec(ctx, `
		INSERT INTO tasks (title, status, created_at, updated_at)
		SELECT 'task ' || i,
		       (ARRAY['todo','in_progress','done'])[1 + i % 3]::task_status,
		       now() - make_interval(secs => i),
		       now()
		FROM generate_series(1, 5000) AS i`); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if _, err := testPool.Exec(ctx, "ANALYZE tasks"); err != nil {
		t.Fatalf("analyzing: %v", err)
	}

	// A mid-table cursor, so an efficient plan must seek, not scan from the top.
	var mid Cursor
	row := testPool.QueryRow(ctx, "SELECT created_at, id FROM tasks ORDER BY created_at DESC, id DESC OFFSET 2500 LIMIT 1")
	if err := row.Scan(&mid.CreatedAt, &mid.ID); err != nil {
		t.Fatalf("picking cursor row: %v", err)
	}

	if _, err := repo.List(ctx, ListFilter{After: &mid, Limit: 51}); err != nil {
		t.Fatalf("List() error: %v", err)
	}
	sql, args := testCapture.last()
	if !strings.Contains(sql, "FROM tasks") {
		t.Fatalf("captured unexpected query: %s", sql)
	}

	rows, err := testPool.Query(ctx, "EXPLAIN "+sql, args...)
	if err != nil {
		t.Fatalf("EXPLAIN error: %v", err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scanning plan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading plan: %v", err)
	}

	planText := plan.String()
	if strings.Contains(planText, "Seq Scan on tasks") {
		t.Errorf("cursor-paginated list falls back to a sequential scan:\n%s", planText)
	}
	if !strings.Contains(planText, "tasks_created_at_id_idx") {
		t.Errorf("cursor-paginated list does not use tasks_created_at_id_idx:\n%s", planText)
	}
}
