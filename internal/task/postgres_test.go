package task

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/floatinginbits/nabu/internal/actor"
	"github.com/floatinginbits/nabu/internal/audit"
	"github.com/floatinginbits/nabu/internal/audit/audittest"
	"github.com/floatinginbits/nabu/internal/project"
	"github.com/floatinginbits/nabu/internal/testdb"
)

// Shared across all integration tests in this package; set up in TestMain.
var (
	testPool    *pgxpool.Pool
	testCapture = &queryCapture{}
)

func TestMain(m *testing.M) {
	testdb.Main(m, &testPool, testdb.WithTracer(testCapture))
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

// fixture is the seeded org and its default project. A task cannot exist
// without a project, so every test in this package works inside one.
type fixture struct {
	orgID     uuid.UUID
	projectID uuid.UUID
}

// reset returns the database to just-migrated state: no tasks, and only the
// project migration 00004 seeds. It deliberately does not truncate projects —
// that would delete the seeded default project and leave nothing to create a
// task in.
func reset(t *testing.T) fixture {
	t.Helper()
	testdb.SkipIfShort(t)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, "DELETE FROM projects WHERE lower(key) <> 'gen'"); err != nil {
		t.Fatalf("deleting test projects: %v", err)
	}
	testdb.Truncate(ctx, t, testPool, "tasks", "audit_logs")

	var f fixture
	row := testPool.QueryRow(ctx, "SELECT id, org_id FROM projects WHERE lower(key) = 'gen'")
	if err := row.Scan(&f.projectID, &f.orgID); err != nil {
		t.Fatalf("reading seeded project: %v", err)
	}
	return f
}

// actorCtx is what every service call needs: the org scope comes from the
// session actor, never from the caller's arguments.
func (f fixture) actorCtx() context.Context {
	return actor.NewContext(context.Background(), actor.Actor{UserID: uuid.New(), OrgID: f.orgID})
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	return NewService(NewPostgresRepository(testPool),
		project.NewService(project.NewPostgresRepository(testPool)), audittest.New(t))
}

// seedTasks inserts tasks with explicit created_at spacing so ordering is
// deterministic; index 0 is the newest.
func seedTasks(t *testing.T, projectID uuid.UUID, n int) {
	t.Helper()
	for i := range n {
		if _, err := testPool.Exec(context.Background(),
			"INSERT INTO tasks (project_id, title, created_at, updated_at) VALUES ($1, $2, now() - make_interval(mins => $3), now())",
			projectID, fmt.Sprintf("task %d", i), i); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
}

// createProject inserts a project directly: project.Service has no Create (it
// is admin-only and lands with RBAC), and these tests need more than one.
func createProject(t *testing.T, orgID uuid.UUID, key, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	row := testPool.QueryRow(context.Background(),
		"INSERT INTO projects (org_id, key, name) VALUES ($1, $2, $3) RETURNING id", orgID, key, name)
	if err := row.Scan(&id); err != nil {
		t.Fatalf("seeding project %s: %v", key, err)
	}
	return id
}

func TestPostgresCreateAndList(t *testing.T) {
	f := reset(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)

	created, err := repo.Create(ctx, f.projectID, "first task")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.ID == uuid.Nil || created.Title != "first task" || created.Status != StatusTodo {
		t.Errorf("Create() = %+v, want todo task titled 'first task' with id", created)
	}
	if created.ProjectID != f.projectID {
		t.Errorf("Create() ProjectID = %v, want %v", created.ProjectID, f.projectID)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Error("Create() timestamps are zero")
	}

	got, err := repo.List(ctx, ListFilter{Scope: Scope{OrgID: f.orgID}, Limit: 10})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(got) != 1 || got[0].ID != created.ID {
		t.Errorf("List() = %+v, want the created task", got)
	}
}

func TestPostgresListStatusFilter(t *testing.T) {
	f := reset(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)

	if _, err := repo.Create(ctx, f.projectID, "stays todo"); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	done, err := repo.Create(ctx, f.projectID, "gets done")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if _, err := testPool.Exec(ctx, "UPDATE tasks SET status = 'done' WHERE id = $1", done.ID); err != nil {
		t.Fatalf("updating status: %v", err)
	}

	status := StatusDone
	got, err := repo.List(ctx, ListFilter{Scope: Scope{OrgID: f.orgID}, Status: &status, Limit: 10})
	if err != nil {
		t.Fatalf("List(done) error: %v", err)
	}
	if len(got) != 1 || got[0].ID != done.ID || got[0].Status != StatusDone {
		t.Errorf("List(done) = %+v, want only the done task", got)
	}
}

// The org scope is what makes the RBAC model meaningful
// (security-baseline.md), so it is asserted against real rows rather than
// assumed from reading the SQL.
func TestPostgresListScoping(t *testing.T) {
	f := reset(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)

	other := createProject(t, f.orgID, "OTH", "Other")
	mine, err := repo.Create(ctx, f.projectID, "in the default project")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if _, err := repo.Create(ctx, other, "in the other project"); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	t.Run("no project filter returns the whole org", func(t *testing.T) {
		got, err := repo.List(ctx, ListFilter{Scope: Scope{OrgID: f.orgID}, Limit: 10})
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("List() returned %d tasks, want both", len(got))
		}
	})

	t.Run("project filter narrows", func(t *testing.T) {
		got, err := repo.List(ctx, ListFilter{Scope: Scope{OrgID: f.orgID, ProjectID: &f.projectID}, Limit: 10})
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(got) != 1 || got[0].ID != mine.ID {
			t.Errorf("List(project) = %+v, want only the default project's task", got)
		}
	})

	t.Run("another org sees nothing", func(t *testing.T) {
		got, err := repo.List(ctx, ListFilter{Scope: Scope{OrgID: uuid.New()}, Limit: 10})
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("List(other org) = %+v, want nothing", got)
		}
	})
}

func TestPostgresCursorPagination(t *testing.T) {
	f := reset(t)
	ctx := f.actorCtx()
	svc := newTestService(t)

	seedTasks(t, f.projectID, 5)

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
	f := reset(t)
	ctx := f.actorCtx()
	svc := newTestService(t)

	seedTasks(t, f.projectID, 4)

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
// pagination path uses an index rather than a Seq Scan.
//
// Two cases, because the two list shapes need two different indexes: an
// unfiltered list cannot use a leading-project_id index to satisfy
// ORDER BY created_at DESC, id DESC, and a project-filtered list should not
// walk the whole org. One case alone would pass while the other shape silently
// regressed to a sequential scan.
//
// Caveat: EXPLAIN with bound values yields a custom plan; generic-plan
// behavior after statement reuse can differ (see backend-design.md).
func TestPostgresListQueryUsesIndex(t *testing.T) {
	f := reset(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)

	// Several projects, so filtering by one is selective and the planner's
	// choice between the two indexes is a real one.
	projectIDs := []uuid.UUID{f.projectID}
	for _, key := range []string{"P1", "P2", "P3", "P4"} {
		projectIDs = append(projectIDs, createProject(t, f.orgID, key, key))
	}
	for i, id := range projectIDs {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO tasks (project_id, title, status, created_at, updated_at)
			SELECT $1,
			       'task ' || i,
			       (ARRAY['todo','in_progress','done'])[1 + i % 3]::task_status,
			       now() - make_interval(secs => i * 5 + $2),
			       now()
			FROM generate_series(1, 2000) AS i`, id, i); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
	if _, err := testPool.Exec(ctx, "ANALYZE tasks"); err != nil {
		t.Fatalf("analyzing tasks: %v", err)
	}
	if _, err := testPool.Exec(ctx, "ANALYZE projects"); err != nil {
		t.Fatalf("analyzing projects: %v", err)
	}

	// A mid-table cursor, so an efficient plan must seek, not scan from the top.
	var mid Cursor
	row := testPool.QueryRow(ctx, "SELECT created_at, id FROM tasks ORDER BY created_at DESC, id DESC OFFSET 5000 LIMIT 1")
	if err := row.Scan(&mid.CreatedAt, &mid.ID); err != nil {
		t.Fatalf("picking cursor row: %v", err)
	}

	tests := []struct {
		name      string
		filter    ListFilter
		wantIndex string
	}{
		{
			name:      "no project filter",
			filter:    ListFilter{Scope: Scope{OrgID: f.orgID}, After: &mid, Limit: 51},
			wantIndex: "tasks_created_at_id_idx",
		},
		{
			name:      "project filter",
			filter:    ListFilter{Scope: Scope{OrgID: f.orgID, ProjectID: &f.projectID}, After: &mid, Limit: 51},
			wantIndex: "tasks_project_created_at_id_idx",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := repo.List(ctx, tt.filter); err != nil {
				t.Fatalf("List() error: %v", err)
			}
			planText := explainLast(t)
			if strings.Contains(planText, "Seq Scan on tasks") {
				t.Errorf("cursor-paginated list falls back to a sequential scan:\n%s", planText)
			}
			if !strings.Contains(planText, tt.wantIndex) {
				t.Errorf("cursor-paginated list does not use %s:\n%s", tt.wantIndex, planText)
			}
		})
	}
}

// explainLast EXPLAINs the query the repository last sent.
func explainLast(t *testing.T) string {
	t.Helper()
	sql, args := testCapture.last()
	if !strings.Contains(sql, "FROM tasks") {
		t.Fatalf("captured unexpected query: %s", sql)
	}

	rows, err := testPool.Query(context.Background(), "EXPLAIN "+sql, args...)
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
	return plan.String()
}

// The audit trail is best-effort: an audit write that fails must not fail the
// state change it describes (ADR-0004). The failure here is a real one rather
// than a stubbed error — the actor is a user id that does not exist, so the
// audit row violates its actor_id foreign key against real Postgres.
func TestPostgresCreateSurvivesAuditFailure(t *testing.T) {
	f := reset(t)
	logs := &logCapture{}
	svc := NewService(
		NewPostgresRepository(testPool),
		project.NewService(project.NewPostgresRepository(testPool)),
		audit.NewPostgresRecorder(testPool, slog.New(logs)),
	)

	created, err := svc.Create(f.actorCtx(), f.projectID, "survives a broken audit trail")
	if err != nil {
		t.Fatalf("Create() error = %v, want the task despite the audit failure", err)
	}

	var count int
	if err := testPool.QueryRow(context.Background(),
		"SELECT count(*) FROM tasks WHERE id = $1", created.ID).Scan(&count); err != nil {
		t.Fatalf("counting tasks: %v", err)
	}
	if count != 1 {
		t.Errorf("task rows = %d, want the task committed even though its audit row was not", count)
	}
	if n := auditRowCount(t); n != 0 {
		t.Fatalf("audit rows = %d, want 0 — the test's premise is that the insert fails", n)
	}

	rec, ok := logs.find("recording audit entry")
	if !ok {
		t.Fatal("audit failure was not logged; a silently dropped audit row is indistinguishable from no action")
	}
	// The failure log must not carry metadata: redacting it at the column and
	// then printing it into a log pipeline that leaves the host redacts nothing.
	for key := range rec {
		if key == "metadata" {
			t.Errorf("audit failure log contains metadata: %v", rec)
		}
	}
	if rec["action"] != "task.created" {
		t.Errorf("audit failure log action = %v, want task.created", rec["action"])
	}
}

func auditRowCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM audit_logs").Scan(&n); err != nil {
		t.Fatalf("counting audit rows: %v", err)
	}
	return n
}

// logCapture collects slog records as attribute maps.
type logCapture struct {
	mu      sync.Mutex
	records []map[string]any
}

func (l *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (l *logCapture) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]any{"msg": r.Message}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, attrs)
	return nil
}

func (l *logCapture) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logCapture) WithGroup(string) slog.Handler      { return l }

func (l *logCapture) find(msg string) (map[string]any, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.records {
		if r["msg"] == msg {
			return r, true
		}
	}
	return nil, false
}
