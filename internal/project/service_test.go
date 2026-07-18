package project

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/floatinginbits/nabu/internal/actor"
)

type fakeRepo struct {
	calls     int
	gotOrgID  uuid.UUID
	projects  []Project
	getResult Project
	err       error
}

func (f *fakeRepo) List(_ context.Context, orgID uuid.UUID) ([]Project, error) {
	f.calls++
	f.gotOrgID = orgID
	return f.projects, f.err
}

func (f *fakeRepo) GetByID(_ context.Context, _, orgID uuid.UUID) (Project, error) {
	f.calls++
	f.gotOrgID = orgID
	return f.getResult, f.err
}

// The service resolves the org from the session, so the repository must always
// receive the actor's org and never anything a caller chose.
func TestServiceScopesToTheSessionOrg(t *testing.T) {
	orgID := uuid.New()
	ctx := actor.NewContext(context.Background(), actor.Actor{UserID: uuid.New(), OrgID: orgID})

	tests := []struct {
		name string
		call func(*Service) error
	}{
		{"List", func(s *Service) error { _, err := s.List(ctx); return err }},
		{"GetByID", func(s *Service) error { _, err := s.GetByID(ctx, uuid.New()); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			if err := tt.call(NewService(repo)); err != nil {
				t.Fatalf("%s() error = %v, want nil", tt.name, err)
			}
			if repo.gotOrgID != orgID {
				t.Errorf("repository scoped to org %v, want the session's %v", repo.gotOrgID, orgID)
			}
		})
	}
}

// A context with no actor is a wiring bug — every project route sits behind
// requireAuth. The service must fail before querying rather than fall through
// to a query scoped to the zero org.
func TestServiceRejectsContextWithoutActor(t *testing.T) {
	tests := []struct {
		name string
		call func(*Service) error
	}{
		{"List", func(s *Service) error {
			_, err := s.List(context.Background())
			return err
		}},
		{"GetByID", func(s *Service) error {
			_, err := s.GetByID(context.Background(), uuid.New())
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			err := tt.call(NewService(repo))
			if !errors.Is(err, actor.ErrNoActor) {
				t.Errorf("%s() error = %v, want ErrNoActor", tt.name, err)
			}
			if repo.calls != 0 {
				t.Errorf("repository called %d times without an actor, want 0", repo.calls)
			}
		})
	}
}
