package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/floatinginbits/nabu/internal/testdb"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	testdb.Main(m, &testPool)
}

// fixture is the org, project, and user an audit row can legally reference.
type fixture struct {
	orgID     uuid.UUID
	projectID uuid.UUID
	userID    uuid.UUID
}

func reset(t *testing.T) fixture {
	t.Helper()
	testdb.SkipIfShort(t)
	ctx := context.Background()
	testdb.Truncate(ctx, t, testPool, "audit_logs")

	var f fixture
	row := testPool.QueryRow(ctx, "SELECT id, org_id FROM projects WHERE lower(key) = 'gen'")
	if err := row.Scan(&f.projectID, &f.orgID); err != nil {
		t.Fatalf("reading seeded project: %v", err)
	}
	err := testPool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash)
		 VALUES ($1, 'Audit Actor', 'x') RETURNING id`,
		"audit-"+uuid.NewString()+"@example.com").Scan(&f.userID)
	if err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	return f
}

type storedRow struct {
	actorID    uuid.NullUUID
	orgID      uuid.UUID
	projectID  uuid.NullUUID
	action     string
	entityType string
	entityID   uuid.NullUUID
	metadata   []byte
}

func onlyRow(t *testing.T) storedRow {
	t.Helper()
	var r storedRow
	err := testPool.QueryRow(context.Background(),
		`SELECT actor_id, org_id, project_id, action, entity_type, entity_id, metadata FROM audit_logs`).
		Scan(&r.actorID, &r.orgID, &r.projectID, &r.action, &r.entityType, &r.entityID, &r.metadata)
	if err != nil {
		t.Fatalf("reading the audit row: %v", err)
	}
	return r
}

func TestPostgresRecorderWritesEntry(t *testing.T) {
	f := reset(t)
	entityID := uuid.New()
	rec := NewPostgresRecorder(testPool, slog.New(&logCapture{}))

	rec.Record(context.Background(), Entry{
		ActorID:    uuid.NullUUID{UUID: f.userID, Valid: true},
		OrgID:      f.orgID,
		ProjectID:  uuid.NullUUID{UUID: f.projectID, Valid: true},
		Action:     "task.created",
		EntityType: "task",
		EntityID:   entityID,
		Metadata:   map[string]any{"title": "a task"},
	})

	got := onlyRow(t)
	if got.actorID.UUID != f.userID || got.orgID != f.orgID || got.projectID.UUID != f.projectID {
		t.Errorf("scope = actor %v org %v project %v, want %v/%v/%v",
			got.actorID.UUID, got.orgID, got.projectID.UUID, f.userID, f.orgID, f.projectID)
	}
	if got.action != "task.created" || got.entityType != "task" || got.entityID.UUID != entityID {
		t.Errorf("event = %s/%s/%v, want task.created/task/%v", got.action, got.entityType, got.entityID.UUID, entityID)
	}
	var metadata map[string]any
	if err := json.Unmarshal(got.metadata, &metadata); err != nil {
		t.Fatalf("stored metadata is not JSON: %v", err)
	}
	if metadata["title"] != "a task" {
		t.Errorf("stored metadata = %v, want the title", metadata)
	}
}

// actor_id and entity_id are both nullable — the first so a row outlives the
// user it names, the second for an event that names no entity — so an entry
// supplying neither must still be written.
func TestPostgresRecorderWritesActorlessEntry(t *testing.T) {
	f := reset(t)
	rec := NewPostgresRecorder(testPool, slog.New(&logCapture{}))

	rec.Record(context.Background(), Entry{
		OrgID:      f.orgID,
		Action:     "auth.logout",
		EntityType: "user",
	})

	got := onlyRow(t)
	if got.actorID.Valid {
		t.Errorf("actor_id = %v, want NULL", got.actorID.UUID)
	}
	if got.entityID.Valid {
		t.Errorf("entity_id = %v, want NULL for uuid.Nil", got.entityID.UUID)
	}
	if string(got.metadata) != "{}" {
		t.Errorf("metadata = %s, want {}", got.metadata)
	}
}

// A request whose client disconnected still gets its audit row: the work it
// describes already happened.
func TestPostgresRecorderIgnoresCallerCancellation(t *testing.T) {
	f := reset(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	NewPostgresRecorder(testPool, slog.New(&logCapture{})).Record(ctx, Entry{
		OrgID:      f.orgID,
		Action:     "task.created",
		EntityType: "task",
		EntityID:   uuid.New(),
	})

	if got := onlyRow(t); got.action != "task.created" {
		t.Errorf("action = %q, want the row written despite the canceled context", got.action)
	}
}

// A failing insert must not panic or block, and must be logged without the
// metadata that the redaction rule keeps out of the log pipeline (ADR-0004).
func TestPostgresRecorderLogsFailureWithoutMetadata(t *testing.T) {
	reset(t)
	logs := &logCapture{}
	entityID := uuid.New()

	// An org that does not exist: a real foreign-key violation rather than a
	// stubbed error.
	NewPostgresRecorder(testPool, slog.New(logs)).Record(context.Background(), Entry{
		OrgID:      uuid.New(),
		Action:     "task.created",
		EntityType: "task",
		EntityID:   entityID,
		Metadata:   map[string]any{"title": "must not be logged"},
	})

	rec, ok := logs.find("recording audit entry")
	if !ok {
		t.Fatal("a failed audit insert was not logged")
	}
	if _, present := rec["metadata"]; present {
		t.Errorf("failure log carries metadata: %v", rec)
	}
	for key, value := range rec {
		if s, isString := value.(string); isString && strings.Contains(s, "must not be logged") {
			t.Errorf("failure log attribute %q echoes the metadata: %v", key, value)
		}
	}
	if rec["action"] != "task.created" || rec["entity_id"] != entityID.String() {
		t.Errorf("failure log = %v, want the action and entity that failed", rec)
	}
}

func TestEncodeMetadata(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"empty", nil, `{}`},
		{"encodes values", map[string]any{"title": "x"}, `{"title":"x"}`},
		{
			// Truncated to a marker rather than dropped: swallowing an
			// oversize failure would mean no audit row and a 200 response, an
			// audit-suppression path a caller can trigger on purpose.
			name: "over the cap truncates to a marker",
			in:   map[string]any{"title": strings.Repeat("x", maxMetadataBytes+1)},
			want: string(truncatedMetadata),
		},
		{
			name: "unencodable value falls back to a marker",
			in:   map[string]any{"ch": make(chan int)},
			want: string(unencodableMetadata),
		},
		{
			// json.Marshal emits an escaped NUL happily and jsonb refuses it, so an
			// unscrubbed NUL is a swallowed insert failure — a 200 with no row.
			name: "NUL is scrubbed",
			in:   map[string]any{"title": "a\x00b"},
			want: `{"title":"ab"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(encodeMetadata(tt.in)); got != tt.want {
				t.Errorf("encodeMetadata() = %s, want %s", got, tt.want)
			}
		})
	}
}

// The truncation marker must survive the round trip into a jsonb column, or the
// cap trades a large write for a failed one.
func TestPostgresRecorderStoresTruncatedMarker(t *testing.T) {
	f := reset(t)

	NewPostgresRecorder(testPool, slog.New(&logCapture{})).Record(context.Background(), Entry{
		OrgID:      f.orgID,
		Action:     "task.created",
		EntityType: "task",
		EntityID:   uuid.New(),
		Metadata:   map[string]any{"title": strings.Repeat("x", maxMetadataBytes+1)},
	})

	var stored map[string]any
	if err := json.Unmarshal(onlyRow(t).metadata, &stored); err != nil {
		t.Fatalf("stored metadata is not JSON: %v", err)
	}
	if stored["_truncated"] != true {
		t.Errorf("stored metadata = %v, want the truncation marker", stored)
	}
}

// Metadata carrying a NUL must still land a row. Postgres rejects the escape
// inside jsonb, and Record swallows the error, so an unscrubbed NUL means a
// successful response with nothing audited.
func TestPostgresRecorderStoresMetadataWithNUL(t *testing.T) {
	f := reset(t)

	NewPostgresRecorder(testPool, slog.New(&logCapture{})).Record(context.Background(), Entry{
		OrgID:      f.orgID,
		Action:     "task.created",
		EntityType: "task",
		EntityID:   uuid.New(),
		Metadata:   map[string]any{"title": "before\x00after"},
	})

	var stored map[string]any
	if err := json.Unmarshal(onlyRow(t).metadata, &stored); err != nil {
		t.Fatalf("stored metadata is not JSON: %v", err)
	}
	if stored["title"] != "beforeafter" {
		t.Errorf("stored metadata = %v, want the title with the NUL removed", stored)
	}
}

type logCapture struct {
	mu      sync.Mutex
	records []map[string]any
}

func (l *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (l *logCapture) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]any{"msg": r.Message}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, attrs)
	return nil
}

func (l *logCapture) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logCapture) WithGroup(string) slog.Handler      { return l }

func (l *logCapture) find(msg string) (map[string]any, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.records {
		if r["msg"] == msg {
			return r, true
		}
	}
	return nil, false
}
