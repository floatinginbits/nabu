package audittest

import (
	"context"
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
