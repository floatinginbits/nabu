package project

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/floatinginbits/nabu/internal/testdb"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	testdb.Main(m, &testPool)
}

// seededOrgID is the singleton org migration 00004 creates. v1's schema has a
// unique index on a constant, so a second organization row cannot exist —
// which is why the cross-org cases below use an org id that resolves to
// nothing rather than a second real org.
func seededOrgID(t *testing.T) uuid.UUID {
	t.Helper()
	testdb.SkipIfShort(t)
	var id uuid.UUID
	if err := testPool.QueryRow(context.Background(), "SELECT id FROM organizations").Scan(&id); err != nil {
		t.Fatalf("resolving the seeded org: %v", err)
	}
	return id
}

func TestPostgresListIsOrgScoped(t *testing.T) {
	orgID := seededOrgID(t)
	repo := NewPostgresRepository(testPool)
	ctx := context.Background()

	t.Run("the seeded org sees its default project", func(t *testing.T) {
		ps, err := repo.List(ctx, orgID)
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(ps) != 1 {
			t.Fatalf("got %d projects, want the seeded one", len(ps))
		}
		if ps[0].Key != "GEN" || ps[0].OrgID != orgID {
			t.Errorf("project = %+v, want GEN in org %v", ps[0], orgID)
		}
	})

	t.Run("another org sees nothing", func(t *testing.T) {
		ps, err := repo.List(ctx, uuid.New())
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(ps) != 0 {
			t.Errorf("List(other org) = %+v, want nothing", ps)
		}
	})
}

// A project outside the caller's org is indistinguishable from one that does
// not exist: both are ErrNotFound, so the lookup can't be used to probe for
// projects the caller may not see (security-baseline.md).
func TestPostgresGetByID(t *testing.T) {
	orgID := seededOrgID(t)
	repo := NewPostgresRepository(testPool)
	ctx := context.Background()

	ps, err := repo.List(ctx, orgID)
	if err != nil || len(ps) == 0 {
		t.Fatalf("List() = %+v, %v; want the seeded project", ps, err)
	}
	seeded := ps[0]

	tests := []struct {
		name  string
		id    uuid.UUID
		orgID uuid.UUID
		want  error
	}{
		{"in the caller's org", seeded.ID, orgID, nil},
		{"right id, wrong org", seeded.ID, uuid.New(), ErrNotFound},
		{"unknown id", uuid.New(), orgID, ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.GetByID(ctx, tt.id, tt.orgID)
			if tt.want != nil {
				if !errors.Is(err, tt.want) {
					t.Fatalf("GetByID() error = %v, want %v", err, tt.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetByID() error: %v", err)
			}
			if got.ID != seeded.ID || got.OrgID != orgID {
				t.Errorf("GetByID() = %+v, want the seeded project", got)
			}
		})
	}
}

func TestPostgresSingletonOrgID(t *testing.T) {
	orgID := seededOrgID(t)
	got, err := NewPostgresRepository(testPool).SingletonOrgID(context.Background())
	if err != nil {
		t.Fatalf("SingletonOrgID() error: %v", err)
	}
	if got != orgID {
		t.Errorf("SingletonOrgID() = %v, want %v", got, orgID)
	}
}
