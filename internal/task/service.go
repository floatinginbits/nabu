package task

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
	maxTitleLen     = 500
)

// ValidationError reports invalid caller input; the HTTP layer translates it
// to a 422 with code VALIDATION_ERROR.
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return e.Msg }

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, title string) (Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Task{}, &ValidationError{Msg: "title is required"}
	}
	if utf8.RuneCountInString(title) > maxTitleLen {
		return Task{}, &ValidationError{Msg: fmt.Sprintf("title exceeds %d characters", maxTitleLen)}
	}
	t, err := s.repo.Create(ctx, title)
	if err != nil {
		return Task{}, fmt.Errorf("creating task: %w", err)
	}
	return t, nil
}

type ListParams struct {
	Status   *Status
	Cursor   string
	PageSize int
}

type ListResult struct {
	Tasks      []Task
	NextCursor string // "" on the last page
}

func (s *Service) List(ctx context.Context, p ListParams) (ListResult, error) {
	if p.Status != nil && !p.Status.Valid() {
		return ListResult{}, &ValidationError{Msg: fmt.Sprintf("unknown status %q", *p.Status)}
	}
	size := p.PageSize
	if size <= 0 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}

	// Fetch one extra row: its presence means another page exists, without a
	// second COUNT query.
	filter := ListFilter{Status: p.Status, Limit: size + 1}
	if p.Cursor != "" {
		c, err := decodeCursor(p.Cursor)
		if err != nil {
			return ListResult{}, &ValidationError{Msg: "invalid cursor"}
		}
		filter.After = &c
	}

	tasks, err := s.repo.List(ctx, filter)
	if err != nil {
		return ListResult{}, fmt.Errorf("listing tasks: %w", err)
	}

	res := ListResult{Tasks: tasks}
	if len(tasks) > size {
		res.Tasks = tasks[:size]
		last := res.Tasks[size-1]
		res.NextCursor = encodeCursor(Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	return res, nil
}

// cursorPayload is the wire shape inside the opaque cursor. Clients never
// construct or decode cursors (api-contract.md), so this can change freely.
type cursorPayload struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        uuid.UUID `json:"id"`
}

func encodeCursor(c Cursor) string {
	b, _ := json.Marshal(cursorPayload(c))
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("decoding cursor: %w", err)
	}
	var p cursorPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return Cursor{}, fmt.Errorf("decoding cursor: %w", err)
	}
	if p.ID == uuid.Nil || p.CreatedAt.IsZero() {
		return Cursor{}, fmt.Errorf("decoding cursor: missing fields")
	}
	return Cursor(p), nil
}
