# Nabu

Self-hosted, open-source task tracker for software development teams. Go backend, React + TypeScript frontend. See [README.md](README.md) for the stack overview and [ARCHITECTURE.md](ARCHITECTURE.md) for architecture decisions and rationale.

## Conventions

### Go
- `gofmt`/`goimports` enforced by CI; `golangci-lint` for static analysis
- `ctx context.Context` as the first parameter on every function that does I/O
- Wrap errors with context: `fmt.Errorf("doing X: %w", err)`
- Table-driven tests in `*_test.go` alongside the code they test
- Structured logging via `slog`, no external logging dependency

### React
- Functional components only, no class components
- TypeScript strict mode; ESLint + Prettier enforced by CI
- API client is generated from the OpenAPI spec — do not hand-write API types

### General
- Comments only when the WHY is non-obvious (hidden constraint, workaround, subtle invariant) — never restate what the code does
- No speculative abstractions, feature flags, or error handling for cases that can't happen
- Conventional Commits format (`feat:`, `fix:`, `chore:`, `docs:`, `refactor:`) for commits and PR titles
- Keep PRs small and scoped to one concern

## Detailed conventions

These cover specific areas in more depth than fits here — read the relevant one before working in that area:

@docs/conventions/frontend-design.md
@docs/conventions/backend-design.md
@docs/conventions/api-contract.md
@docs/conventions/testing-strategy.md
@docs/conventions/data-model.md
@docs/conventions/security-baseline.md
@docs/conventions/git-workflow.md

## For agents working in this repo
- Read ARCHITECTURE.md before making architectural decisions — check its "Open architectural decisions" section before assuming something is settled.
- Never commit directly to `master`; always work on a `feature/`, `fix/`, `chore/`, or `docs/` branch and open a PR.
- If an issue is ambiguous or underspecified, comment asking for clarification instead of guessing.
