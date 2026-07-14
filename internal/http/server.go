// Package http holds Nabu's HTTP handlers and middleware. Handlers translate
// requests and responses only — business logic lives in the domain services.
package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/floatinginbits/nabu/internal/http/api"
	"github.com/floatinginbits/nabu/internal/task"
	"github.com/floatinginbits/nabu/internal/web"
)

// NewHandler builds the full HTTP handler: the routes generated from the
// OpenAPI spec, wrapped in the fixed middleware chain requestID → logging →
// recovery (see backend-design.md; auth and RBAC join the chain in M2), so
// logging always captures a request even when a handler panics.
func NewHandler(log *slog.Logger, tasks *task.Service) http.Handler {
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

	h := api.HandlerWithOptions(&apiServer{log: log, tasks: tasks}, api.StdHTTPServerOptions{
		BaseRouter: mux,
		// Parameter binding errors (e.g. non-integer pageSize) surface here.
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		},
	})
	h = recovery(log)(h)
	h = logging(log)(h)
	h = requestID(h)
	return h
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
