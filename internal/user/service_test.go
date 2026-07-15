package user

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// fakeRepo is an in-memory Repository keyed by lowercased email, mirroring
// the case-insensitive unique index on the real users table.
type fakeRepo struct {
	users       map[string]User
	createCalls int
	// createErr, when set, is returned by Create before touching the map —
	// simulates DB-level failures the map model can't produce (e.g. losing
	// the bootstrap race to another replica).
	createErr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: make(map[string]User)}
}

func (f *fakeRepo) Create(_ context.Context, email, displayName, passwordHash string) (User, error) {
	f.createCalls++
	if f.createErr != nil {
		return User{}, f.createErr
	}
	key := strings.ToLower(email)
	if _, ok := f.users[key]; ok {
		return User{}, ErrEmailTaken
	}
	now := time.Now()
	u := User{
		ID:           uuid.New(),
		Email:        email,
		DisplayName:  displayName,
		PasswordHash: passwordHash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	f.users[key] = u
	return u, nil
}

func (f *fakeRepo) GetByEmail(_ context.Context, email string) (User, error) {
	u, ok := f.users[strings.ToLower(email)]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (f *fakeRepo) GetByID(_ context.Context, id uuid.UUID) (User, error) {
	for _, u := range f.users {
		if u.ID == id {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (f *fakeRepo) Count(_ context.Context) (int64, error) {
	return int64(len(f.users)), nil
}

func TestServiceCreateValidation(t *testing.T) {
	tests := []struct {
		name        string
		email       string
		displayName string
		password    string
		wantEmail   string // asserted on the stored user when wantErr is false
		wantErr     bool
	}{
		{"valid", "alice@example.com", "Alice", "correct horse", "alice@example.com", false},
		{"email trimmed and lowercased", "  Alice@Example.COM ", "Alice", "correct horse", "alice@example.com", false},
		{"email missing @", "aliceexample.com", "Alice", "correct horse", "", true},
		{"email in Name <addr> form", "Alice <alice@example.com>", "Alice", "correct horse", "", true},
		{"email empty", "", "Alice", "correct horse", "", true},
		{"display name empty", "alice@example.com", "", "correct horse", "", true},
		{"display name whitespace only", "alice@example.com", "   ", "correct horse", "", true},
		{"display name over 200 runes", "alice@example.com", strings.Repeat("é", maxDisplayNameLen+1), "correct horse", "", true},
		{"display name exactly 200 runes", "alice@example.com", strings.Repeat("é", maxDisplayNameLen), "correct horse", "alice@example.com", false},
		{"password under 8 runes", "alice@example.com", "Alice", "1234567", "", true},
		{"password over 72 bytes", "alice@example.com", "Alice", strings.Repeat("a", maxPasswordBytes+1), "", true},
		{"password exactly 72 bytes", "alice@example.com", "Alice", strings.Repeat("a", maxPasswordBytes), "alice@example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo()
			svc := NewService(repo)
			got, err := svc.Create(context.Background(), tt.email, tt.displayName, tt.password)
			if tt.wantErr {
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("Create() error = %v, want *ValidationError", err)
				}
				if repo.createCalls != 0 {
					t.Errorf("repo.Create called %d times on validation failure, want 0", repo.createCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create() error: %v", err)
			}
			if got.Email != tt.wantEmail {
				t.Errorf("Create() email = %q, want %q", got.Email, tt.wantEmail)
			}
		})
	}
}

func TestServiceCreatePasswordHash(t *testing.T) {
	const password = "open sesame 123"
	svc := NewService(newFakeRepo())
	got, err := svc.Create(context.Background(), "hash@example.com", "Hash", password)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if got.PasswordHash == password {
		t.Fatal("PasswordHash is the plaintext password")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(got.PasswordHash), []byte(password)); err != nil {
		t.Errorf("PasswordHash does not verify against the original password: %v", err)
	}
}

func TestServiceCreateDuplicateEmail(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo())
	if _, err := svc.Create(ctx, "alice@example.com", "Alice", "correct horse"); err != nil {
		t.Fatalf("first Create() error: %v", err)
	}
	// Different case: the service lowercases, so the repo sees the same key.
	_, err := svc.Create(ctx, "ALICE@Example.com", "Alice Again", "correct horse")
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("second Create() error = %v, want ErrEmailTaken", err)
	}
}

func TestServiceAuthenticate(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeRepo())
	seeded, err := svc.Create(ctx, "alice@example.com", "Alice", "open sesame 123")
	if err != nil {
		t.Fatalf("seeding user: %v", err)
	}

	tests := []struct {
		name     string
		email    string
		password string
		wantErr  error
	}{
		{"correct credentials", "alice@example.com", "open sesame 123", nil},
		{"email case-insensitive", "ALICE@Example.COM", "open sesame 123", nil},
		{"wrong password", "alice@example.com", "not the password", ErrInvalidCredentials},
		{"unknown email", "nobody@example.com", "open sesame 123", ErrInvalidCredentials},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.Authenticate(ctx, tt.email, tt.password)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Authenticate() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Authenticate() error: %v", err)
			}
			if got.ID != seeded.ID {
				t.Errorf("Authenticate() returned user %v, want %v", got.ID, seeded.ID)
			}
		})
	}
}

func TestServiceEnsureInitialAdmin(t *testing.T) {
	ctx := context.Background()

	t.Run("creates admin on empty repo", func(t *testing.T) {
		repo := newFakeRepo()
		svc := NewService(repo)
		created, err := svc.EnsureInitialAdmin(ctx, "Admin@Example.com", "open sesame 123")
		if err != nil {
			t.Fatalf("EnsureInitialAdmin() error: %v", err)
		}
		if !created {
			t.Fatal("EnsureInitialAdmin() = false, want true")
		}
		u, err := repo.GetByEmail(ctx, "admin@example.com")
		if err != nil {
			t.Fatalf("admin not found after EnsureInitialAdmin: %v", err)
		}
		if u.Email != "admin@example.com" || u.DisplayName != "Admin" {
			t.Errorf("admin user = %+v, want lowercased email and display name %q", u, "Admin")
		}
	})

	t.Run("no-op when users exist", func(t *testing.T) {
		repo := newFakeRepo()
		svc := NewService(repo)
		if _, err := svc.Create(ctx, "existing@example.com", "Existing", "open sesame 123"); err != nil {
			t.Fatalf("seeding user: %v", err)
		}
		callsBefore := repo.createCalls
		created, err := svc.EnsureInitialAdmin(ctx, "admin@example.com", "open sesame 123")
		if err != nil {
			t.Fatalf("EnsureInitialAdmin() error: %v", err)
		}
		if created {
			t.Error("EnsureInitialAdmin() = true on non-empty repo, want false")
		}
		if repo.createCalls != callsBefore {
			t.Errorf("repo.Create called %d more times, want 0", repo.createCalls-callsBefore)
		}
	})

	t.Run("lost race to another replica is not an error", func(t *testing.T) {
		repo := newFakeRepo()
		repo.createErr = ErrEmailTaken // another instance created the admin between Count and Create
		svc := NewService(repo)
		created, err := svc.EnsureInitialAdmin(ctx, "admin@example.com", "open sesame 123")
		if err != nil {
			t.Fatalf("EnsureInitialAdmin() error: %v", err)
		}
		if created {
			t.Error("EnsureInitialAdmin() = true, want false when another replica won the race")
		}
	})

	t.Run("invalid configured password propagates", func(t *testing.T) {
		repo := newFakeRepo()
		svc := NewService(repo)
		created, err := svc.EnsureInitialAdmin(ctx, "admin@example.com", "short")
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("EnsureInitialAdmin() error = %v, want *ValidationError", err)
		}
		if created {
			t.Error("EnsureInitialAdmin() = true, want false on invalid password")
		}
		if repo.createCalls != 0 {
			t.Errorf("repo.Create called %d times, want 0", repo.createCalls)
		}
	})
}
