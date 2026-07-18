package user

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/floatinginbits/nabu/internal/testdb"
)

// Shared across all integration tests in this package; set up in TestMain.
var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	testdb.Main(m, &testPool)
}

// requireDB skips under -short. Tests share one database and there is no
// truncation between them, so every test must use emails of its own.
func requireDB(t *testing.T) {
	t.Helper()
	testdb.SkipIfShort(t)
}

func createTestUser(ctx context.Context, t *testing.T, repo *PostgresRepository, email string) User {
	t.Helper()
	u, err := repo.Create(ctx, email, "Test User", "x-test-hash")
	if err != nil {
		t.Fatalf("Create(%q) error: %v", email, err)
	}
	return u
}

func TestPostgresCreateGetByEmailCaseInsensitive(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)

	created, err := repo.Create(ctx, "Round.Trip@Example.com", "Round Tripper", "hash-round-trip")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Error("Create() returned zero ID")
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Error("Create() timestamps are zero")
	}

	got, err := repo.GetByEmail(ctx, "round.trip@EXAMPLE.COM")
	if err != nil {
		t.Fatalf("GetByEmail() with different case error: %v", err)
	}
	if got.ID != created.ID ||
		got.Email != "Round.Trip@Example.com" ||
		got.DisplayName != "Round Tripper" ||
		got.PasswordHash != "hash-round-trip" {
		t.Errorf("GetByEmail() = %+v, want the created user %+v", got, created)
	}
	if !got.CreatedAt.Equal(created.CreatedAt) || !got.UpdatedAt.Equal(created.UpdatedAt) {
		t.Errorf("timestamps did not round-trip: got %v/%v, want %v/%v",
			got.CreatedAt, got.UpdatedAt, created.CreatedAt, created.UpdatedAt)
	}
}

func TestPostgresCreateDuplicateEmailDifferentCase(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)

	createTestUser(ctx, t, repo, "dupe@example.com")
	_, err := repo.Create(ctx, "DUPE@Example.com", "Second", "hash-2")
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("Create() with same email in different case error = %v, want ErrEmailTaken", err)
	}
}

func TestPostgresGetByEmailNotFound(t *testing.T) {
	requireDB(t)
	_, err := NewPostgresRepository(testPool).GetByEmail(context.Background(), "missing@example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByEmail(unknown) error = %v, want ErrNotFound", err)
	}
}

func TestPostgresGetByID(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)

	t.Run("round-trip", func(t *testing.T) {
		created := createTestUser(ctx, t, repo, "by-id@example.com")
		got, err := repo.GetByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetByID() error: %v", err)
		}
		if got.ID != created.ID || got.Email != created.Email || got.DisplayName != created.DisplayName || got.PasswordHash != created.PasswordHash {
			t.Errorf("GetByID() = %+v, want %+v", got, created)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		_, err := repo.GetByID(ctx, uuid.New())
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("GetByID(unknown) error = %v, want ErrNotFound", err)
		}
	})
}

func TestPostgresCount(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	repo := NewPostgresRepository(testPool)

	before, err := repo.Count(ctx)
	if err != nil {
		t.Fatalf("Count() error: %v", err)
	}
	createTestUser(ctx, t, repo, "count-a@example.com")
	createTestUser(ctx, t, repo, "count-b@example.com")
	after, err := repo.Count(ctx)
	if err != nil {
		t.Fatalf("Count() error: %v", err)
	}
	if after != before+2 {
		t.Errorf("Count() = %d after 2 inserts, want %d", after, before+2)
	}
}
