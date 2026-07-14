package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/floatinginbits/nabu/internal/http/api"
	"github.com/floatinginbits/nabu/internal/task"
)

// stubRepo satisfies task.Repository for tests that never reach the database.
type stubRepo struct{}

func (stubRepo) Create(context.Context, string) (task.Task, error)          { return task.Task{}, nil }
func (stubRepo) List(context.Context, task.ListFilter) ([]task.Task, error) { return nil, nil }

func newTestHandler(t *testing.T) (http.Handler, *logRecorder) {
	t.Helper()
	rec := &logRecorder{}
	return NewHandler(slog.New(rec), task.NewService(stubRepo{})), rec
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
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}

	t.Run("API 404 uses the error envelope", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
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
