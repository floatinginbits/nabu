package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/floatinginbits/nabu/internal/audit/sqlcgen"
)

// maxMetadataBytes caps the encoded metadata document. Without a cap, a single
// oversized field (a pasted 2 MB description) makes every write a large JSONB
// write, and since Record swallows its errors, an insert that fails on size
// would yield no audit row and a successful response — an audit-suppression
// path an attacker can trigger deliberately.
const maxMetadataBytes = 8 << 10

// writeTimeout bounds the insert. Record is synchronous and runs before the
// response, so this is the ceiling on the request latency a stalled audit write
// can add — and, because the write ignores caller cancellation, the ceiling on
// how long it holds a pool connection after the client has hung up.
const writeTimeout = 5 * time.Second

// PostgresRecorder writes audit entries to Postgres, outside any transaction
// the audited operation may be running in (ADR-0004).
type PostgresRecorder struct {
	q   *sqlcgen.Queries
	log *slog.Logger
}

func NewPostgresRecorder(pool *pgxpool.Pool, log *slog.Logger) *PostgresRecorder {
	return &PostgresRecorder{q: sqlcgen.New(pool), log: log}
}

func (r *PostgresRecorder) Record(ctx context.Context, e Entry) {
	// The audit row describes work that already committed, so it must not be
	// abandoned because the client hung up; the timeout keeps a write the caller
	// no longer waits on from running unbounded.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	metadata := encodeMetadata(e.Metadata)
	err := r.q.InsertAuditLog(ctx, sqlcgen.InsertAuditLogParams{
		ActorID:    e.ActorID,
		OrgID:      e.OrgID,
		ProjectID:  e.ProjectID,
		Action:     e.Action,
		EntityType: e.EntityType,
		EntityID:   uuid.NullUUID{UUID: e.EntityID, Valid: e.EntityID != uuid.Nil},
		Metadata:   metadata,
	})
	if err != nil {
		// Deliberately without metadata: logging it would undo the redaction
		// rule in a pipeline that leaves the host (ADR-0004). The identifiers
		// below are enough to find the operation in the request log.
		r.log.ErrorContext(ctx, "recording audit entry",
			slog.String("action", e.Action),
			slog.String("entity_type", e.EntityType),
			slog.String("entity_id", e.EntityID.String()),
			slog.String("actor_id", nullUUIDString(e.ActorID)),
			slog.Any("error", err),
		)
	}
}

// truncatedMetadata replaces a document that exceeds the cap. The row is still
// written: knowing an action happened without its detail beats not knowing.
var truncatedMetadata = []byte(`{"_truncated": true}`)

// unencodableMetadata replaces a document json.Marshal rejects (a channel or a
// cyclic value reached through an `any`), which is a caller bug rather than a
// runtime condition — but not one worth losing the audit row over.
var unencodableMetadata = []byte(`{"_unencodable": true}`)

// escapedNUL is how json.Marshal encodes a NUL inside a string. Postgres jsonb
// rejects that escape outright, so leaving one in place would fail the insert —
// and because Record swallows its errors, that failure is a missing audit row
// behind a successful response. Dropping the character keeps the row.
var escapedNUL = []byte(`\u0000`)

func encodeMetadata(m map[string]any) []byte {
	if len(m) == 0 {
		return []byte(`{}`)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return unencodableMetadata
	}
	b = bytes.ReplaceAll(b, escapedNUL, nil)
	if len(b) > maxMetadataBytes {
		return truncatedMetadata
	}
	return b
}

func nullUUIDString(id uuid.NullUUID) string {
	if !id.Valid {
		return ""
	}
	return id.UUID.String()
}
