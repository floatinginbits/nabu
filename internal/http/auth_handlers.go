package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/floatinginbits/nabu/internal/auth"
	"github.com/floatinginbits/nabu/internal/http/api"
	"github.com/floatinginbits/nabu/internal/user"
)

func (s *apiServer) Login(w http.ResponseWriter, r *http.Request) {
	var req api.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid JSON body")
		return
	}
	pair, u, err := s.auth.Login(r.Context(), string(req.Email), req.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid email or password")
		return
	}
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	s.setSessionCookies(w, pair)
	writeJSON(w, http.StatusOK, toUserProfile(u))
}

func (s *apiServer) Refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(refreshCookie)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "no session to refresh")
		return
	}
	pair, err := s.auth.Refresh(r.Context(), cookie.Value)
	if errors.Is(err, auth.ErrInvalidToken) {
		// The token was invalid, expired, or reused; clear the stale cookies so
		// the client stops presenting them.
		s.clearSessionCookies(w)
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "session expired")
		return
	}
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	s.setSessionCookies(w, pair)
	w.WriteHeader(http.StatusNoContent)
}

func (s *apiServer) Logout(w http.ResponseWriter, r *http.Request) {
	// Logout is idempotent: an absent or already-revoked token still clears the
	// cookies and returns 204. A revocation that actually fails is the one case
	// that does not — it surfaces as a 500 with the cookies left in place,
	// rather than showing a logged-out UI over a session still live server-side.
	if cookie, err := r.Cookie(refreshCookie); err == nil {
		if err := s.auth.Logout(r.Context(), cookie.Value); err != nil {
			s.writeServiceError(w, r, err)
			return
		}
	}
	s.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *apiServer) GetCurrentUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	u, err := s.users.GetByID(r.Context(), userID)
	if errors.Is(err, user.ErrNotFound) {
		// A valid token for a since-deleted user: treat as unauthenticated.
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toUserProfile(u))
}

// toUserProfile maps a domain user to the wire DTO, dropping the password hash
// — the hash must never cross this boundary (security-baseline.md).
func toUserProfile(u user.User) api.UserProfile {
	return api.UserProfile{
		Id:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
	}
}
