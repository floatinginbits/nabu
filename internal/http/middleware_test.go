package http

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/actor"
	"github.com/floatinginbits/nabu/internal/auth"
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

// requireAuth guards every /api route that is not on isPublic's list, and the
// rejection must use the standard error envelope so the client can switch on
// the code (api-contract.md).
func TestRequireAuth(t *testing.T) {
	h, _ := newTestHandler(t)
	validToken := mintAccessToken(t, uuid.New(), time.Now().Add(time.Hour), testAuthSecret)

	tests := []struct {
		name       string
		method     string
		cookie     *http.Cookie
		wantStatus int
	}{
		{"GET without a cookie", http.MethodGet, nil, http.StatusUnauthorized},
		{"POST without a cookie", http.MethodPost, nil, http.StatusUnauthorized},
		{"garbage token", http.MethodGet, &http.Cookie{Name: accessCookie, Value: "not-a-jwt"}, http.StatusUnauthorized},
		{"expired token", http.MethodGet, &http.Cookie{
			Name:  accessCookie,
			Value: mintAccessToken(t, uuid.New(), time.Now().Add(-time.Minute), testAuthSecret),
		}, http.StatusUnauthorized},
		{"token signed with another key", http.MethodGet, &http.Cookie{
			Name:  accessCookie,
			Value: mintAccessToken(t, uuid.New(), time.Now().Add(time.Hour), "an-entirely-different-signing-key!!"),
		}, http.StatusUnauthorized},
		// The wrong cookie name is no cookie at all.
		{"token in the wrong cookie", http.MethodGet, &http.Cookie{Name: "nabu_token", Value: validToken}, http.StatusUnauthorized},
		{"valid session", http.MethodGet, &http.Cookie{Name: accessCookie, Value: validToken}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.method == http.MethodPost {
				body = strings.NewReader(`{"title":"x"}`)
			}
			req := httptest.NewRequest(tt.method, "/api/v1/tasks", body)
			// Present the CSRF header throughout, so a 401 here is the auth
			// check talking and not the CSRF middleware in front of it.
			req.Header.Set(csrfHeader, "1")
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}
			w := do(h, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantStatus == http.StatusUnauthorized {
				assertErrorCode(t, w, http.StatusUnauthorized, "UNAUTHORIZED")
			}
		})
	}
}

// The actor reaching the handler must carry the org the middleware was wired
// with, not just the user from the token — phase 1 fills OrgID in from the
// resolved singleton org, and a service reading a zero org would silently query
// nothing rather than fail.
func TestRequireAuthPopulatesActor(t *testing.T) {
	log := slog.New(&logRecorder{})
	authsvc := auth.NewService(nil, nil, []byte(testAuthSecret), log)
	userID, orgID := uuid.New(), uuid.New()

	var got actor.Actor
	var ok bool
	h := requireAuth(authsvc, orgID)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = actor.FromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	req.AddCookie(&http.Cookie{
		Name:  accessCookie,
		Value: mintAccessToken(t, userID, time.Now().Add(time.Hour), testAuthSecret),
	})
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !ok {
		t.Fatal("no actor in the handler's context")
	}
	if got.UserID != userID {
		t.Errorf("actor UserID = %v, want %v", got.UserID, userID)
	}
	if got.OrgID != orgID {
		t.Errorf("actor OrgID = %v, want %v", got.OrgID, orgID)
	}
}

// A public route never runs the token check, so nothing downstream may assume
// an actor is present.
func TestRequireAuthLeavesPublicRoutesWithoutAnActor(t *testing.T) {
	log := slog.New(&logRecorder{})
	authsvc := auth.NewService(nil, nil, []byte(testAuthSecret), log)

	var ok bool
	h := requireAuth(authsvc, uuid.New())(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, ok = actor.FromContext(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil))

	if ok {
		t.Error("public route reached the handler with an actor in context")
	}
}

// csrf requires the custom header on state-changing API requests; safe methods
// and non-API routes are exempt (ADR-0003).
func TestCSRF(t *testing.T) {
	h, _ := newTestHandler(t)
	session := &http.Cookie{
		Name:  accessCookie,
		Value: mintAccessToken(t, uuid.New(), time.Now().Add(time.Hour), testAuthSecret),
	}

	tests := []struct {
		name       string
		method     string
		path       string
		header     string
		wantStatus int
	}{
		{"POST without the header", http.MethodPost, "/api/v1/tasks", "", http.StatusForbidden},
		{"DELETE without the header", http.MethodDelete, "/api/v1/tasks", "", http.StatusForbidden},
		{"POST with the header", http.MethodPost, "/api/v1/tasks", "1", http.StatusCreated},
		// Safe methods carry no CSRF risk and must not need the header.
		{"GET is exempt", http.MethodGet, "/api/v1/tasks", "", http.StatusOK},
		{"HEAD is exempt", http.MethodHead, "/api/v1/tasks", "", http.StatusOK},
		// The SPA and /health are not /api routes.
		{"non-API route is exempt", http.MethodPost, "/not-api", "", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.method == http.MethodPost {
				body = strings.NewReader(`{"title":"x"}`)
			}
			req := httptest.NewRequest(tt.method, tt.path, body)
			req.AddCookie(session)
			if tt.header != "" {
				req.Header.Set(csrfHeader, tt.header)
			}
			w := do(h, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantStatus == http.StatusForbidden {
				assertErrorCode(t, w, http.StatusForbidden, "CSRF_REQUIRED")
			}
		})
	}

	// csrf sits outside requireAuth in the chain (see NewHandler), so a request
	// missing both is refused for the CSRF header first.
	t.Run("csrf is checked before auth", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"title":"x"}`))
		assertErrorCode(t, do(h, req), http.StatusForbidden, "CSRF_REQUIRED")
	})
}

// The routes that must work without a session: anything that isn't /api, plus
// the endpoints that establish or clear one (isPublic in middleware.go). Each
// case asserts a status only the handler behind the middleware produces —
// reaching the handler is the proof the route is public.
func TestPublicRoutes(t *testing.T) {
	h, _ := newTestHandler(t)

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{"health", http.MethodGet, "/health", "", http.StatusOK},
		{"SPA root", http.MethodGet, "/", "", http.StatusOK},
		{"SPA client route", http.MethodGet, "/some/client/route", "", http.StatusOK},
		// A malformed body means login parsed the request rather than being
		// turned away for having no session.
		{"login", http.MethodPost, "/api/v1/auth/login", `{"email":`, http.StatusBadRequest},
		// Logout with nothing to revoke is a no-op 204, never a 401.
		{"logout", http.MethodPost, "/api/v1/auth/logout", "", http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if strings.HasPrefix(tt.path, "/api/") && tt.method != http.MethodGet {
				req.Header.Set(csrfHeader, "1")
			}
			w := do(h, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
	// The third auth endpoint, refresh, needs the database to reach its
	// handler; TestRefreshWorksWithADeadAccessToken covers that it is public.
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
