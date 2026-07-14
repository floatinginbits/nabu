package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/floatinginbits/nabu/internal/http/api"
)

// chain builds the same middleware stack as NewHandler around an arbitrary
// handler, so middleware behavior is testable with panicking/etc. handlers.
func chain(log *slog.Logger, h http.Handler) http.Handler {
	return requestID(logging(log)(recovery(log)(h)))
}

func TestRequestID(t *testing.T) {
	tests := []struct {
		name     string
		incoming string
	}{
		{"generated when absent", ""},
		{"incoming header preserved", "test-id-123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotCtxID string
			h := chain(slog.New(&logRecorder{}), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotCtxID = RequestIDFromContext(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.incoming != "" {
				req.Header.Set("X-Request-ID", tt.incoming)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			headerID := w.Header().Get("X-Request-ID")
			if headerID == "" {
				t.Fatal("X-Request-ID missing from response")
			}
			if gotCtxID != headerID {
				t.Errorf("context ID %q != response header ID %q", gotCtxID, headerID)
			}
			if tt.incoming != "" && headerID != tt.incoming {
				t.Errorf("header ID = %q, want incoming %q preserved", headerID, tt.incoming)
			}
		})
	}
}

func TestRecoveryTurnsPanicInto500(t *testing.T) {
	rec := &logRecorder{}
	h := chain(slog.New(rec), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/panics", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	var body api.Error
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if body.Error.Code != "INTERNAL" {
		t.Errorf("error code = %q, want INTERNAL", body.Error.Code)
	}
}

// The middleware order invariant from backend-design.md: logging sits outside
// recovery, so a request is logged (with its final 500 status) even when the
// handler panics.
func TestPanickedRequestIsStillLogged(t *testing.T) {
	rec := &logRecorder{}
	h := chain(slog.New(rec), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/panics", nil)
	req.Header.Set("X-Request-ID", "panic-req-1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	var reqLog map[string]any
	for _, r := range rec.records {
		if r["msg"] == "request" {
			reqLog = r
		}
	}
	if reqLog == nil {
		t.Fatal("no request log record emitted for panicked request")
	}
	if reqLog["status"] != int64(http.StatusInternalServerError) {
		t.Errorf("logged status = %v, want %d", reqLog["status"], http.StatusInternalServerError)
	}
	if reqLog["request_id"] != "panic-req-1" {
		t.Errorf("logged request_id = %v, want panic-req-1", reqLog["request_id"])
	}
}
