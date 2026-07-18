// Package audit records who did what to which entity. Writes are best-effort
// and non-transactional: a recorder failure degrades the trail, it never fails
// the operation being audited. See ADR-0004 for that trade-off and for the
// metadata redaction rule this package's callers must follow.
package audit

import (
	"context"

	"github.com/google/uuid"
)

// Entry is one auditable event.
type Entry struct {
	// ActorID is the user responsible, absent for events that happen without
	// an authenticated session (a failed login has no user).
	ActorID uuid.NullUUID
	// OrgID scopes the row. Always set: it is what every read of this table
	// filters on (ADR-0005).
	OrgID uuid.UUID
	// ProjectID is absent for org-level events such as login and logout.
	ProjectID uuid.NullUUID
	// Action is a dotted verb: "task.created", "auth.login_failed".
	Action     string
	EntityType string
	// EntityID is uuid.Nil when the event names no entity that exists.
	EntityID uuid.UUID

	// Metadata is deliberately map[string]any rather than any or a domain
	// struct. Domain structs carry fields that must never be persisted here —
	// user.User holds PasswordHash — and marshalling one whole would put bcrypt
	// hashes into an unindexed, unredacted, never-expiring column. Build this
	// with a per-entity allowlist function in the package that owns the entity.
	Metadata map[string]any
}

// Recorder writes audit entries. Record returns no error on purpose: audit is
// best-effort, and a caller offered an error would have to decide whether to
// fail a state change that already succeeded. Implementations log their own
// failures (ADR-0004).
type Recorder interface {
	Record(ctx context.Context, e Entry)
}
