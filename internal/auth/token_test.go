package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// validClaims are the claims a genuine Nabu access token carries; each
// rejection case below spoils exactly one of them.
func validClaims(userID uuid.UUID) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		Subject:   userID.String(),
		Issuer:    issuer,
		IssuedAt:  jwt.NewNumericDate(testNow),
		ExpiresAt: jwt.NewNumericDate(testNow.Add(accessTTL)),
	}
}

func signed(t *testing.T, method jwt.SigningMethod, key any, claims jwt.RegisteredClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("signing test token with %s: %v", method.Alg(), err)
	}
	return s
}

func TestHashToken(t *testing.T) {
	const plaintext = "an-opaque-refresh-token"
	got := hashToken(plaintext)

	want := sha256.Sum256([]byte(plaintext))
	if string(got) != string(want[:]) {
		t.Errorf("hashToken() = %x, want the SHA-256 %x", got, want)
	}
	if len(got) != sha256.Size {
		t.Errorf("hashToken() length = %d, want %d", len(got), sha256.Size)
	}
	if string(got) == plaintext {
		t.Error("hashToken() returned the plaintext")
	}
	// The at-rest form must be a pure function of the token: rotation and
	// logout both look a row up by re-hashing what the client presented.
	if string(hashToken(plaintext)) != string(got) {
		t.Error("hashToken() is not deterministic")
	}
	if string(hashToken(plaintext+"x")) == string(got) {
		t.Error("hashToken() collided on different inputs")
	}
}

func TestGenerateRefreshToken(t *testing.T) {
	plaintext, hash, err := generateRefreshToken()
	if err != nil {
		t.Fatalf("generateRefreshToken() error: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(plaintext)
	if err != nil {
		t.Fatalf("token %q is not base64url: %v", plaintext, err)
	}
	if len(raw) != refreshTokenBytes {
		t.Errorf("entropy = %d bytes, want %d (ADR-0003)", len(raw), refreshTokenBytes)
	}
	if string(hash) != string(hashToken(plaintext)) {
		t.Errorf("returned hash = %x, want the SHA-256 of the token %x", hash, hashToken(plaintext))
	}

	seen := map[string]bool{plaintext: true}
	for range 50 {
		p, _, err := generateRefreshToken()
		if err != nil {
			t.Fatalf("generateRefreshToken() error: %v", err)
		}
		if seen[p] {
			t.Fatalf("generateRefreshToken() repeated a token: %q", p)
		}
		seen[p] = true
	}
}

func TestVerifyAccessTokenRoundTrip(t *testing.T) {
	svc := newTestService(t, &fakeUsers{}, &fakeRefresh{})
	userID := uuid.New()

	token, expiry, err := svc.issueAccess(userID, testNow)
	if err != nil {
		t.Fatalf("issueAccess() error: %v", err)
	}
	if !expiry.Equal(testNow.Add(accessTTL)) {
		t.Errorf("expiry = %v, want %v (15-minute TTL per ADR-0003)", expiry, testNow.Add(accessTTL))
	}

	got, err := svc.VerifyAccessToken(token)
	if err != nil {
		t.Fatalf("VerifyAccessToken() on a freshly issued token: %v", err)
	}
	if got != userID {
		t.Errorf("subject = %v, want %v", got, userID)
	}

	// ADR-0003: no role claims, so a permission change applies on the next
	// request instead of at the end of the token's lifetime.
	var claims jwt.MapClaims
	if _, err := jwt.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) { return testSecret, nil }); err != nil {
		t.Fatalf("re-parsing the issued token: %v", err)
	}
	for _, unwanted := range []string{"role", "roles", "permissions", "scope"} {
		if _, ok := claims[unwanted]; ok {
			t.Errorf("access token carries a %q claim; RBAC must read live assignments from the database", unwanted)
		}
	}
}

// TestVerifyAccessTokenRejects is the security boundary of the stateless
// hot path: every token that is not one we minted, unexpired, for this
// issuer, must be refused.
func TestVerifyAccessTokenRejects(t *testing.T) {
	svc := newTestService(t, &fakeUsers{}, &fakeRefresh{})
	userID := uuid.New()

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

	tests := []struct {
		name  string
		token func(t *testing.T) string
	}{
		{
			name:  "empty string",
			token: func(*testing.T) string { return "" },
		},
		{
			name:  "garbage",
			token: func(*testing.T) string { return "not-a-jwt-at-all" },
		},
		{
			name:  "three garbage segments",
			token: func(*testing.T) string { return "aaaa.bbbb.cccc" },
		},
		{
			name: "expired",
			token: func(t *testing.T) string {
				// Minted by a clock 16 minutes in the past: exp is already
				// behind the wall clock the verifier uses.
				stale := newTestService(t, &fakeUsers{}, &fakeRefresh{})
				issuedAt := time.Now().Add(-accessTTL - time.Minute)
				token, _, err := stale.issueAccess(userID, issuedAt)
				if err != nil {
					t.Fatalf("issueAccess() error: %v", err)
				}
				return token
			},
		},
		{
			name: "signed with a different secret",
			token: func(t *testing.T) string {
				return signed(t, jwt.SigningMethodHS256, []byte("a-completely-different-32-byte-secret"), validClaims(userID))
			},
		},
		{
			name: "wrong issuer",
			token: func(t *testing.T) string {
				claims := validClaims(userID)
				claims.Issuer = "evil-idp"
				return signed(t, jwt.SigningMethodHS256, testSecret, claims)
			},
		},
		{
			name: "no issuer",
			token: func(t *testing.T) string {
				claims := validClaims(userID)
				claims.Issuer = ""
				return signed(t, jwt.SigningMethodHS256, testSecret, claims)
			},
		},
		// The three alg-confusion cases are not equally load-bearing. Verified
		// by parsing each without jwt.WithValidMethods: "alg none" and RS256
		// are refused anyway by the library's own none-guard and by the keyfunc
		// returning an HMAC key for an RSA verify. Only HS512 is accepted once
		// the method list is unpinned, so it is the case that would actually
		// fail if WithValidMethods were dropped. The other two are kept as
		// defense in depth: they would catch a future keyfunc that hands back a
		// public key or tolerates "none".
		{
			name: "alg none",
			token: func(t *testing.T) string {
				return signed(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, validClaims(userID))
			},
		},
		{
			name: "RS256",
			token: func(t *testing.T) string {
				return signed(t, jwt.SigningMethodRS256, rsaKey, validClaims(userID))
			},
		},
		{
			name: "HS512 with the real secret",
			token: func(t *testing.T) string {
				// Signs and verifies with our own key: the pinned method list is
				// the only thing standing between this token and acceptance.
				return signed(t, jwt.SigningMethodHS512, testSecret, validClaims(userID))
			},
		},
		{
			name: "no expiry claim",
			token: func(t *testing.T) string {
				claims := validClaims(userID)
				claims.ExpiresAt = nil
				return signed(t, jwt.SigningMethodHS256, testSecret, claims)
			},
		},
		{
			name: "subject is not a UUID",
			token: func(t *testing.T) string {
				claims := validClaims(userID)
				claims.Subject = "alice@example.com"
				return signed(t, jwt.SigningMethodHS256, testSecret, claims)
			},
		},
		{
			name: "empty subject",
			token: func(t *testing.T) string {
				claims := validClaims(userID)
				claims.Subject = ""
				return signed(t, jwt.SigningMethodHS256, testSecret, claims)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.VerifyAccessToken(tt.token(t))
			// Identity, not errors.Is: verification must not leak why a token
			// was rejected past the sentinel the middleware turns into a 401.
			if err != ErrInvalidToken {
				t.Fatalf("VerifyAccessToken() error = %v, want ErrInvalidToken", err)
			}
			if got != uuid.Nil {
				t.Errorf("VerifyAccessToken() = %v on a rejected token, want the zero UUID", got)
			}
		})
	}
}
