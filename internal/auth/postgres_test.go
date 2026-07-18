package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/floatinginbits/nabu/internal/testdb"
	"github.com/floatinginbits/nabu/internal/user"
)

// Integration tests for the rotation state machine against real Postgres
// (testing-strategy.md): the FOR UPDATE lock and the grace window only exist
// at the database level, so a fake would prove nothing here.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	testdb.Main(m, &testPool)
}

// requireDB skips under -short. Tests share one database without truncation,
// so every test seeds its own user and family and asserts family-scoped.
func requireDB(t *testing.T) {
	t.Helper()
	testdb.SkipIfShort(t)
}

// baseTime is the rotation clock the tests inject. It has no sub-microsecond
// component on purpose: Postgres truncates timestamptz to microseconds, and
// the grace-window boundary is compared against the stored replaced_at.
var baseTime = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

// storedToken is the raw row, read back independently of the repository so
// assertions test the database state rather than the mapping code.
type storedToken struct {
	id         uuid.UUID
	familyID   uuid.UUID
	expiresAt  time.Time
	revokedAt  *time.Time
	replacedAt *time.Time
	replacedBy *uuid.UUID
}

const rowSelect = `SELECT id, family_id, expires_at, revoked_at, replaced_by, replaced_at FROM refresh_tokens`

func scanToken(row pgx.Row) (storedToken, error) {
	var (
		st         storedToken
		revoked    pgtype.Timestamptz
		replacedAt pgtype.Timestamptz
		replacedBy uuid.NullUUID
	)
	if err := row.Scan(&st.id, &st.familyID, &st.expiresAt, &revoked, &replacedBy, &replacedAt); err != nil {
		return storedToken{}, err
	}
	if revoked.Valid {
		v := revoked.Time
		st.revokedAt = &v
	}
	if replacedAt.Valid {
		v := replacedAt.Time
		st.replacedAt = &v
	}
	if replacedBy.Valid {
		v := replacedBy.UUID
		st.replacedBy = &v
	}
	return st, nil
}

func rowByHash(ctx context.Context, t *testing.T, hash []byte) (storedToken, bool) {
	t.Helper()
	st, err := scanToken(testPool.QueryRow(ctx, rowSelect+" WHERE token_hash = $1", hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return storedToken{}, false
	}
	if err != nil {
		t.Fatalf("reading refresh row by hash: %v", err)
	}
	return st, true
}

func familyRows(ctx context.Context, t *testing.T, familyID uuid.UUID) []storedToken {
	t.Helper()
	rows, err := testPool.Query(ctx, rowSelect+" WHERE family_id = $1 ORDER BY created_at, id", familyID)
	if err != nil {
		t.Fatalf("querying family %s: %v", familyID, err)
	}
	defer rows.Close()

	var out []storedToken
	for rows.Next() {
		st, err := scanToken(rows)
		if err != nil {
			t.Fatalf("scanning family row: %v", err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating family %s: %v", familyID, err)
	}
	return out
}

func newTestUserID(ctx context.Context, t *testing.T) uuid.UUID {
	t.Helper()
	u, err := user.NewPostgresRepository(testPool).Create(ctx,
		fmt.Sprintf("refresh-%s@example.com", uuid.NewString()), "Refresh Test", "x-test-hash")
	if err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	return u.ID
}

// newHash mints a refresh token the way the service does and returns its
// stored form; the plaintext is irrelevant to the repository.
func newHash(t *testing.T) []byte {
	t.Helper()
	_, hash, err := generateRefreshToken()
	if err != nil {
		t.Fatalf("generating refresh token: %v", err)
	}
	return hash
}

// seedFamily starts a session for a fresh user: one login token, one family.
func seedFamily(ctx context.Context, t *testing.T, repo *PostgresRefreshRepository, expiresAt time.Time) ([]byte, RefreshToken) {
	t.Helper()
	hash := newHash(t)
	row, err := repo.Create(ctx, uuid.New(), newTestUserID(ctx, t), hash, expiresAt)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	return hash, row
}

func TestPostgresCreate(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	repo := NewPostgresRefreshRepository(testPool)

	familyID := uuid.New()
	userID := newTestUserID(ctx, t)
	hash := newHash(t)
	expiresAt := baseTime.Add(refreshTTL)

	got, err := repo.Create(ctx, familyID, userID, hash, expiresAt)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if got.ID == uuid.Nil {
		t.Error("Create() returned the zero ID")
	}
	if got.FamilyID != familyID || got.UserID != userID {
		t.Errorf("Create() = family %v/user %v, want %v/%v", got.FamilyID, got.UserID, familyID, userID)
	}
	if !got.ExpiresAt.Equal(expiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expiresAt)
	}
	if got.RevokedAt != nil || got.ReplacedAt != nil {
		t.Errorf("Create() = %+v, want a live, unreplaced token", got)
	}

	stored, ok := rowByHash(ctx, t, hash)
	if !ok {
		t.Fatal("Create() stored no row addressable by the token hash")
	}
	if stored.id != got.ID {
		t.Errorf("stored row id = %v, want %v", stored.id, got.ID)
	}
}

func TestPostgresRotateHappyPath(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	repo := NewPostgresRefreshRepository(testPool)

	presentedHash, presented := seedFamily(ctx, t, repo, baseTime.Add(refreshTTL))
	successorHash := newHash(t)
	newExpiry := baseTime.Add(refreshTTL)

	succ, outcome, err := repo.Rotate(ctx, presentedHash, successorHash, newExpiry, graceWindow, baseTime)
	if err != nil {
		t.Fatalf("Rotate() error: %v", err)
	}
	if outcome != RotateOK {
		t.Fatalf("outcome = %s, want RotateOK", outcomeName(outcome))
	}
	if succ.ID == presented.ID {
		t.Fatal("Rotate() returned the presented row, want the minted successor")
	}
	if succ.FamilyID != presented.FamilyID {
		t.Errorf("successor family = %v, want the presented token's family %v", succ.FamilyID, presented.FamilyID)
	}
	if succ.UserID != presented.UserID {
		t.Errorf("successor user = %v, want %v", succ.UserID, presented.UserID)
	}
	if !succ.ExpiresAt.Equal(newExpiry) {
		t.Errorf("successor expiry = %v, want the sliding expiry %v", succ.ExpiresAt, newExpiry)
	}
	if succ.RevokedAt != nil || succ.ReplacedAt != nil {
		t.Errorf("successor = %+v, want a live, unreplaced token", succ)
	}

	stored, ok := rowByHash(ctx, t, successorHash)
	if !ok {
		t.Fatal("successor is not addressable by its own hash")
	}
	if stored.id != succ.ID {
		t.Errorf("row stored under the new hash = %v, want the returned successor %v", stored.id, succ.ID)
	}

	old, ok := rowByHash(ctx, t, presentedHash)
	if !ok {
		t.Fatal("presented row disappeared; rotation must consume it in place, not delete it")
	}
	if old.replacedAt == nil {
		t.Fatal("replaced_at is unset on the presented row: a second use would not be detectable as reuse")
	}
	if !old.replacedAt.Equal(baseTime) {
		t.Errorf("replaced_at = %v, want the injected rotation clock %v", old.replacedAt, baseTime)
	}
	if old.replacedBy == nil || *old.replacedBy != succ.ID {
		t.Errorf("replaced_by = %v, want the successor %v", old.replacedBy, succ.ID)
	}
	if old.revokedAt != nil {
		t.Error("presented row was revoked; a normal rotation must only mark it replaced")
	}
	if rows := familyRows(ctx, t, presented.FamilyID); len(rows) != 2 {
		t.Errorf("family has %d rows, want 2 (presented + successor)", len(rows))
	}
}

func TestPostgresRotateInvalid(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	repo := NewPostgresRefreshRepository(testPool)

	tests := []struct {
		name      string
		presented func(t *testing.T) []byte
	}{
		{
			name:      "unknown hash",
			presented: func(t *testing.T) []byte { return newHash(t) },
		},
		{
			name: "expired token",
			presented: func(t *testing.T) []byte {
				hash, _ := seedFamily(ctx, t, repo, baseTime.Add(-time.Second))
				return hash
			},
		},
		{
			name: "expires exactly at the rotation clock",
			presented: func(t *testing.T) []byte {
				// expires_at must be strictly after now to rotate.
				hash, _ := seedFamily(ctx, t, repo, baseTime)
				return hash
			},
		},
		{
			name: "revoked token",
			presented: func(t *testing.T) []byte {
				hash, _ := seedFamily(ctx, t, repo, baseTime.Add(refreshTTL))
				if _, err := repo.RevokeFamilyByHash(ctx, hash); err != nil {
					t.Fatalf("revoking seeded token: %v", err)
				}
				return hash
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			presentedHash := tt.presented(t)
			successorHash := newHash(t)

			got, outcome, err := repo.Rotate(ctx, presentedHash, successorHash, baseTime.Add(refreshTTL), graceWindow, baseTime)
			if err != nil {
				t.Fatalf("Rotate() error = %v, want a clean RotateInvalid", err)
			}
			if outcome != RotateInvalid {
				t.Fatalf("outcome = %s, want RotateInvalid", outcomeName(outcome))
			}
			// RefreshRepository documents the token as zero on RotateInvalid;
			// a rejected rotation must hand its caller nothing to act on.
			if got != (RefreshToken{}) {
				t.Errorf("Rotate() = %+v, want the zero RefreshToken on RotateInvalid", got)
			}
			if _, ok := rowByHash(ctx, t, successorHash); ok {
				t.Error("Rotate() minted a successor for a token it rejected")
			}
		})
	}
}

// TestPostgresRotateGraceWindow covers the two-tab race: a second refresh of
// the same token arrives just after the first rotated it. Inside the grace
// window that is a concurrent refresh, not a stolen token (ADR-0003).
func TestPostgresRotateGraceWindow(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	repo := NewPostgresRefreshRepository(testPool)

	tests := []struct {
		name   string
		second time.Time // clock of the second rotation
	}{
		{"same instant", baseTime},
		{"mid-window", baseTime.Add(graceWindow / 2)},
		{"exactly at the window edge", baseTime.Add(graceWindow)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			presentedHash, presented := seedFamily(ctx, t, repo, baseTime.Add(refreshTTL))

			firstHash := newHash(t)
			first, outcome, err := repo.Rotate(ctx, presentedHash, firstHash, baseTime.Add(refreshTTL), graceWindow, baseTime)
			if err != nil || outcome != RotateOK {
				t.Fatalf("first Rotate() = %s, %v; want RotateOK", outcomeName(outcome), err)
			}

			siblingHash := newHash(t)
			sibling, outcome, err := repo.Rotate(ctx, presentedHash, siblingHash, tt.second.Add(refreshTTL), graceWindow, tt.second)
			if err != nil {
				t.Fatalf("second Rotate() error: %v", err)
			}
			if outcome != RotateOK {
				t.Fatalf("outcome = %s, want RotateOK: a refresh inside the grace window is a concurrent tab, not reuse", outcomeName(outcome))
			}
			if sibling.ID == first.ID {
				t.Error("grace rotation returned the first successor's row, want a distinct sibling")
			}
			if sibling.FamilyID != presented.FamilyID {
				t.Errorf("sibling family = %v, want %v", sibling.FamilyID, presented.FamilyID)
			}
			if sibling.UserID != presented.UserID {
				t.Errorf("sibling user = %v, want %v", sibling.UserID, presented.UserID)
			}
			if sibling.RevokedAt != nil || sibling.ReplacedAt != nil {
				t.Errorf("sibling = %+v, want a live, unreplaced token", sibling)
			}

			rows := familyRows(ctx, t, presented.FamilyID)
			if len(rows) != 3 {
				t.Fatalf("family has %d rows, want 3 (presented + successor + sibling)", len(rows))
			}
			for _, r := range rows {
				if r.revokedAt != nil {
					t.Errorf("row %s is revoked; the grace path must not trip reuse detection", r.id)
				}
			}

			old, ok := rowByHash(ctx, t, presentedHash)
			if !ok {
				t.Fatal("presented row disappeared")
			}
			if old.replacedBy == nil || *old.replacedBy != first.ID {
				t.Errorf("replaced_by = %v, want the first successor %v: the grace path must not relink the chain", old.replacedBy, first.ID)
			}
			if old.replacedAt == nil || !old.replacedAt.Equal(baseTime) {
				t.Errorf("replaced_at = %v, want the first rotation's clock %v", old.replacedAt, baseTime)
			}

			// Both successors are usable sessions, which is the point of the
			// grace window: neither tab is logged out.
			for name, hash := range map[string][]byte{"first successor": firstHash, "sibling": siblingHash} {
				_, outcome, err := repo.Rotate(ctx, hash, newHash(t), tt.second.Add(refreshTTL), graceWindow, tt.second.Add(time.Minute))
				if err != nil || outcome != RotateOK {
					t.Errorf("rotating the %s = %s, %v; want RotateOK", name, outcomeName(outcome), err)
				}
			}
		})
	}
}

// TestPostgresRotateReuseDetection covers the stolen-cookie signal: a token
// presented after it was rotated, outside the grace window, kills the family.
func TestPostgresRotateReuseDetection(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	repo := NewPostgresRefreshRepository(testPool)

	tests := []struct {
		name   string
		second time.Time
	}{
		{"just outside the window", baseTime.Add(graceWindow + time.Millisecond)},
		{"long after", baseTime.Add(time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			presentedHash, presented := seedFamily(ctx, t, repo, baseTime.Add(refreshTTL))

			successorHash := newHash(t)
			_, outcome, err := repo.Rotate(ctx, presentedHash, successorHash, baseTime.Add(refreshTTL), graceWindow, baseTime)
			if err != nil || outcome != RotateOK {
				t.Fatalf("first Rotate() = %s, %v; want RotateOK", outcomeName(outcome), err)
			}

			thiefHash := newHash(t)
			row, outcome, err := repo.Rotate(ctx, presentedHash, thiefHash, tt.second.Add(refreshTTL), graceWindow, tt.second)
			if err != nil {
				t.Fatalf("second Rotate() error: %v", err)
			}
			if outcome != RotateReuse {
				t.Fatalf("outcome = %s, want RotateReuse", outcomeName(outcome))
			}
			if row.ID != presented.ID {
				t.Errorf("returned row = %v, want the presented row %v (the service logs its user and family)", row.ID, presented.ID)
			}
			if row.FamilyID != presented.FamilyID || row.UserID != presented.UserID {
				t.Errorf("returned row = family %v/user %v, want %v/%v", row.FamilyID, row.UserID, presented.FamilyID, presented.UserID)
			}
			if _, ok := rowByHash(ctx, t, thiefHash); ok {
				t.Error("reuse minted a successor; a detected replay must issue no token")
			}

			rows := familyRows(ctx, t, presented.FamilyID)
			if len(rows) != 2 {
				t.Fatalf("family has %d rows, want 2", len(rows))
			}
			for _, r := range rows {
				if r.revokedAt == nil {
					t.Errorf("row %s has revoked_at unset, want every token in the family revoked", r.id)
				}
			}

			// The legitimate holder's live token dies with the family — that is
			// the intended cost of a suspected theft.
			_, outcome, err = repo.Rotate(ctx, successorHash, newHash(t), tt.second.Add(refreshTTL), graceWindow, tt.second)
			if err != nil {
				t.Fatalf("rotating the revoked successor: %v", err)
			}
			if outcome != RotateInvalid {
				t.Errorf("rotating the successor of a revoked family = %s, want RotateInvalid", outcomeName(outcome))
			}
		})
	}
}

// presentAfterRotation rotates a fresh token at baseTime to stamp replaced_at,
// then presents that same already-rotated token again from an app instance
// whose clock sits skew behind the one that rotated it. It returns the family's
// first row, the hash the second attempt tried to mint, and the result.
func presentAfterRotation(ctx context.Context, t *testing.T, repo *PostgresRefreshRepository, skew time.Duration) (RefreshToken, []byte, rotateResult) {
	t.Helper()
	presentedHash, presented := seedFamily(ctx, t, repo, baseTime.Add(refreshTTL))
	if _, outcome, err := repo.Rotate(ctx, presentedHash, newHash(t), baseTime.Add(refreshTTL), graceWindow, baseTime); err != nil || outcome != RotateOK {
		t.Fatalf("seeding rotation = %s, %v; want RotateOK", outcomeName(outcome), err)
	}

	behind := baseTime.Add(-skew)

	// Guard the premise: if the stamp ever stopped landing ahead of the
	// presenting clock, the cases below would still pass while quietly testing
	// something with no skew in it at all.
	stamped, ok := rowByHash(ctx, t, presentedHash)
	if !ok || stamped.replacedAt == nil {
		t.Fatal("no replaced_at stamped on the presented row")
	}
	if !stamped.replacedAt.After(behind) {
		t.Fatalf("replaced_at %v is not ahead of the presenting clock %v; this case no longer exercises a behind-clock instance",
			stamped.replacedAt, behind)
	}

	siblingHash := newHash(t)
	row, outcome, err := repo.Rotate(ctx, presentedHash, siblingHash, behind.Add(refreshTTL), graceWindow, behind)
	return presented, siblingHash, rotateResult{row: row, outcome: outcome, err: err}
}

// TestPostgresRotateGraceWindowUnderClockSkew covers refreshes whose elapsed
// time comes out negative. now is read by the app instance handling the
// request — Postgres is not in this clock path at all — so one instance can
// stamp replaced_at slightly ahead of the clock another instance later reads
// it with. Since elapsed is the token's true age plus that skew, the sign of
// elapsed says something about skew rather than about replay: only skew
// exceeding the token's whole age can turn it negative. The window is
// therefore measured by magnitude, and these two cases pin both ends of it.
//
// The skews are absolute rather than derived from graceWindow, so retuning the
// window cannot quietly turn either case into a copy of the other.
func TestPostgresRotateGraceWindowUnderClockSkew(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	repo := NewPostgresRefreshRepository(testPool)

	// The second tab of an ordinary two-tab race, landing on an instance whose
	// clock sits behind the one that just rotated. The token is milliseconds
	// old; the negative elapsed is the skew, not evidence of anything. Revoking
	// here would log an honest user out of every session and raise a false
	// theft warning, which is why this is the load-bearing case.
	t.Run("skew inside the window is a concurrent refresh", func(t *testing.T) {
		presented, siblingHash, res := presentAfterRotation(ctx, t, repo, time.Second)
		if res.err != nil {
			t.Fatalf("Rotate() error: %v", res.err)
		}
		if res.outcome != RotateOK {
			t.Fatalf("outcome = %s, want RotateOK: elapsed of -1s is skew between instances, not a replay", outcomeName(res.outcome))
		}
		if res.row.FamilyID != presented.FamilyID {
			t.Errorf("sibling family = %v, want %v", res.row.FamilyID, presented.FamilyID)
		}

		sibling, ok := rowByHash(ctx, t, siblingHash)
		if !ok {
			t.Fatal("no sibling minted: the concurrent tab was denied a token")
		}
		if sibling.id != res.row.ID {
			t.Errorf("row stored under the sibling hash = %v, want the returned row %v", sibling.id, res.row.ID)
		}
		rows := familyRows(ctx, t, presented.FamilyID)
		if len(rows) != 3 {
			t.Fatalf("family has %d rows, want 3 (presented + successor + sibling)", len(rows))
		}
		for _, r := range rows {
			if r.revokedAt != nil {
				t.Errorf("row %s is revoked; ordinary clock skew must not trip reuse detection", r.id)
			}
		}
	})

	// Skew big enough to flip the sign of a token that is genuinely old. An
	// elapsed time this far outside the window is not a concurrent refresh in
	// either direction, so the magnitude bound still does the security work.
	t.Run("skew beyond the window is still a replay", func(t *testing.T) {
		presented, siblingHash, res := presentAfterRotation(ctx, t, repo, time.Hour)
		if res.err != nil {
			t.Fatalf("Rotate() error: %v", res.err)
		}
		if res.outcome != RotateReuse {
			t.Fatalf("outcome = %s, want RotateReuse: elapsed of -1h is far outside the window", outcomeName(res.outcome))
		}
		if res.row.ID != presented.ID {
			t.Errorf("returned row = %v, want the presented row %v", res.row.ID, presented.ID)
		}
		if _, ok := rowByHash(ctx, t, siblingHash); ok {
			t.Error("a replay minted a sibling: the grace path ran where reuse detection was required")
		}
		rows := familyRows(ctx, t, presented.FamilyID)
		if len(rows) != 2 {
			t.Fatalf("family has %d rows, want 2 (presented + successor, no sibling)", len(rows))
		}
		for _, r := range rows {
			if r.revokedAt == nil {
				t.Errorf("row %s is still live; a detected replay must revoke the whole family", r.id)
			}
		}
	})
}

func TestPostgresRevokeFamilyByHash(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	repo := NewPostgresRefreshRepository(testPool)

	t.Run("revokes every live row in the family", func(t *testing.T) {
		presentedHash, presented := seedFamily(ctx, t, repo, baseTime.Add(refreshTTL))
		successorHash := newHash(t)
		if _, outcome, err := repo.Rotate(ctx, presentedHash, successorHash, baseTime.Add(refreshTTL), graceWindow, baseTime); err != nil || outcome != RotateOK {
			t.Fatalf("seeding rotation = %s, %v; want RotateOK", outcomeName(outcome), err)
		}

		// Logout presents the current token, so revocation must walk to the
		// family rather than only revoking the row it matched.
		if _, err := repo.RevokeFamilyByHash(ctx, successorHash); err != nil {
			t.Fatalf("RevokeFamilyByHash() error: %v", err)
		}
		rows := familyRows(ctx, t, presented.FamilyID)
		if len(rows) != 2 {
			t.Fatalf("family has %d rows, want 2", len(rows))
		}
		for _, r := range rows {
			if r.revokedAt == nil {
				t.Errorf("row %s has revoked_at unset after logout", r.id)
			}
		}
		if _, outcome, err := repo.Rotate(ctx, successorHash, newHash(t), baseTime.Add(refreshTTL), graceWindow, baseTime); err != nil || outcome != RotateInvalid {
			t.Errorf("rotating after logout = %s, %v; want RotateInvalid", outcomeName(outcome), err)
		}
	})

	t.Run("unknown hash is a silent no-op", func(t *testing.T) {
		_, other := seedFamily(ctx, t, repo, baseTime.Add(refreshTTL))

		if _, err := repo.RevokeFamilyByHash(ctx, newHash(t)); err != nil {
			t.Fatalf("RevokeFamilyByHash(unknown) error = %v, want nil (logout is idempotent)", err)
		}
		for _, r := range familyRows(ctx, t, other.FamilyID) {
			if r.revokedAt != nil {
				t.Errorf("row %s in an unrelated family was revoked", r.id)
			}
		}
	})

	t.Run("repeated logout does not restamp", func(t *testing.T) {
		hash, seeded := seedFamily(ctx, t, repo, baseTime.Add(refreshTTL))
		if _, err := repo.RevokeFamilyByHash(ctx, hash); err != nil {
			t.Fatalf("first RevokeFamilyByHash() error: %v", err)
		}
		first := familyRows(ctx, t, seeded.FamilyID)

		if _, err := repo.RevokeFamilyByHash(ctx, hash); err != nil {
			t.Fatalf("second RevokeFamilyByHash() error = %v, want nil", err)
		}
		second := familyRows(ctx, t, seeded.FamilyID)
		if len(first) != len(second) {
			t.Fatalf("family row count changed from %d to %d", len(first), len(second))
		}
		for i := range first {
			if first[i].revokedAt == nil || second[i].revokedAt == nil {
				t.Fatalf("row %s is not revoked", first[i].id)
			}
			if !first[i].revokedAt.Equal(*second[i].revokedAt) {
				t.Errorf("row %s revoked_at moved from %v to %v on a repeat logout",
					first[i].id, first[i].revokedAt, second[i].revokedAt)
			}
		}
	})
}

type rotateResult struct {
	row     RefreshToken
	outcome RotateOutcome
	err     error
}

// rotateConcurrently runs one Rotate per clock in clocks, all against the same
// presented hash, released together so the transactions genuinely contend.
func rotateConcurrently(ctx context.Context, t *testing.T, repo *PostgresRefreshRepository, presentedHash []byte, clocks ...time.Time) []rotateResult {
	t.Helper()
	results := make([]rotateResult, len(clocks))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, clock := range clocks {
		hash := newHash(t) // minted here: newHash calls t.Fatalf, illegal off the test goroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			row, outcome, err := repo.Rotate(ctx, presentedHash, hash, clock.Add(refreshTTL), graceWindow, clock)
			results[i] = rotateResult{row: row, outcome: outcome, err: err}
		}()
	}
	close(start)
	wg.Wait()
	return results
}

// TestPostgresRotateConcurrent is the race the grace window exists for, run
// for real against Postgres rather than simulated.
func TestPostgresRotateConcurrent(t *testing.T) {
	requireDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo := NewPostgresRefreshRepository(testPool)

	// Two tabs refresh the same token at the same instant: one takes the happy
	// path and the other the grace path, and neither tab is logged out.
	t.Run("two tabs refresh at the same instant", func(t *testing.T) {
		presentedHash, presented := seedFamily(ctx, t, repo, baseTime.Add(refreshTTL))

		var ids []uuid.UUID
		for _, r := range rotateConcurrently(ctx, t, repo, presentedHash, baseTime, baseTime) {
			if r.err != nil {
				t.Fatalf("concurrent Rotate() error: %v", r.err)
			}
			if r.outcome != RotateOK {
				t.Fatalf("concurrent Rotate() outcome = %s, want RotateOK for both tabs", outcomeName(r.outcome))
			}
			if r.row.FamilyID != presented.FamilyID {
				t.Errorf("issued token in family %v, want %v", r.row.FamilyID, presented.FamilyID)
			}
			ids = append(ids, r.row.ID)
		}
		if ids[0] == ids[1] {
			t.Error("both tabs were handed the same token row")
		}

		rows := familyRows(ctx, t, presented.FamilyID)
		if len(rows) != 3 {
			t.Fatalf("family has %d rows, want 3 (presented + one successor per tab)", len(rows))
		}
		replaced := 0
		for _, r := range rows {
			if r.revokedAt != nil {
				t.Errorf("row %s is revoked; a concurrent refresh must not trip reuse detection", r.id)
			}
			if r.replacedAt != nil {
				replaced++
				if r.id != presented.ID {
					t.Errorf("row %s is marked replaced, want only the presented row %s", r.id, presented.ID)
				}
			}
		}
		if replaced != 1 {
			t.Errorf("%d rows marked replaced, want exactly 1 (the presented row)", replaced)
		}
	})

	// The interleaving the row lock actually protects: a stale replay revokes
	// the family while a legitimate tab is minting a grace sibling into it.
	// Whichever transaction wins, a revoked family must not be left holding a
	// live token — that would be a session surviving its own theft signal.
	t.Run("a replay revoking the family races a grace refresh", func(t *testing.T) {
		presentedHash, presented := seedFamily(ctx, t, repo, baseTime.Add(refreshTTL))
		if _, outcome, err := repo.Rotate(ctx, presentedHash, newHash(t), baseTime.Add(refreshTTL), graceWindow, baseTime); err != nil || outcome != RotateOK {
			t.Fatalf("seeding rotation = %s, %v; want RotateOK", outcomeName(outcome), err)
		}

		// Same stale token, two clocks: one inside the grace window (the honest
		// second tab), one long outside it (the replay).
		results := rotateConcurrently(ctx, t, repo, presentedHash, baseTime, baseTime.Add(time.Hour))
		grace, replay := results[0], results[1]
		if grace.err != nil || replay.err != nil {
			t.Fatalf("Rotate() errors: grace %v, replay %v", grace.err, replay.err)
		}
		if replay.outcome != RotateReuse {
			t.Errorf("replay outcome = %s, want RotateReuse", outcomeName(replay.outcome))
		}
		// The honest tab wins a token only if it got there first; either order
		// is correct, so the outcome is not pinned — the invariant below is.
		if grace.outcome == RotateReuse {
			t.Errorf("grace refresh outcome = %s, want RotateOK or RotateInvalid", outcomeName(grace.outcome))
		}

		for _, r := range familyRows(ctx, t, presented.FamilyID) {
			if r.revokedAt == nil {
				t.Errorf("row %s is still live after the family was revoked for reuse", r.id)
			}
		}
	})
}
