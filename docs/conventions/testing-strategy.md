# Testing Strategy

How Nabu's test suite gets built, beyond the baseline (table-driven Go tests) in `CLAUDE.md`.

## Test pyramid
- **Unit** (majority of tests): service/business-logic functions in Go, component logic and hooks on the frontend — fast, no network/DB
- **Integration**: repository layer against a real Postgres instance (testcontainers, or the Docker Compose test profile), and API handlers exercised end-to-end within the Go process (no real HTTP, but the full handler → service → repository chain)
- **End-to-end** (thin layer, golden paths only): login, create a task, move it across a board, assign a role — not exhaustive coverage, just the paths that would be embarrassing to break

## From feature to test cases
For every feature or bug fix, derive test cases from:
1. **Acceptance criteria** in the issue — each stated behavior gets at least one test
2. **RBAC matrix** — for any protected endpoint/action, test each role (admin, project lead, contributor, viewer) gets the correct allow/deny outcome, not just the happy-path role
3. **Boundary/edge cases** — empty states, single-item and exact-page-size pagination boundaries, concurrent edits to the same task
4. **Regression case** (bug fixes only) — a test that fails on the old code and passes on the fix, so the bug can't silently return

## RBAC test matrix
Maintain this as literal table-driven test cases (role × endpoint → expected status), not prose. Adding a new role or a new protected endpoint should make the coverage gap visible in the test file itself.

## Fixtures and test data
Prefer small builder/factory functions (`newTestTask(opts...)`) over static fixture files. Test data should be the minimum needed to exercise what the test asserts — a test with ten irrelevant fields set up is hiding what actually matters.

## Frontend testing
Framework not yet chosen — Vitest + React Testing Library is the natural default given the Vite build tool already decided, but this should be confirmed rather than assumed. Test user-visible behavior (what's rendered, what happens on interaction), not implementation details like internal state shape.

## Definition of done
A change isn't done until: its tests cover the golden path plus at least one failure/edge case, and the full relevant suite (not just the new test) passes locally.
