package task

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeRepo struct {
	gotTitle  string
	gotFilter ListFilter
	tasks     []Task
	err       error
}

func (f *fakeRepo) Create(_ context.Context, title string) (Task, error) {
	f.gotTitle = title
	if f.err != nil {
		return Task{}, f.err
	}
	return Task{ID: uuid.New(), Title: title, Status: StatusTodo}, nil
}

func (f *fakeRepo) List(_ context.Context, filter ListFilter) ([]Task, error) {
	f.gotFilter = filter
	return f.tasks, f.err
}

func newTestTask(createdAt time.Time) Task {
	return Task{ID: uuid.New(), Title: "t", Status: StatusTodo, CreatedAt: createdAt, UpdatedAt: createdAt}
}

func TestServiceCreate(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		wantTitle string
		wantErr   bool
	}{
		{"valid", "Fix the bug", "Fix the bug", false},
		{"trims whitespace", "  padded  ", "padded", false},
		{"empty", "", "", true},
		{"whitespace only", "   ", "", true},
		{"too long", strings.Repeat("x", maxTitleLen+1), "", true},
		{"at max length", strings.Repeat("x", maxTitleLen), strings.Repeat("x", maxTitleLen), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			svc := NewService(repo)
			got, err := svc.Create(context.Background(), tt.title)
			if tt.wantErr {
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("Create() error = %v, want ValidationError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create() error: %v", err)
			}
			if got.Title != tt.wantTitle || repo.gotTitle != tt.wantTitle {
				t.Errorf("Create() title = %q (repo saw %q), want %q", got.Title, repo.gotTitle, tt.wantTitle)
			}
		})
	}
}

func TestServiceListPageSize(t *testing.T) {
	tests := []struct {
		name      string
		pageSize  int
		wantLimit int
	}{
		{"default", 0, defaultPageSize + 1},
		{"explicit", 10, 11},
		{"capped at max", maxPageSize + 500, maxPageSize + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			svc := NewService(repo)
			if _, err := svc.List(context.Background(), ListParams{PageSize: tt.pageSize}); err != nil {
				t.Fatalf("List() error: %v", err)
			}
			if repo.gotFilter.Limit != tt.wantLimit {
				t.Errorf("repo limit = %d, want %d", repo.gotFilter.Limit, tt.wantLimit)
			}
		})
	}
}

func TestServiceListValidation(t *testing.T) {
	bad := Status("bogus")
	tests := []struct {
		name   string
		params ListParams
	}{
		{"unknown status", ListParams{Status: &bad}},
		{"garbage cursor", ListParams{Cursor: "not-base64!!!"}},
		{"valid base64, wrong payload", ListParams{Cursor: "e30"}}, // {}
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeRepo{})
			_, err := svc.List(context.Background(), tt.params)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("List() error = %v, want ValidationError", err)
			}
		})
	}
}

func TestServiceListNextCursor(t *testing.T) {
	now := time.Now()
	full := make([]Task, 3) // pageSize 2 + 1 extra row
	for i := range full {
		full[i] = newTestTask(now.Add(-time.Duration(i) * time.Minute))
	}

	t.Run("more pages", func(t *testing.T) {
		repo := &fakeRepo{tasks: full}
		svc := NewService(repo)
		res, err := svc.List(context.Background(), ListParams{PageSize: 2})
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(res.Tasks) != 2 {
			t.Fatalf("len(tasks) = %d, want 2 (extra row trimmed)", len(res.Tasks))
		}
		if res.NextCursor == "" {
			t.Fatal("NextCursor empty, want set")
		}
		c, err := decodeCursor(res.NextCursor)
		if err != nil {
			t.Fatalf("decoding returned cursor: %v", err)
		}
		if c.ID != res.Tasks[1].ID {
			t.Errorf("cursor points at %v, want last returned task %v", c.ID, res.Tasks[1].ID)
		}
	})

	t.Run("last page", func(t *testing.T) {
		repo := &fakeRepo{tasks: full[:2]} // exactly pageSize rows, no extra
		svc := NewService(repo)
		res, err := svc.List(context.Background(), ListParams{PageSize: 2})
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(res.Tasks) != 2 || res.NextCursor != "" {
			t.Errorf("got %d tasks, cursor %q; want 2 tasks and empty cursor", len(res.Tasks), res.NextCursor)
		}
	})

	t.Run("empty", func(t *testing.T) {
		svc := NewService(&fakeRepo{})
		res, err := svc.List(context.Background(), ListParams{})
		if err != nil {
			t.Fatalf("List() error: %v", err)
		}
		if len(res.Tasks) != 0 || res.NextCursor != "" {
			t.Errorf("got %d tasks, cursor %q; want none", len(res.Tasks), res.NextCursor)
		}
	})
}

func TestCursorRoundTrip(t *testing.T) {
	want := Cursor{CreatedAt: time.Now().UTC().Truncate(time.Microsecond), ID: uuid.New()}
	got, err := decodeCursor(encodeCursor(want))
	if err != nil {
		t.Fatalf("decodeCursor() error: %v", err)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || got.ID != want.ID {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}
