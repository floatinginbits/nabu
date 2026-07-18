package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/actor"
	"github.com/floatinginbits/nabu/internal/auth"
)

type ctxKey int

const requestIDKey ctxKey = iota

// RequestIDFromContext returns the request ID set by the requestID middleware,
// or "" outside a request.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func newRequestID() string {
	var b [16]byte
	// crypto/rand.Read never fails on supported platforms (it panics instead).
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// statusRecorder captures the response status for the logging middleware.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			log.InfoContext(r.Context(), "request",
				slog.String("request_id", RequestIDFromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}

// isPublic reports whether a request bypasses the access-token check: any
// non-API route (the SPA and /health) and the auth endpoints that establish or
// clear a session, none of which can require a valid access token to be usable.
func isPublic(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	switch r.URL.Path {
	case "/api/v1/auth/login", "/api/v1/auth/refresh", "/api/v1/auth/logout":
		return true
	}
	return false
}

// csrf rejects state-changing API requests that lack the custom header the
// client wrapper always sends. A cross-site attacker cannot set a custom header
// without a preflight we never grant, so this defeats cookie-driven CSRF
// (ADR-0003). Safe methods and non-API routes are exempt.
func csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get(csrfHeader) == "" {
				// Its own code, not FORBIDDEN: this is a client that failed to
				// send a header, which RBAC's "you lack permission" 403 will
				// also carry once it lands. The frontend has to tell a bug in
				// its own wrapper apart from a real authorization denial.
				writeError(w, http.StatusForbidden, "CSRF_REQUIRED", "missing "+csrfHeader+" header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuth validates the access-token cookie on protected routes and puts
// the session actor in the context. Public routes (see isPublic) pass through.
//
// orgID is the org the session belongs to. It is resolved server-side rather
// than taken from the request, so a client can never widen its own scope
// (security-baseline.md); once multi-org lands it comes from a token claim
// instead, and services never learn the difference.
func requireAuth(authsvc *auth.Service, orgID uuid.UUID) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublic(r) {
				next.ServeHTTP(w, r)
				return
			}
			cookie, err := r.Cookie(accessCookie)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
				return
			}
			userID, err := authsvc.VerifyAccessToken(cookie.Value)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
				return
			}
			ctx := actor.NewContext(r.Context(), actor.Actor{UserID: userID, OrgID: orgID})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func recovery(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				p := recover()
				if p == nil {
					return
				}
				// net/http uses this sentinel to abort a response; it must
				// propagate, not be treated as a crash.
				if p == http.ErrAbortHandler {
					panic(p)
				}
				log.ErrorContext(r.Context(), "panic recovered",
					slog.String("request_id", RequestIDFromContext(r.Context())),
					slog.Any("panic", p),
					slog.String("stack", string(debug.Stack())),
				)
				writeError(w, http.StatusInternalServerError, "INTERNAL", "internal server error")
			}()
			next.ServeHTTP(w, r)
		})
	}
}
