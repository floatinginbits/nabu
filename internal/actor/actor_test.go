package actor

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestFromContext(t *testing.T) {
	full := Actor{UserID: uuid.New(), OrgID: uuid.New()}

	tests := []struct {
		name   string
		ctx    context.Context
		want   Actor
		wantOK bool
	}{
		{"round-trips a stored actor", NewContext(context.Background(), full), full, true},
		{"bare context has no actor", context.Background(), Actor{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FromContext(tt.ctx)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("actor = %+v, want %+v", got, tt.want)
			}
		})
	}
}
