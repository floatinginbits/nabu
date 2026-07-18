// Package actor carries the authenticated session's identity and scope through
// the request context. It is a leaf package — context plus uuid, nothing else —
// so that auth, audit, and rbac can all read the actor without importing each
// other and forming a cycle.
package actor

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNoActor means a scoped operation ran without a session actor in the
// context. Every scoped route sits behind requireAuth, so this is a wiring
// bug, not a client error — it surfaces as a 500, never as an unscoped query.
// It lives here, in the leaf package, so every domain service reports the same
// sentinel instead of defining its own.
var ErrNoActor = errors.New("no actor in context")

// Actor is the session-derived identity every scoped query and permission check
// resolves against. OrgID comes from the server's own resolution of the session,
// never from client input (security-baseline.md).
type Actor struct {
	UserID uuid.UUID
	OrgID  uuid.UUID
}

// A package-private struct type cannot collide with a key defined in any other
// package, which an exported or primitive-typed key could.
type ctxKey struct{}

// NewContext returns ctx carrying a.
func NewContext(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

// FromContext returns the actor set by the auth middleware. The bool is false
// on public routes, which never run the auth check.
func FromContext(ctx context.Context) (Actor, bool) {
	a, ok := ctx.Value(ctxKey{}).(Actor)
	return a, ok
}
