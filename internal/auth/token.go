package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	// Lifetimes are fixed by ADR-0003 rather than configurable: the access
	// token is short enough that non-revocability is acceptable, the refresh
	// token long enough for a sliding month-long session.
	accessTTL  = 15 * time.Minute
	refreshTTL = 30 * 24 * time.Hour
	// graceWindow is how long a just-rotated token still yields a fresh token
	// instead of tripping reuse detection, absorbing the two-tabs race.
	graceWindow = 10 * time.Second

	issuer            = "nabu"
	refreshTokenBytes = 32
)

// hashToken is the at-rest form of a refresh token. A 256-bit random value has
// no low-entropy preimage to protect, so a fast hash (not bcrypt) is correct.
func hashToken(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

// generateRefreshToken returns a new opaque token and its stored hash.
func generateRefreshToken() (plaintext string, hash []byte, err error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("generating refresh token: %w", err)
	}
	plaintext = base64.RawURLEncoding.EncodeToString(b)
	return plaintext, hashToken(plaintext), nil
}

// issueAccess mints a signed access token for the user and returns it with its
// expiry (the HTTP layer sets the cookie Max-Age from it).
func (s *Service) issueAccess(userID uuid.UUID, now time.Time) (string, time.Time, error) {
	exp := now.Add(accessTTL)
	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		Issuer:    issuer,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(exp),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.signing)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing access token: %w", err)
	}
	return signed, exp, nil
}

// VerifyAccessToken validates a token's signature, algorithm, issuer, and
// expiry and returns the subject. WithValidMethods pins HS256, closing the
// alg-confusion / "alg: none" attacks.
func (s *Service) VerifyAccessToken(tokenString string) (uuid.UUID, error) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(tokenString, &claims,
		func(*jwt.Token) (any, error) { return s.signing, nil },
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}
	return id, nil
}
