package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/auth"
	"github.com/floatinginbits/nabu/internal/http/api"
	"github.com/floatinginbits/nabu/internal/project"
	"github.com/floatinginbits/nabu/internal/task"
)

// stubRepo satisfies task.Repository for tests that never reach the database.
type stubRepo struct{}

func (stubRepo) Create(context.Context, uuid.UUID, string) (task.Task, error) {
	return task.Task{}, nil
}
func (stubRepo) List(context.Context, task.ListFilter) ([]task.Task, error) { return nil, nil }

// stubProjects satisfies task.Projects by resolving every project id. These
// tests are about routing and the middleware chain, so project validation is
// deliberately out of the way; api_test.go covers it against real rows.
type stubProjects struct{}

func (stubProjects) GetByID(_ context.Context, id, orgID uuid.UUID) (project.Project, error) {
	return project.Project{ID: id, OrgID: orgID, Key: "GEN", Name: "General"}, nil
}

// newTestHandler builds the production handler with no database behind it.
// The auth service is real — verifying an access token needs only the signing
// secret, no repository — so the real middleware chain still runs, and these
// routing/middleware tests stay fast enough for -short.
//
// OrgID is synthetic rather than the seeded org: nothing here reaches Postgres,
// so there is no row to resolve, and under -short no container exists at all.
// The integration fixtures in api_test.go use the real one.
func newTestHandler(t *testing.T) (http.Handler, *logRecorder) {
	t.Helper()
	rec := &logRecorder{}
	log := slog.New(rec)
	h, err := NewHandler(Deps{
		Log:   log,
		Tasks: task.NewService(stubRepo{}, stubProjects{}),
		Auth:  auth.NewService(nil, nil, []byte(testAuthSecret), log),
		// Users is only reached by GET /users/me, which these tests don't call;
		// the auth_test.go integration tests cover it against real Postgres.
		Users:        nil,
		CookieSecure: false,
		OrgID:        uuid.New(),
	})
	if err != nil {
		t.Fatalf("NewHandler() error: %v", err)
	}
	return h, rec
}

// NewHandler fails fast on a zero OrgID because the field is zero-value-valid
// on a struct literal: an omitted OrgID would otherwise put uuid.Nil in every
// actor and make org-scoped queries return empty results instead of erroring.
// The failure mode is silent, so the guard needs a test.
func TestNewHandlerRequiresOrgID(t *testing.T) {
	log := slog.New(&logRecorder{})
	h, err := NewHandler(Deps{
		Log:   log,
		Tasks: task.NewService(stubRepo{}, stubProjects{}),
		Auth:  auth.NewService(nil, nil, []byte(testAuthSecret), log),
	})
	if err == nil {
		t.Fatal("NewHandler() with a zero OrgID returned no error")
	}
	if !errors.Is(err, errMissingOrgID) {
		t.Errorf("NewHandler() error = %v, want errMissingOrgID", err)
	}
	if h != nil {
		t.Error("NewHandler() returned a handler alongside its error")
	}
}

func TestHealth(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf(`body = %v, want {"status":"ok"}`, body)
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header missing from response")
	}
}

// TestRouting covers where a request lands, so every API request here carries
// a valid session and the CSRF header — routing is only observable past the
// auth and CSRF middleware. Their rejections are middleware_test.go's subject.
func TestRouting(t *testing.T) {
	h, _ := newTestHandler(t)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"unknown API path gets JSON 404", http.MethodGet, "/api/v1/nope", http.StatusNotFound},
		{"wrong method on health", http.MethodPost, "/health", http.StatusMethodNotAllowed},
		// The all-method /api/ fallback wins over 405 here; see NewHandler.
		{"wrong method on tasks", http.MethodDelete, "/api/v1/tasks", http.StatusNotFound},
		// Client-side routes fall back to the SPA's index.html.
		{"SPA fallback", http.MethodGet, "/some/client/route", http.StatusOK},
		{"SPA root", http.MethodGet, "/", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := do(h, authedRequest(t, tt.method, tt.path, nil))
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}

	t.Run("API 404 uses the error envelope", func(t *testing.T) {
		w := do(h, authedRequest(t, http.MethodGet, "/api/v1/nope", nil))
		var body api.Error
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decoding body %q: %v", w.Body.String(), err)
		}
		if body.Error.Code != "NOT_FOUND" {
			t.Errorf("error code = %q, want NOT_FOUND", body.Error.Code)
		}
	})
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "title is required")

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnprocessableEntity)
	}
	var body api.Error
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if body.Error.Code != "VALIDATION_ERROR" || body.Error.Message != "title is required" {
		t.Errorf("body = %+v, want code VALIDATION_ERROR with message", body)
	}
}

// logRecorder is a minimal slog.Handler capturing records for assertions.
type logRecorder struct {
	records []map[string]any
}

func (l *logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (l *logRecorder) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]any{"msg": r.Message, "level": r.Level.String()}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	l.records = append(l.records, attrs)
	return nil
}

func (l *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logRecorder) WithGroup(string) slog.Handler      { return l }
