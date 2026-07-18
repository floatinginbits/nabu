package audittest

import (
	"context"
	"fmt"
	"testing"

	"github.com/floatinginbits/nabu/internal/audit"
)

// The denylist is the mitigation ADR-0004 leans on, so it needs its own test:
// a check that silently never fires is worse than no check, because the ADR
// claims coverage it does not have.
func TestRecorderDenylist(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		wantFlag bool
	}{
		{"allowlisted fields pass", map[string]any{"title": "Fix the bug", "status": "todo"}, false},
		{"empty metadata passes", nil, false},
		{"denied key", map[string]any{"password_hash": "x"}, true},
		// The value checks are what catch a secret hidden under an innocent
		// key, which a key-name check alone would miss.
		{"bcrypt hash under a harmless key", map[string]any{"note": "$2a$12$abcdef"}, true},
		{"bcrypt 2b variant", map[string]any{"note": "$2b$12$abcdef"}, true},
		{"jwt under a harmless key", map[string]any{"note": "eyJhbGciOiJIUzI1NiJ9.x.y"}, true},
		{"refresh token hash", map[string]any{"token_hash": "deadbeef"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Recorder{}
			r.Record(context.Background(), audit.Entry{Action: "test.action", Metadata: tt.metadata})
			got := len(r.violations()) > 0
			if got != tt.wantFlag {
				t.Errorf("violations found = %v, want %v (%v)", got, tt.wantFlag, r.violations())
			}
		})
	}
}

// The matcher working is not the guarantee ADR-0004 claims; New *invoking* it
// at cleanup is. Deleting the t.Cleanup line in New leaves every other test in
// the repo passing, so the wiring needs an assertion of its own — driven
// through a spy, because a real failing subtest would propagate its failure to
// this test.
func TestNewAssertsAtCleanup(t *testing.T) {
	tests := []struct {
		name      string
		metadata  map[string]any
		wantError bool
	}{
		{"a denylisted entry fails the test at cleanup", map[string]any{"password_hash": "$2a$12$x"}, true},
		{"a clean entry does not", map[string]any{"title": "Fix the bug"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy := &spyTB{}
			r := New(spy)
			r.Record(context.Background(), audit.Entry{Action: "test.action", Metadata: tt.metadata})
			spy.runCleanups()

			if got := len(spy.errors) > 0; got != tt.wantError {
				t.Errorf("cleanup reported an error = %v, want %v (%v)", got, tt.wantError, spy.errors)
			}
		})
	}
}

// spyTB stands in for *testing.T so a deliberate violation can be observed
// instead of failing the suite that observes it.
type spyTB struct {
	cleanups []func()
	errors   []string
}

func (s *spyTB) Helper()           {}
func (s *spyTB) Cleanup(f func())  { s.cleanups = append(s.cleanups, f) }
func (s *spyTB) Error(args ...any) { s.errors = append(s.errors, fmt.Sprint(args...)) }
func (s *spyTB) runCleanups() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
}
