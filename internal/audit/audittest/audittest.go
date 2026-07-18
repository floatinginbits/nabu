// Package audittest provides the recording audit.Recorder the domain test
// suites install, plus the secret-denylist assertion ADR-0004 relies on.
//
// The redaction rule is enforced here, at the Recorder boundary, rather than in
// per-builder tests: a test written against today's metadata builders cannot
// fail for a builder somebody adds next quarter, which is the exact risk the
// rule exists to control. Every test that wires New(t) contributes its entries
// to the check automatically.
package audittest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/floatinginbits/nabu/internal/audit"
)

// deniedSubstrings must never appear in an encoded audit entry, as a key or as
// a value. The bcrypt and JWT prefixes catch a secret that was copied into
// metadata under an innocent-looking key, which a key-name check alone misses.
var deniedSubstrings = []string{
	"password",
	"password_hash",
	"$2a$", // bcrypt hash
	"$2b$",
	"eyJ", // base64url '{"' — a JWT header or payload
	"token_hash",
	"refresh_token",
	"secret",
}

// Recorder collects entries in memory and asserts, at test cleanup, that none
// of them carry a secret.
type Recorder struct {
	mu      sync.Mutex
	entries []audit.Entry
}

// New returns a Recorder that checks its own entries against the denylist when
// the test finishes, so a caller gets the guarantee without opting in.
func New(t *testing.T) *Recorder {
	t.Helper()
	r := &Recorder{}
	t.Cleanup(func() { r.AssertNoSecrets(t) })
	return r
}

func (r *Recorder) Record(_ context.Context, e audit.Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
}

// Entries returns the entries recorded so far.
func (r *Recorder) Entries() []audit.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]audit.Entry(nil), r.entries...)
}

// Actions returns the recorded actions in order, for tests that only care that
// the right event fired.
func (r *Recorder) Actions() []string {
	entries := r.Entries()
	actions := make([]string, len(entries))
	for i, e := range entries {
		actions[i] = e.Action
	}
	return actions
}

// AssertNoSecrets marshals every recorded entry's metadata and fails the test
// if the result contains anything on the denylist.
func (r *Recorder) AssertNoSecrets(t *testing.T) {
	t.Helper()
	for _, v := range r.violations() {
		t.Error(v)
	}
}

func (r *Recorder) violations() []string {
	var found []string
	for _, e := range r.Entries() {
		encoded, err := json.Marshal(e.Metadata)
		if err != nil {
			found = append(found, fmt.Sprintf("audit entry %q has unmarshalable metadata: %v", e.Action, err))
			continue
		}
		lowered := strings.ToLower(string(encoded))
		for _, denied := range deniedSubstrings {
			if strings.Contains(lowered, strings.ToLower(denied)) {
				found = append(found, fmt.Sprintf("audit entry %q leaks %q in its metadata; metadata "+
					"must be built from a per-entity allowlist (ADR-0004)", e.Action, denied))
			}
		}
	}
	return found
}
