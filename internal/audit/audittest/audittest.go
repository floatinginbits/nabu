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

	"github.com/floatinginbits/nabu/internal/audit"
)

// deniedSubstrings must never appear in an encoded audit entry, as a key or as
// a value. The bcrypt and JWT prefixes catch a secret that was copied into
// metadata under an innocent-looking key, which a key-name check alone misses.
//
// Deliberately not here: a bare "password". It would flag a legitimate
// {"password_changed": true}, and the pressure to "fix" that lands on this
// list rather than on the entry — the wrong direction for the check guarding
// this boundary. "password_hash" and the bcrypt prefixes cover the hazard.
var deniedSubstrings = []string{
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
	next    audit.Recorder
}

// TB is the testing.TB subset this package needs. It is an interface rather
// than *testing.T so the cleanup wiring below can be tested with a spy: a
// t.Cleanup that stops being registered, or an AssertNoSecrets that stops
// firing, is invisible to every other test in the repo, and the guarantee
// ADR-0004 claims would be silently off.
type TB interface {
	Helper()
	Cleanup(func())
	Error(args ...any)
}

// New returns a Recorder that checks its own entries against the denylist when
// the test finishes, so a caller gets the guarantee without opting in.
func New(t TB) *Recorder {
	t.Helper()
	return newWithCleanup(t, nil)
}

// Tee wraps a real Recorder so a suite that needs entries to actually reach the
// database — the end-to-end HTTP suite — still contributes them to the
// denylist. Without it that suite is exempt from the check, and it is the one
// where a new endpoint's first audited action shows up.
func Tee(t TB, next audit.Recorder) *Recorder {
	t.Helper()
	return newWithCleanup(t, next)
}

func newWithCleanup(t TB, next audit.Recorder) *Recorder {
	r := &Recorder{next: next}
	t.Cleanup(func() { r.AssertNoSecrets(t) })
	return r
}

func (r *Recorder) Record(ctx context.Context, e audit.Entry) {
	r.mu.Lock()
	r.entries = append(r.entries, e)
	next := r.next
	r.mu.Unlock()
	if next != nil {
		next.Record(ctx, e)
	}
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
func (r *Recorder) AssertNoSecrets(t TB) {
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
