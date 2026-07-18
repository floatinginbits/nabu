package task

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/actor"
	"github.com/floatinginbits/nabu/internal/audit"
	"github.com/floatinginbits/nabu/internal/audit/audittest"
	"github.com/floatinginbits/nabu/internal/project"
)

type fakeRepo struct {
	calls        int
	gotProjectID uuid.UUID
	gotTitle     string
	gotFilter    ListFilter
	tasks        []Task
	err          error
}

func (f *fakeRepo) Create(_ context.Context, projectID uuid.UUID, title string) (Task, error) {
	f.calls++
	f.gotProjectID, f.gotTitle = projectID, title
	if f.err != nil {
		return Task{}, f.err
	}
	return Task{ID: uuid.New(), ProjectID: projectID, Title: title, Status: StatusTodo}, nil
}

func (f *fakeRepo) List(_ context.Context, filter ListFilter) ([]Task, error) {
	f.calls++
	f.gotFilter = filter
	return f.tasks, f.err
}

// fakeProjects resolves exactly the projects seeded into it, and only inside
// the org they belong to — the same "another org's project does not exist"
// behavior the real service has, including reading the org from the context
// rather than a parameter.
type fakeProjects struct {
	orgID uuid.UUID
	known map[uuid.UUID]bool
}

func (f *fakeProjects) GetByID(ctx context.Context, id uuid.UUID) (project.Project, error) {
	a, ok := actor.FromContext(ctx)
	if !ok {
		return project.Project{}, actor.ErrNoActor
	}
	if a.OrgID != f.orgID || !f.known[id] {
		return project.Project{}, project.ErrNotFound
	}
	return project.Project{ID: id, OrgID: a.OrgID, Key: "GEN", Name: "General"}, nil
}

// testScope is the org, project, and actor context a service test runs inside.
type testScope struct {
	orgID     uuid.UUID
	userID    uuid.UUID
	projectID uuid.UUID
	ctx       context.Context
	projects  *fakeProjects
	// audit is checked against the secret denylist when the test ends, so
	// every entry any case in this file produces is covered (ADR-0004).
	audit *audittest.Recorder
}

func newTestScope(t *testing.T) testScope {
	t.Helper()
	orgID, userID, projectID := uuid.New(), uuid.New(), uuid.New()
	return testScope{
		orgID:     orgID,
		userID:    userID,
		projectID: projectID,
		ctx:       actor.NewContext(context.Background(), actor.Actor{UserID: userID, OrgID: orgID}),
		projects:  &fakeProjects{orgID: orgID, known: map[uuid.UUID]bool{projectID: true}},
		audit:     audittest.New(t),
	}
}

func (s testScope) service(repo Repository) *Service { return NewService(repo, s.projects, s.audit) }

func newTestTask(createdAt time.Time) Task {
	return Task{ID: uuid.New(), Title: "t", Status: StatusTodo, CreatedAt: createdAt, UpdatedAt: createdAt}
}

// A context with no actor is a wiring bug, not client input: the service must
// fail rather than fall through to a query scoped to the zero org, which would
// silently return or write across every org. Asserted once, on both methods,
// because every other case in this file supplies an actor. Create surfaces it
// from project resolution, which is what owns the org scope it needs.
func TestServiceRejectsContextWithoutActor(t *testing.T) {
	s := newTestScope(t)
	repo := &fakeRepo{}
	svc := s.service(repo)
	bare := context.Background()

	if _, err := svc.List(bare, ListParams{}); !errors.Is(err, actor.ErrNoActor) {
		t.Errorf("List() error = %v, want ErrNoActor", err)
	}
	if _, err := svc.Create(bare, s.projectID, "title"); !errors.Is(err, actor.ErrNoActor) {
		t.Errorf("Create() error = %v, want ErrNoActor", err)
	}
	// The failure must happen before any query: an unscoped call is the thing
	// the guard exists to prevent, not just the missing error.
	if repo.calls != 0 {
		t.Errorf("repository called %d times without an actor, want 0", repo.calls)
	}
	// It is an internal error, never a client-visible validation error — the
	// HTTP layer maps ValidationError to 422 and everything else to 500.
	var ve *ValidationError
	if _, err := svc.List(bare, ListParams{}); errors.As(err, &ve) {
		t.Error("List() without an actor returned a ValidationError, want an internal error")
	}
}

func TestServiceCreate(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		wantTitle string
		wantErr   bool
	}{
		{"valid", "Fix the bug", "Fix the bug", false},
		{"trims whitespace", "  padded  ", "padded", false},
		{"empty", "", "", true},
		{"whitespace only", "   ", "", true},
		{"too long", strings.Repeat("x", maxTitleLen+1), "", true},
		{"at max length", strings.Repeat("x", maxTitleLen), strings.Repeat("x", maxTitleLen), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScope(t)
			repo := &fakeRepo{}
			got, err := s.service(repo).Create(s.ctx, s.projectID, tt.title)
			if tt.wantErr {
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("Create() error = %v, want ValidationError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create() error: %v", err)
			}
			if got.Title != tt.wantTitle || repo.gotTitle != tt.wantTitle {
				t.Errorf("Create() title = %q (repo saw %q), want %q", got.Title, repo.gotTitle, tt.wantTitle)
			}
			if repo.gotProjectID != s.projectID {
				t.Errorf("repo saw project %v, want %v", repo.gotProjectID, s.projectID)
			}
		})
	}
}

func TestServiceCreateRecordsAudit(t *testing.T) {
	s := newTestScope(t)
	repo := &fakeRepo{}

	created, err := s.service(repo).Create(s.ctx, s.projectID, "Fix the bug")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	entries := s.audit.Entries()
	if len(entries) != 1 {
		t.Fatalf("recorded %d audit entries, want 1", len(entries))
	}
	got := entries[0]
	want := audit.Entry{
		ActorID:    uuid.NullUUID{UUID: s.userID, Valid: true},
		OrgID:      s.orgID,
		ProjectID:  uuid.NullUUID{UUID: s.projectID, Valid: true},
		Action:     "task.created",
		EntityType: "task",
		EntityID:   created.ID,
		Metadata:   map[string]any{"title": "Fix the bug", "status": string(StatusTodo)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("audit entry = %+v, want %+v", got, want)
	}
}

// Nothing happened, so nothing is audited: an audit row for a rejected write
// makes the trail describe changes the database never saw.
func TestServiceCreateDoesNotAuditFailures(t *testing.T) {
	tests := []struct {
		name      string
		projectID func(s testScope) uuid.UUID
		title     string
		repoErr   error
	}{
		{name: "invalid title", projectID: func(s testScope) uuid.UUID { return s.projectID }, title: "  "},
		{name: "unknown project", projectID: func(_ testScope) uuid.UUID { return uuid.New() }, title: "a title"},
		{name: "repository failure", projectID: func(s testScope) uuid.UUID { return s.projectID }, title: "a title", repoErr: errors.New("connection refused")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScope(t)
			repo := &fakeRepo{err: tt.repoErr}
			if _, err := s.service(repo).Create(s.ctx, tt.projectID(s), tt.title); err == nil {
				t.Fatal("Create() succeeded, want an error")
			}
			if n := len(s.audit.Entries()); n != 0 {
				t.Errorf("recorded %d audit entries for a failed create, want 0", n)
			}
		})
	}
}

// A projectId is client-supplied, so it is authorization input: a project the
// actor's org cannot see must be rejected as unknown, and must never reach the
// repository (security-baseline.md).
func TestServiceCreateRejectsProjectOutsideTheActorsOrg(t *testing.T) {
	s := newTestScope(t)
	repo := &fakeRepo{}

	_, err := s.service(repo).Create(s.ctx, uuid.New(), "a title")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("Create() error = %v, want ValidationError", err)
	}
	if repo.calls != 0 {
		t.Errorf("repository called %d times for an unresolvable project, want 0", repo.calls)
	}
}

// The org a query runs in comes from the session actor; only the optional
// project filter comes from the caller, so a request can narrow what it sees
// but never widen it.
func TestServiceListScope(t *testing.T) {
	s := newTestScope(t)

	t.Run("org comes from the actor", func(t *testing.T) {
		repo := &fakeRepo{}
		if _, err := s.service(repo).List(s.ctx, ListParams{}); err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if repo.gotFilter.OrgID != s.orgID {
			t.Errorf("filter OrgID = %v, want the actor's %v", repo.gotFilter.OrgID, s.orgID)
		}
		if repo.gotFilter.ProjectID != nil {
			t.Errorf("filter ProjectID = %v, want nil when unset", repo.gotFilter.ProjectID)
		}
	})

	t.Run("project filter is forwarded", func(t *testing.T) {
		repo := &fakeRepo{}
		if _, err := s.service(repo).List(s.ctx, ListParams{ProjectID: &s.projectID}); err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if repo.gotFilter.ProjectID == nil || *repo.gotFilter.ProjectID != s.projectID {
			t.Errorf("filter ProjectID = %v, want %v", repo.gotFilter.ProjectID, s.projectID)
		}
		if repo.gotFilter.OrgID != s.orgID {
			t.Errorf("filter OrgID = %v, want the actor's %v", repo.gotFilter.OrgID, s.orgID)
		}
	})
}

func TestServiceListPageSize(t *testing.T) {
	tests := []struct {
		name      string
		pageSize  int
		wantLimit int
	}{
		{"default", 0, defaultPageSize + 1},
		{"explicit", 10, 11},
		{"capped at max", maxPageSize + 500, maxPageSize + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScope(t)
			repo := &fakeRepo{}
			if _, err := s.service(repo).List(s.ctx, ListParams{PageSize: tt.pageSize}); err != nil {
				t.Fatalf("List() error: %v", err)
			}
			if repo.gotFilter.Limit != tt.wantLimit {
				t.Errorf("repo limit = %d, want %d", repo.gotFilter.Limit, tt.wantLimit)
			}
		})
	}
}

func TestServiceListValidation(t *testing.T) {
	bad := Status("bogus")
	tests := []struct {
		name   string
		params ListParams
	}{
		{"unknown status", ListParams{Status: &bad}},
		{"garbage cursor", ListParams{Cursor: "not-base64!!!"}},
		{"valid base64, wrong payload", ListParams{Cursor: "e30"}}, // {}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestScope(t)
			_, err := s.service(&fakeRepo{}).List(s.ctx, tt.params)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("List() error = %v, want ValidationError", err)
			}
		})
	}
}

func TestServiceListNextCursor(t *testing.T) {
	now := time.Now()
	full := make([]Task, 3) // pageSize 2 + 1 extra row
	for i := range full {
		full[i] = newTestTask(now.Add(-time.Duration(i) * time.Minute))
	}

	t.Run("more pages", func(t *testing.T) {
		s := newTestScope(t)
		repo := &fakeRepo{tasks: full}
		res, err := s.service(repo).List(s.ctx, ListParams{PageSize: 2})
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(res.Tasks) != 2 {
			t.Fatalf("len(tasks) = %d, want 2 (extra row trimmed)", len(res.Tasks))
		}
		if res.NextCursor == "" {
			t.Fatal("NextCursor empty, want set")
		}
		c, err := decodeCursor(res.NextCursor)
		if err != nil {
			t.Fatalf("decoding returned cursor: %v", err)
		}
		if c.ID != res.Tasks[1].ID {
			t.Errorf("cursor points at %v, want last returned task %v", c.ID, res.Tasks[1].ID)
		}
	})

	t.Run("last page", func(t *testing.T) {
		s := newTestScope(t)
		repo := &fakeRepo{tasks: full[:2]} // exactly pageSize rows, no extra
		res, err := s.service(repo).List(s.ctx, ListParams{PageSize: 2})
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(res.Tasks) != 2 || res.NextCursor != "" {
			t.Errorf("got %d tasks, cursor %q; want 2 tasks and empty cursor", len(res.Tasks), res.NextCursor)
		}
	})

	t.Run("single item", func(t *testing.T) {
		s := newTestScope(t)
		repo := &fakeRepo{tasks: full[:1]}
		res, err := s.service(repo).List(s.ctx, ListParams{PageSize: 2})
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(res.Tasks) != 1 || res.NextCursor != "" {
			t.Errorf("got %d tasks, cursor %q; want 1 task and empty cursor", len(res.Tasks), res.NextCursor)
		}
	})

	t.Run("empty", func(t *testing.T) {
		s := newTestScope(t)
		res, err := s.service(&fakeRepo{}).List(s.ctx, ListParams{})
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(res.Tasks) != 0 || res.NextCursor != "" {
			t.Errorf("got %d tasks, cursor %q; want none", len(res.Tasks), res.NextCursor)
		}
	})
}

func TestCursorRoundTrip(t *testing.T) {
	want := Cursor{CreatedAt: time.Now().UTC().Truncate(time.Microsecond), ID: uuid.New()}
	got, err := decodeCursor(encodeCursor(want))
	if err != nil {
		t.Fatalf("decodeCursor() error: %v", err)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || got.ID != want.ID {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}
