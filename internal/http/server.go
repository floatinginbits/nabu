// Package http holds Nabu's HTTP handlers and middleware. Handlers translate
// requests and responses only — business logic lives in the domain services.
package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// NewHandler builds the full HTTP handler: routes wrapped in the fixed
// middleware chain requestID → logging → recovery (see backend-design.md;
// auth and RBAC join the chain in M2), so logging always captures a request
// even when a handler panics.
func NewHandler(log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)

	var h http.Handler = mux
	h = recovery(log)(h)
	h = logging(log)(h)
	h = requestID(h)
	return h
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Encoding a value we constructed can't fail, and the status is already
	// written — nothing useful to do with an error here.
	_ = json.NewEncoder(w).Encode(body)
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError emits the API error envelope from ARCHITECTURE.md:
// {"error": {"code": "...", "message": "..."}}.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]errorDetail{"error": {Code: code, Message: message}})
}
