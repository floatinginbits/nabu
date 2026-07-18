// Package http holds Nabu's HTTP handlers and middleware. Handlers translate
// requests and responses only — business logic lives in the domain services.
package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/auth"
	"github.com/floatinginbits/nabu/internal/http/api"
	"github.com/floatinginbits/nabu/internal/project"
	"github.com/floatinginbits/nabu/internal/task"
	"github.com/floatinginbits/nabu/internal/user"
	"github.com/floatinginbits/nabu/internal/web"
)

var errMissingOrgID = errors.New("Deps.OrgID must be set")

// Deps are the dependencies NewHandler wires into the HTTP layer.
type Deps struct {
	Log          *slog.Logger
	Tasks        *task.Service
	Projects     *project.Service
	Auth         *auth.Service
	Users        *user.Service
	CookieSecure bool
	// OrgID is the org every session is scoped to, resolved from the singleton
	// organizations row at startup.
	OrgID uuid.UUID
}

// NewHandler builds the full HTTP handler: the routes generated from the
// OpenAPI spec, wrapped in the fixed middleware chain requestID → logging →
// recovery → csrf → auth (see backend-design.md), so logging always captures a
// request even when a handler panics and auth runs after recovery.
// It fails rather than starting with a zero OrgID: OrgID is zero-value-valid on
// a struct literal, so an omitted field would put uuid.Nil in every actor and
// silently return empty results from every org-scoped query instead of erroring
// (backend-design.md: fail fast on missing config).
func NewHandler(d Deps) (http.Handler, error) {
	if d.OrgID == uuid.Nil {
		return nil, fmt.Errorf("building handler: %w", errMissingOrgID)
	}
	log := d.Log
	mux := http.NewServeMux()
	// The SPA takes every GET the API doesn't claim ("GET /" rather than "/"
	// keeps ServeMux's 405-on-wrong-method behavior for non-API routes), and
	// unknown /api/ paths get the JSON envelope rather than index.html. The
	// fallback is registered per method (an all-method "/api/" pattern
	// conflicts with "GET /"), so a wrong method on a real API path reports
	// 404 instead of 405 — acceptable until an endpoint needs the
	// distinction.
	mux.Handle("GET /", web.Handler())
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		mux.HandleFunc(method+" /api/", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "no such endpoint")
		})
	}

	srv := &apiServer{
		log:          log,
		tasks:        d.Tasks,
		projects:     d.Projects,
		auth:         d.Auth,
		users:        d.Users,
		cookieSecure: d.CookieSecure,
	}
	h := api.HandlerWithOptions(srv, api.StdHTTPServerOptions{
		BaseRouter: mux,
		// Parameter binding errors (e.g. non-integer pageSize) surface here.
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		},
	})
	h = requireAuth(d.Auth, d.OrgID)(h)
	h = csrf(h)
	h = recovery(log)(h)
	h = logging(log)(h)
	h = requestID(h)
	return h, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Encoding a value we constructed can't fail, and the status is already
	// written — nothing useful to do with an error here.
	_ = json.NewEncoder(w).Encode(body)
}

// writeError emits the API error envelope from ARCHITECTURE.md:
// {"error": {"code": "...", "message": "..."}}.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, api.Error{Error: api.ErrorDetail{Code: code, Message: message}})
}
