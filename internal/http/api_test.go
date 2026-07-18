package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/floatinginbits/nabu/internal/audit"
	"github.com/floatinginbits/nabu/internal/audit/audittest"
	"github.com/floatinginbits/nabu/internal/auth"
	"github.com/floatinginbits/nabu/internal/http/api"
	"github.com/floatinginbits/nabu/internal/project"
	"github.com/floatinginbits/nabu/internal/task"
	"github.com/floatinginbits/nabu/internal/testdb"
	"github.com/floatinginbits/nabu/internal/user"
)

// Integration tests: the full handler → service → repository chain against
// real Postgres (testing-strategy.md), no real HTTP server.
var testPool *pgxpool.Pool

const (
	// testAuthSecret stands in for NABU_AUTH_SECRET (>= 32 bytes per ADR-0003).
	testAuthSecret = "test-auth-secret-not-a-real-key-32b"

	testUserEmail       = "tester@nabu.test"
	testUserDisplayName = "Test User"
	testUserPassword    = "correct-horse-battery-staple"
)

// testUserID is the suite's single seeded account. Creating a user costs a
// bcrypt hash at cost 12 (user package), so the suite seeds one account in
// TestMain rather than one per test.
var testUserID uuid.UUID

func TestMain(m *testing.M) {
	testdb.Main(m, &testPool, testdb.WithSeed(func(ctx context.Context, pool *pgxpool.Pool) error {
		u, err := user.NewService(user.NewPostgresRepository(pool)).
			Create(ctx, testUserEmail, testUserDisplayName, testUserPassword)
		if err != nil {
			return fmt.Errorf("seeding test user: %w", err)
		}
		testUserID = u.ID
		return nil
	}))
}

// testServer is the production handler plus the services behind it, so a test
// can mint a real session against the very auth.Service the middleware chain
// verifies against — auth is exercised, never stubbed out.
type testServer struct {
	http.Handler
	auth *auth.Service
	log  *logRecorder
	// projectID is the seeded 'GEN' project every task in these tests is
	// created in; requests carry it as CreateTaskRequest.projectId.
	projectID uuid.UUID
}

// newAPIHandler builds the production handler over a clean tasks table.
func newAPIHandler(t *testing.T) *testServer {
	t.Helper()
	testdb.SkipIfShort(t)
	testdb.Truncate(context.Background(), t, testPool, "tasks", "audit_logs")
	return newTestServer(t, false)
}

// seededScope reads the org and default project migration 00004 creates. They
// are resolved from the database rather than hardcoded so a change to the
// seed's identifiers surfaces as a migration change, not as tests quietly
// querying an org that no longer exists.
func seededScope(t *testing.T) (orgID, projectID uuid.UUID) {
	t.Helper()
	row := testPool.QueryRow(context.Background(),
		"SELECT id, org_id FROM projects WHERE lower(key) = 'gen'")
	if err := row.Scan(&projectID, &orgID); err != nil {
		t.Fatalf("resolving the seeded project: %v", err)
	}
	return orgID, projectID
}

// newTestServer wires the production handler against the test database.
// cookieSecure mirrors Deps.CookieSecure (NABU_COOKIE_SECURE).
func newTestServer(t *testing.T, cookieSecure bool) *testServer {
	t.Helper()
	testdb.SkipIfShort(t)
	rec := &logRecorder{}
	log := slog.New(rec)
	orgID, projectID := seededScope(t)
	// Tee'd rather than faked: the real recorder keeps this suite's end-to-end
	// check that an audited request actually lands a row, and the wrapper brings
	// the suite under the ADR-0004 denylist — this is where a new endpoint's
	// first audited action is most likely to be exercised.
	recorder := audittest.Tee(t, audit.NewPostgresRecorder(testPool, log))
	users := user.NewService(user.NewPostgresRepository(testPool))
	authSvc := auth.NewService(users, auth.NewPostgresRefreshRepository(testPool), recorder, []byte(testAuthSecret), orgID, log)
	projects := project.NewService(project.NewPostgresRepository(testPool))
	h, err := NewHandler(Deps{
		Log:          log,
		Tasks:        task.NewService(task.NewPostgresRepository(testPool), projects, recorder),
		Projects:     projects,
		Auth:         authSvc,
		Users:        users,
		CookieSecure: cookieSecure,
		OrgID:        orgID,
	})
	if err != nil {
		t.Fatalf("NewHandler() error: %v", err)
	}
	return &testServer{Handler: h, auth: authSvc, log: rec, projectID: projectID}
}

var (
	sessionOnce   sync.Once
	sessionCookie *http.Cookie
	sessionErr    error
)

// testSession returns an access cookie for the seeded user, minted by a real
// login through a real auth.Service. It is cached for the whole suite: the
// access token is a stateless JWT signed with testAuthSecret, so it is valid
// for every handler instance here, and logging in per test would pay bcrypt
// cost 12 each time.
func testSession(t *testing.T, authsvc *auth.Service) *http.Cookie {
	t.Helper()
	sessionOnce.Do(func() {
		pair, _, err := authsvc.Login(context.Background(), testUserEmail, testUserPassword)
		if err != nil {
			sessionErr = err
			return
		}
		sessionCookie = &http.Cookie{Name: accessCookie, Value: pair.Access}
	})
	if sessionErr != nil {
		t.Fatalf("minting test session: %v", sessionErr)
	}
	return sessionCookie
}

// doJSON sends an authenticated JSON request. /api/v1/tasks is no longer
// public, so every call carries the session cookie and the CSRF header that
// the real client wrapper always sends (ADR-0003); tests that exercise auth
// itself build their own requests instead.
func doJSON(t *testing.T, ts *testServer, method, path, body string) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(testSession(t, ts.auth))
	req.Header.Set(csrfHeader, "1")
	w := do(ts, req)

	fields := map[string]json.RawMessage{}
	if len(w.Body.Bytes()) > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
			t.Fatalf("%s %s: decoding response %q: %v", method, path, w.Body.String(), err)
		}
	}
	return w, fields
}

// do runs one request through the handler with nothing added to it — the
// building block for tests that control the cookies and headers themselves.
func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func assertErrorCode(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, wantStatus, w.Body.String())
	}
	var body api.Error
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error envelope from %q: %v", w.Body.String(), err)
	}
	if body.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", body.Error.Code, wantCode)
	}
	if body.Error.Message == "" {
		t.Error("error message empty")
	}
}

// createTaskBody builds a valid CreateTaskRequest in the seeded project, so a
// test that is not about the project field doesn't have to restate it.
func (ts *testServer) createTaskBody(title string) string {
	return fmt.Sprintf(`{"title":%q,"projectId":%q}`, title, ts.projectID)
}

func TestCreateTaskEndpoint(t *testing.T) {
	h := newAPIHandler(t)

	w, _ := doJSON(t, h, http.MethodPost, "/api/v1/tasks", h.createTaskBody("Ship the walking skeleton"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
	var created api.Task
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding task: %v", err)
	}
	if created.Title != "Ship the walking skeleton" || created.Status != api.Todo {
		t.Errorf("created = %+v, want todo task with title", created)
	}
	if created.Id.String() == "00000000-0000-0000-0000-000000000000" {
		t.Error("created task has zero id")
	}
	if created.ProjectId != h.projectID {
		t.Errorf("created projectId = %v, want the seeded project %v", created.ProjectId, h.projectID)
	}
	if created.CreatedAt.IsZero() {
		t.Error("createdAt is zero")
	}
}

// End to end: a request that changes state leaves an audit row scoped to the
// session's org, project, and user — none of which the request supplies.
func TestCreateTaskEndpointRecordsAudit(t *testing.T) {
	h := newAPIHandler(t)

	w, _ := doJSON(t, h, http.MethodPost, "/api/v1/tasks", h.createTaskBody("audited"))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
	var created api.Task
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding task: %v", err)
	}

	var (
		action    string
		actorID   uuid.UUID
		projectID uuid.UUID
		entityID  uuid.UUID
	)
	err := testPool.QueryRow(context.Background(),
		`SELECT action, actor_id, project_id, entity_id FROM audit_logs WHERE action = 'task.created'`).
		Scan(&action, &actorID, &projectID, &entityID)
	if err != nil {
		t.Fatalf("reading the audit row for the created task: %v", err)
	}
	if entityID != created.Id {
		t.Errorf("audit entity_id = %v, want the created task %v", entityID, created.Id)
	}
	if projectID != h.projectID {
		t.Errorf("audit project_id = %v, want %v", projectID, h.projectID)
	}
	if actorID != testUserID {
		t.Errorf("audit actor_id = %v, want the session user %v", actorID, testUserID)
	}
}

func TestCreateTaskValidation(t *testing.T) {
	h := newAPIHandler(t)

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"empty title", h.createTaskBody(""), http.StatusUnprocessableEntity},
		{"whitespace title", h.createTaskBody("   "), http.StatusUnprocessableEntity},
		{"malformed json", `{"title":`, http.StatusBadRequest},
		{"title too long", h.createTaskBody(strings.Repeat("x", 501)), http.StatusUnprocessableEntity},
		// A projectId the session's org cannot see is rejected as unknown, not
		// as forbidden: it must not confirm the project exists elsewhere.
		{"project in another org", fmt.Sprintf(`{"title":"x","projectId":%q}`, uuid.New()), http.StatusUnprocessableEntity},
		{"missing projectId", `{"title":"x"}`, http.StatusUnprocessableEntity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, _ := doJSON(t, h, http.MethodPost, "/api/v1/tasks", tt.body)
			assertErrorCode(t, w, tt.wantStatus, "VALIDATION_ERROR")
		})
	}
}

func TestListTasksEndpoint(t *testing.T) {
	h := newAPIHandler(t)

	t.Run("empty list", func(t *testing.T) {
		w, fields := doJSON(t, h, http.MethodGet, "/api/v1/tasks", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if string(fields["data"]) != "[]" {
			t.Errorf(`data = %s, want []`, fields["data"])
		}
		if string(fields["nextCursor"]) != "null" {
			t.Errorf("nextCursor = %s, want null", fields["nextCursor"])
		}
	})

	for i := 1; i <= 3; i++ {
		w, _ := doJSON(t, h, http.MethodPost, "/api/v1/tasks", h.createTaskBody(fmt.Sprintf("task %d", i)))
		if w.Code != http.StatusCreated {
			t.Fatalf("seeding task %d: status %d", i, w.Code)
		}
	}

	t.Run("cursor walk with pageSize 1", func(t *testing.T) {
		var titles []string
		cursor := ""
		for range 5 { // bounded; expect to break after 3 pages
			path := "/api/v1/tasks?pageSize=1"
			if cursor != "" {
				path += "&cursor=" + cursor
			}
			w, _ := doJSON(t, h, http.MethodGet, path, "")
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
			}
			var page api.TaskList
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatalf("decoding page: %v", err)
			}
			for _, item := range page.Data {
				titles = append(titles, item.Title)
			}
			if page.NextCursor == nil {
				break
			}
			cursor = *page.NextCursor
		}
		if len(titles) != 3 {
			t.Fatalf("walked %d tasks, want 3: %v", len(titles), titles)
		}
		// Newest first.
		if titles[0] != "task 3" || titles[2] != "task 1" {
			t.Errorf("order = %v, want newest first", titles)
		}
	})

	t.Run("status filter", func(t *testing.T) {
		w, _ := doJSON(t, h, http.MethodGet, "/api/v1/tasks?status=done", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		var page api.TaskList
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatalf("decoding page: %v", err)
		}
		if len(page.Data) != 0 {
			t.Errorf("done tasks = %d, want 0", len(page.Data))
		}
	})

	// projectId narrows within the session's org; an id outside it matches
	// nothing rather than reaching across the org boundary.
	t.Run("project filter", func(t *testing.T) {
		w, _ := doJSON(t, h, http.MethodGet, "/api/v1/tasks?projectId="+h.projectID.String(), "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
		var page api.TaskList
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatalf("decoding page: %v", err)
		}
		if len(page.Data) != 3 {
			t.Errorf("tasks in the seeded project = %d, want 3", len(page.Data))
		}

		w, _ = doJSON(t, h, http.MethodGet, "/api/v1/tasks?projectId="+uuid.New().String(), "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatalf("decoding page: %v", err)
		}
		if len(page.Data) != 0 {
			t.Errorf("tasks for an unknown project = %d, want 0", len(page.Data))
		}
	})

	t.Run("invalid params", func(t *testing.T) {
		w, _ := doJSON(t, h, http.MethodGet, "/api/v1/tasks?pageSize=abc", "")
		assertErrorCode(t, w, http.StatusBadRequest, "VALIDATION_ERROR")

		w, _ = doJSON(t, h, http.MethodGet, "/api/v1/tasks?cursor=garbage", "")
		assertErrorCode(t, w, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

		w, _ = doJSON(t, h, http.MethodGet, "/api/v1/tasks?status=bogus", "")
		assertErrorCode(t, w, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	})
}

// GET /api/v1/projects lists the caller's org only, which for v1's singleton
// org is the seeded 'GEN' project.
func TestListProjectsEndpoint(t *testing.T) {
	h := newAPIHandler(t)

	w, _ := doJSON(t, h, http.MethodGet, "/api/v1/projects", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var list api.ProjectList
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decoding projects: %v", err)
	}
	if len(list.Data) != 1 {
		t.Fatalf("got %d projects, want the seeded one", len(list.Data))
	}
	if list.Data[0].Id != h.projectID || list.Data[0].Key != "GEN" {
		t.Errorf("project = %+v, want the seeded GEN project %v", list.Data[0], h.projectID)
	}
}
