package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/http/api"
	"github.com/floatinginbits/nabu/internal/user"
)

// mintAccessToken signs an access token the real auth.Service accepts, with an
// arbitrary expiry and signing key. The claim shape is the wire contract fixed
// by ADR-0003 (HS256, sub/iat/exp/iss), and minting one directly is the only
// way to produce an expired or wrongly-signed token without waiting out the
// 15-minute TTL or reaching into auth's unexported clock.
func mintAccessToken(t *testing.T, sub uuid.UUID, expiry time.Time, secret string) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   sub.String(),
		Issuer:    "nabu",
		IssuedAt:  jwt.NewNumericDate(expiry.Add(-accessTokenTTL)),
		ExpiresAt: jwt.NewNumericDate(expiry),
	}).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("minting access token: %v", err)
	}
	return signed
}

// accessTokenTTL mirrors ADR-0003's 15-minute access-token lifetime; it only
// backdates iat on minted tokens, so drifting from the real constant would not
// break these tests.
const accessTokenTTL = 15 * time.Minute

// authedRequest builds what the real client sends on every API call: a valid
// access cookie plus the CSRF header.
func authedRequest(t *testing.T, method, path string, body io.Reader) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	req.AddCookie(&http.Cookie{
		Name:  accessCookie,
		Value: mintAccessToken(t, uuid.New(), time.Now().Add(time.Hour), testAuthSecret),
	})
	req.Header.Set(csrfHeader, "1")
	return req
}

// cookieByName returns the named cookie from the response, failing if absent.
func cookieByName(t *testing.T, w *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s cookie in response (Set-Cookie: %v)", name, w.Result().Header.Values("Set-Cookie"))
	return nil
}

func findCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// postJSON sends a POST with the CSRF header and the given cookies — the shape
// of every state-changing auth call.
func postJSON(t *testing.T, ts *testServer, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, "1")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return do(ts, req)
}

func loginBody(email, password string) string {
	b, _ := json.Marshal(map[string]string{"email": email, "password": password})
	return string(b)
}

// loginVia performs a real login through the handler and returns both session
// cookies as the browser would resend them (name and value only).
func loginVia(t *testing.T, ts *testServer) (access, refresh *http.Cookie) {
	t.Helper()
	w := postJSON(t, ts, "/api/v1/auth/login", loginBody(testUserEmail, testUserPassword))
	if w.Code != http.StatusOK {
		t.Fatalf("login: status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	a := cookieByName(t, w, accessCookie)
	r := cookieByName(t, w, refreshCookie)
	return &http.Cookie{Name: a.Name, Value: a.Value}, &http.Cookie{Name: r.Name, Value: r.Value}
}

func TestLoginEndpoint(t *testing.T) {
	ts := newTestServer(t, false)

	t.Run("valid credentials return the user profile", func(t *testing.T) {
		w := postJSON(t, ts, "/api/v1/auth/login", loginBody(testUserEmail, testUserPassword))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
		var profile api.UserProfile
		if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil {
			t.Fatalf("decoding profile: %v", err)
		}
		if profile.Id != testUserID {
			t.Errorf("id = %s, want %s", profile.Id, testUserID)
		}
		if profile.Email != testUserEmail {
			t.Errorf("email = %q, want %q", profile.Email, testUserEmail)
		}
		if profile.DisplayName != testUserDisplayName {
			t.Errorf("displayName = %q, want %q", profile.DisplayName, testUserDisplayName)
		}
	})

	// security-baseline.md: the password hash must never cross the API
	// boundary, and neither must the credential the caller just sent.
	t.Run("response never carries the password hash", func(t *testing.T) {
		u, err := user.NewService(user.NewPostgresRepository(testPool)).GetByID(context.Background(), testUserID)
		if err != nil {
			t.Fatalf("loading seeded user: %v", err)
		}
		w := postJSON(t, ts, "/api/v1/auth/login", loginBody(testUserEmail, testUserPassword))
		body := w.Body.String()
		if strings.Contains(body, u.PasswordHash) {
			t.Error("login response contains the bcrypt password hash")
		}
		if strings.Contains(body, testUserPassword) {
			t.Error("login response echoes the plaintext password")
		}
		// Belt and braces: pin the exact key set, so a field added to the DTO
		// has to be considered here rather than leaking silently.
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil {
			t.Fatalf("decoding body: %v", err)
		}
		for _, k := range []string{"id", "email", "displayName"} {
			if _, ok := fields[k]; !ok {
				t.Errorf("profile is missing key %q", k)
			}
			delete(fields, k)
		}
		for k := range fields {
			t.Errorf("unexpected key %q in login response", k)
		}
	})

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{"wrong password", loginBody(testUserEmail, "wrong-password-entirely"), http.StatusUnauthorized, "UNAUTHORIZED"},
		{"unknown email", loginBody("nobody@nabu.test", testUserPassword), http.StatusUnauthorized, "UNAUTHORIZED"},
		{"malformed json", `{"email":`, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"empty body", ``, http.StatusBadRequest, "VALIDATION_ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := postJSON(t, ts, "/api/v1/auth/login", tt.body)
			assertErrorCode(t, w, tt.wantStatus, tt.wantCode)
			if c := findCookie(w, accessCookie); c != nil {
				t.Errorf("failed login set an access cookie: %v", c)
			}
		})
	}

	// No account enumeration: a wrong password and an unknown address must be
	// indistinguishable to the caller, byte for byte (auth.ErrInvalidCredentials).
	t.Run("unknown email is indistinguishable from wrong password", func(t *testing.T) {
		wrongPw := postJSON(t, ts, "/api/v1/auth/login", loginBody(testUserEmail, "wrong-password-entirely"))
		unknown := postJSON(t, ts, "/api/v1/auth/login", loginBody("nobody@nabu.test", testUserPassword))

		if wrongPw.Code != unknown.Code {
			t.Errorf("status: wrong password = %d, unknown email = %d", wrongPw.Code, unknown.Code)
		}
		if wrongPw.Body.String() != unknown.Body.String() {
			t.Errorf("bodies differ:\n wrong password: %s\n unknown email:  %s", wrongPw.Body, unknown.Body)
		}
	})
}

// Cookie attributes are security-load-bearing (ADR-0003): SameSite and Path on
// the refresh cookie are what keep it off ordinary API calls, and HttpOnly is
// what keeps both tokens away from JS.
func TestLoginCookieAttributes(t *testing.T) {
	for _, cookieSecure := range []bool{true, false} {
		name := "cookieSecure=false"
		if cookieSecure {
			name = "cookieSecure=true"
		}
		t.Run(name, func(t *testing.T) {
			ts := newTestServer(t, cookieSecure)
			w := postJSON(t, ts, "/api/v1/auth/login", loginBody(testUserEmail, testUserPassword))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
			}

			tests := []struct {
				cookie       string
				wantPath     string
				wantSameSite http.SameSite
			}{
				{accessCookie, "/", http.SameSiteLaxMode},
				{refreshCookie, refreshCookiePath, http.SameSiteStrictMode},
			}
			for _, tt := range tests {
				t.Run(tt.cookie, func(t *testing.T) {
					c := cookieByName(t, w, tt.cookie)
					if c.Value == "" {
						t.Error("cookie value is empty")
					}
					if !c.HttpOnly {
						t.Error("HttpOnly = false, want true")
					}
					if c.Path != tt.wantPath {
						t.Errorf("Path = %q, want %q", c.Path, tt.wantPath)
					}
					if c.SameSite != tt.wantSameSite {
						t.Errorf("SameSite = %v, want %v", c.SameSite, tt.wantSameSite)
					}
					if c.Secure != cookieSecure {
						t.Errorf("Secure = %v, want %v (Deps.CookieSecure)", c.Secure, cookieSecure)
					}
					if c.MaxAge <= 0 {
						t.Errorf("MaxAge = %d, want a positive lifetime", c.MaxAge)
					}
				})
			}
		})
	}
}

func TestRefreshEndpoint(t *testing.T) {
	ts := newTestServer(t, false)

	t.Run("no cookie", func(t *testing.T) {
		w := postJSON(t, ts, "/api/v1/auth/refresh", "")
		assertErrorCode(t, w, http.StatusUnauthorized, "UNAUTHORIZED")
	})

	t.Run("valid cookie rotates both tokens", func(t *testing.T) {
		_, refresh := loginVia(t, ts)

		w := postJSON(t, ts, "/api/v1/auth/refresh", "", refresh)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body: %s)", w.Code, w.Body.String())
		}
		// Both cookies must be reissued: cookieByName fails if either is
		// missing from the response. The access token's *value* is not compared
		// against the old one — JWT iat/exp have second granularity, so a
		// refresh in the same second as the login legitimately re-mints an
		// identical token, and asserting otherwise would be flaky.
		newAccess := cookieByName(t, w, accessCookie)
		newRefresh := cookieByName(t, w, refreshCookie)

		// The rotation invariant: the presented refresh token is spent, and a
		// different one comes back (ADR-0003).
		if newRefresh.Value == refresh.Value {
			t.Error("refresh token was not rotated; the same value came back")
		}
		if newRefresh.Value == "" || newAccess.Value == "" {
			t.Error("reissued cookies must carry values")
		}
		// The rotated-to token must itself work.
		again := postJSON(t, ts, "/api/v1/auth/refresh", "",
			&http.Cookie{Name: refreshCookie, Value: newRefresh.Value})
		if again.Code != http.StatusNoContent {
			t.Errorf("refresh with the rotated token: status = %d, want 204", again.Code)
		}
	})

	t.Run("garbage cookie is rejected and cookies cleared", func(t *testing.T) {
		w := postJSON(t, ts, "/api/v1/auth/refresh", "",
			&http.Cookie{Name: refreshCookie, Value: "not-a-real-token"})
		assertErrorCode(t, w, http.StatusUnauthorized, "UNAUTHORIZED")
		assertSessionCleared(t, w)
	})

	// Reuse of an already-rotated token past the grace window is the
	// stolen-cookie signal: the family is revoked and the client is logged out.
	t.Run("reused token past the grace window is rejected", func(t *testing.T) {
		_, refresh := loginVia(t, ts)

		first := postJSON(t, ts, "/api/v1/auth/refresh", "", refresh)
		if first.Code != http.StatusNoContent {
			t.Fatalf("first refresh: status = %d, want 204", first.Code)
		}
		ageOutGraceWindow(t)

		reused := postJSON(t, ts, "/api/v1/auth/refresh", "", refresh)
		assertErrorCode(t, reused, http.StatusUnauthorized, "UNAUTHORIZED")
		assertSessionCleared(t, reused)

		// Reuse revokes the whole family, so the successor dies with it.
		successor := cookieByName(t, first, refreshCookie)
		after := postJSON(t, ts, "/api/v1/auth/refresh", "",
			&http.Cookie{Name: refreshCookie, Value: successor.Value})
		if after.Code != http.StatusUnauthorized {
			t.Errorf("successor after family revocation: status = %d, want 401", after.Code)
		}
	})

	// Two tabs refreshing at once present the same token twice; inside the
	// grace window that must not trip reuse detection (ADR-0003).
	t.Run("concurrent refresh inside the grace window succeeds", func(t *testing.T) {
		_, refresh := loginVia(t, ts)

		first := postJSON(t, ts, "/api/v1/auth/refresh", "", refresh)
		if first.Code != http.StatusNoContent {
			t.Fatalf("first refresh: status = %d, want 204", first.Code)
		}
		second := postJSON(t, ts, "/api/v1/auth/refresh", "", refresh)
		if second.Code != http.StatusNoContent {
			t.Fatalf("second refresh inside grace: status = %d, want 204 (body: %s)", second.Code, second.Body.String())
		}
		a := cookieByName(t, first, refreshCookie)
		b := cookieByName(t, second, refreshCookie)
		if a.Value == b.Value {
			t.Error("concurrent refreshes returned the same token; each caller needs its own")
		}
	})
}

func TestLogoutEndpoint(t *testing.T) {
	ts := newTestServer(t, false)

	t.Run("revokes the session and clears cookies", func(t *testing.T) {
		_, refresh := loginVia(t, ts)

		w := postJSON(t, ts, "/api/v1/auth/logout", "", refresh)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body: %s)", w.Code, w.Body.String())
		}
		assertSessionCleared(t, w)

		// The point of an opaque, server-side refresh token: after logout it is
		// genuinely dead, not merely forgotten by the client.
		after := postJSON(t, ts, "/api/v1/auth/refresh", "", refresh)
		assertErrorCode(t, after, http.StatusUnauthorized, "UNAUTHORIZED")
	})

	t.Run("is idempotent without a session", func(t *testing.T) {
		w := postJSON(t, ts, "/api/v1/auth/logout", "")
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body: %s)", w.Code, w.Body.String())
		}
	})

	t.Run("tolerates an unknown refresh token", func(t *testing.T) {
		w := postJSON(t, ts, "/api/v1/auth/logout", "",
			&http.Cookie{Name: refreshCookie, Value: "not-a-real-token"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body: %s)", w.Code, w.Body.String())
		}
	})
}

func TestGetCurrentUser(t *testing.T) {
	ts := newTestServer(t, false)
	access, _ := loginVia(t, ts)

	t.Run("valid session returns the authenticated user", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
		req.AddCookie(access)
		w := do(ts, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
		}
		var profile api.UserProfile
		if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil {
			t.Fatalf("decoding profile: %v", err)
		}
		if profile.Id != testUserID || profile.Email != testUserEmail {
			t.Errorf("profile = %+v, want the seeded user %s", profile, testUserID)
		}
	})

	tests := []struct {
		name   string
		cookie *http.Cookie
	}{
		{"no cookie", nil},
		{"garbage token", &http.Cookie{Name: accessCookie, Value: "not-a-jwt"}},
		{"expired token", &http.Cookie{
			Name:  accessCookie,
			Value: mintAccessToken(t, testUserID, time.Now().Add(-time.Minute), testAuthSecret),
		}},
		// A token signed with the wrong key must not pass: the signature check
		// is the whole basis of stateless verification.
		{"wrong signing secret", &http.Cookie{
			Name:  accessCookie,
			Value: mintAccessToken(t, testUserID, time.Now().Add(time.Hour), "an-entirely-different-signing-key!!"),
		}},
		// A well-formed token for an account that does not exist.
		{"unknown user", &http.Cookie{
			Name:  accessCookie,
			Value: mintAccessToken(t, uuid.New(), time.Now().Add(time.Hour), testAuthSecret),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}
			assertErrorCode(t, do(ts, req), http.StatusUnauthorized, "UNAUTHORIZED")
		})
	}
}

// Refresh must work precisely when the access token is dead — that is why it is
// a public route (isPublic in middleware.go) rather than one behind requireAuth.
func TestRefreshWorksWithADeadAccessToken(t *testing.T) {
	ts := newTestServer(t, false)
	_, refresh := loginVia(t, ts)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.Header.Set(csrfHeader, "1")
	req.AddCookie(&http.Cookie{
		Name:  accessCookie,
		Value: mintAccessToken(t, testUserID, time.Now().Add(-time.Hour), testAuthSecret),
	})
	req.AddCookie(refresh)

	w := do(ts, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 — an expired access cookie must not block refresh", w.Code)
	}
}

// assertSessionCleared checks both cookies were expired with the Path each was
// set with; a mismatched Path leaves the cookie in the browser.
func assertSessionCleared(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	tests := []struct {
		name     string
		wantPath string
	}{
		{accessCookie, "/"},
		{refreshCookie, refreshCookiePath},
	}
	for _, tt := range tests {
		c := cookieByName(t, w, tt.name)
		if c.MaxAge >= 0 {
			t.Errorf("%s: MaxAge = %d, want < 0 to expire the cookie", tt.name, c.MaxAge)
		}
		if c.Value != "" {
			t.Errorf("%s: value = %q, want empty", tt.name, c.Value)
		}
		if c.Path != tt.wantPath {
			t.Errorf("%s: Path = %q, want %q so the browser drops the right cookie", tt.name, c.Path, tt.wantPath)
		}
	}
}

// ageOutGraceWindow backdates every rotated token's replaced_at beyond the
// service's grace window. The window is a fixed 10s constant in auth and the
// service clock is not injectable from here, so reaching into the row is the
// only way to test reuse detection without a 10-second sleep.
//
// The backdate is deliberately enormous rather than "just past 10s": if it
// merely cleared the current window, widening graceWindow would make the reuse
// test pass by never reaching the reuse branch at all — a test that stops
// testing anything. Only replaced_at moves, so expires_at still sits in the
// future and the token stays otherwise live.
//
// It rewrites every rotated row, not one session's, so it relies on this
// package's tests running sequentially — adding t.Parallel to a refresh test
// would need this scoped to a family first.
func ageOutGraceWindow(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		"UPDATE refresh_tokens SET replaced_at = replaced_at - interval '365 days' WHERE replaced_at IS NOT NULL")
	if err != nil {
		t.Fatalf("ageing out the grace window: %v", err)
	}
}
