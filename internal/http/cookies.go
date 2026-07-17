package http

import (
	"net/http"
	"time"

	"github.com/floatinginbits/nabu/internal/auth"
)

const (
	accessCookie  = "nabu_access"
	refreshCookie = "nabu_refresh"
	// refreshCookiePath scopes the refresh cookie so browsers send it only to
	// the auth endpoints, never on ordinary API calls (ADR-0003).
	refreshCookiePath = "/api/v1/auth"
	csrfHeader        = "X-Nabu-Csrf"
)

// setSessionCookies writes the access and refresh tokens as HTTP-only cookies
// (attributes per ADR-0003).
func (s *apiServer) setSessionCookies(w http.ResponseWriter, pair auth.TokenPair) {
	now := time.Now()
	http.SetCookie(w, &http.Cookie{
		Name:     accessCookie,
		Value:    pair.Access,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  pair.AccessExpiry,
		MaxAge:   int(pair.AccessExpiry.Sub(now).Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookie,
		Value:    pair.Refresh,
		Path:     refreshCookiePath,
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteStrictMode,
		Expires:  pair.RefreshExpiry,
		MaxAge:   int(pair.RefreshExpiry.Sub(now).Seconds()),
	})
}

// clearSessionCookies expires both cookies, matching the Path each was set with
// so the browser actually removes them.
func (s *apiServer) clearSessionCookies(w http.ResponseWriter) {
	for _, c := range []struct {
		name string
		path string
		same http.SameSite
	}{
		{accessCookie, "/", http.SameSiteLaxMode},
		{refreshCookie, refreshCookiePath, http.SameSiteStrictMode},
	} {
		http.SetCookie(w, &http.Cookie{
			Name:     c.name,
			Value:    "",
			Path:     c.path,
			HttpOnly: true,
			Secure:   s.cookieSecure,
			SameSite: c.same,
			MaxAge:   -1,
		})
	}
}
