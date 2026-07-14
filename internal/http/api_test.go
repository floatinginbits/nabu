package http

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/floatinginbits/nabu/internal/http/api"
	"github.com/floatinginbits/nabu/internal/store"
	"github.com/floatinginbits/nabu/internal/task"
)

// Integration tests: the full handler → service → repository chain against
// real Postgres (testing-strategy.md), no real HTTP server.
var testPool *pgxpool.Pool

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
	testPool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		return 0, fmt.Errorf("creating pool: %w", err)
	}
	defer testPool.Close()

	return m.Run(), nil
}

// newAPIHandler builds the production handler over a clean tasks table.
func newAPIHandler(t *testing.T) http.Handler {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test; -short set")
	}
	if _, err := testPool.Exec(context.Background(), "TRUNCATE tasks"); err != nil {
		t.Fatalf("truncating tasks: %v", err)
	}
	svc := task.NewService(task.NewPostgresRepository(testPool))
	return NewHandler(slog.New(&logRecorder{}), svc)
}

func doJSON(t *testing.T, h http.Handler, method, path, body string) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	fields := map[string]json.RawMessage{}
	if len(w.Body.Bytes()) > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
			t.Fatalf("%s %s: decoding response %q: %v", method, path, w.Body.String(), err)
		}
	}
	return w, fields
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

func TestCreateTaskEndpoint(t *testing.T) {
	h := newAPIHandler(t)

	w, _ := doJSON(t, h, http.MethodPost, "/api/v1/tasks", `{"title":"Ship the walking skeleton"}`)
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
	if created.CreatedAt.IsZero() {
		t.Error("createdAt is zero")
	}
}

func TestCreateTaskValidation(t *testing.T) {
	h := newAPIHandler(t)

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"empty title", `{"title":""}`, http.StatusUnprocessableEntity},
		{"whitespace title", `{"title":"   "}`, http.StatusUnprocessableEntity},
		{"malformed json", `{"title":`, http.StatusBadRequest},
		{"title too long", fmt.Sprintf(`{"title":%q}`, strings.Repeat("x", 501)), http.StatusUnprocessableEntity},
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
		w, _ := doJSON(t, h, http.MethodPost, "/api/v1/tasks", fmt.Sprintf(`{"title":"task %d"}`, i))
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

	t.Run("invalid params", func(t *testing.T) {
		w, _ := doJSON(t, h, http.MethodGet, "/api/v1/tasks?pageSize=abc", "")
		assertErrorCode(t, w, http.StatusBadRequest, "VALIDATION_ERROR")

		w, _ = doJSON(t, h, http.MethodGet, "/api/v1/tasks?cursor=garbage", "")
		assertErrorCode(t, w, http.StatusUnprocessableEntity, "VALIDATION_ERROR")

		w, _ = doJSON(t, h, http.MethodGet, "/api/v1/tasks?status=bogus", "")
		assertErrorCode(t, w, http.StatusUnprocessableEntity, "VALIDATION_ERROR")
	})
}
