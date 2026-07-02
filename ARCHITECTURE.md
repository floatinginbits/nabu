# Nabu — Architecture

This document describes Nabu's system architecture: the tech stack, how the pieces fit together, and the design decisions behind them. For coding conventions and implementation-level detail, see `CLAUDE.md` and `docs/conventions/`.

## Overview

Nabu is a self-hosted, open-source task tracker for software development teams. Core constraints that shape every decision below:

- **Self-hosted only** — one deployment serves one organization; there is no multi-tenancy
- **MIT licensed** — no commercial license required to build or run
- **Enterprise-grade** — designed to satisfy the operational and security expectations of an enterprise deployment, even where the full feature (SSO, compliance certs) isn't built yet
- **Scoped to software teams** — not a generic project-management tool; the data model and UI are opinionated around how software gets built

## Tech Stack

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| Backend | Go | Single binary, low memory footprint, strong concurrency — ideal for self-hosted deployment |
| Frontend | React + TypeScript | Proven, large contributor pool, strict TS mode |
| Build tool | Vite | Fast, minimal config |
| API style | REST + OpenAPI spec | Simpler than GraphQL for current UI needs; free documentation + generated clients |
| Database | PostgreSQL | Relational, JSONB for flexible metadata, built-in full-text search for v1 |
| Search | Meilisearch | Self-hosted, fast, typo-tolerant full-text search; lighter than Elasticsearch |
| Cache + job queue | Redis | Session/token caching plus the async job backend for background work |
| Logging | `slog` (Go standard library) | Structured, zero-allocation, no external dependency |

## System Design

### Stateless application tier
The app server holds no session state. Everything that needs to persist — session data, cached values, search indexes — is externalized to Postgres, Redis, or Meilisearch. This is what makes the app tier horizontally scalable from day one and lets deployment move from Docker Compose to Kubernetes later without an application rewrite.

### Authentication — two-token pattern
- **Access token**: short-lived JWT (15–30 min), validated statelessly on every request — no DB lookup on the hot path
- **Refresh token**: long-lived, opaque, stored in Postgres — enables instant revocation, active-session visibility, and force logout, none of which a pure-JWT design supports
- Both tokens live in HTTP-only cookies; never `localStorage`, which is an XSS exposure enterprises will flag in review
- Designed so a future SAML/SSO integration plugs into the same token pair (the IdP assertion just becomes another way to mint the same two tokens) — no auth-layer rebuild required later

### API design
- URL versioning: `/api/v1/...`; a breaking change bumps the whole API to `/api/v2`, not per-endpoint
- Consistent error envelope: `{ "error": { "code": "...", "message": "..." } }` — clients switch on `code`, never parse `message`
- Cursor-based pagination on list endpoints (not offset) — holds up better on large datasets
- HTTP status codes used semantically (`201` for creates, `422` for validation errors, etc.)

### Data model
One unified task entity underlies Kanban, Scrum, and backlog-plus-milestones — these are views over the same data (grouping/filtering), not separate schemas. Sprint and story points are optional fields on a task, not a parallel entity. Two extensibility points are built into the schema from day one, specifically to avoid a future migration:
- A `links`/`references` field on tasks, populated by pasted PR/issue URLs in v1 and by webhook-driven automation in v2 without a schema change
- A `NotificationService` interface (Strategy pattern) with a v1 no-op implementation — email, Slack, and webhook notifiers plug in later via the same interface, with no business-logic changes

Full schema-level conventions: `docs/conventions/data-model.md`.

### Enterprise baseline
- RBAC at both org level and project level (roles: admin, project lead, contributor, viewer), enforced at the service layer
- Pluggable auth layer — local credentials today, SSO/SAML-ready by construction (see Authentication above)
- Audit logging is a day-one data model concern, even though the UI for it is minimal initially — retrofitting audit trails after the fact is painful and often incomplete
- **Explicitly out of scope for v1**: SSO/SAML, SOC2/compliance certifications, multi-tenancy. The architecture avoids closing the door on any of these, but none are built yet.

## Deployment

### v1: Docker Compose
Multi-container Compose stack, chosen for ease of self-hosting by small teams without Kubernetes operational overhead.

### Future: Kubernetes
The stateless app tier and externalized state (Postgres/Redis/Meilisearch) mean the move to Kubernetes is a deployment change, not an application rewrite.

### Container topology

```
app          → Go API server (stateless, horizontally scalable)
worker       → same Go binary in worker mode (background jobs, Redis-backed queue)
db           → PostgreSQL
cache        → Redis
search       → Meilisearch
prometheus   → metrics scraper (optional Compose profile)
grafana      → dashboards (optional Compose profile)
```

Prometheus and Grafana are opt-in via a Compose profile — not required for a basic deployment.

## Observability
- The Go app exposes `/health` and `/metrics` (Prometheus format)
- A pre-built Grafana dashboard covers API latency, DB connections, queue depth, and memory/CPU
- Tuning knobs (connection pool sizes, worker concurrency, cache TTLs) are environment variables, so operators can tune performance or cost without touching code or external tooling

## Related documents
- `CLAUDE.md` — root-level conventions and pointers into `docs/conventions/`
- `docs/conventions/backend-design.md` — Go package layout and service/repository layering
- `docs/conventions/frontend-design.md` — React component and state conventions
- `docs/conventions/api-contract.md` — how the frontend and backend stay in sync (OpenAPI codegen, error handling, auth flow)
- `docs/conventions/data-model.md` — entity shapes, RBAC storage, audit log schema, naming conventions
- `docs/conventions/security-baseline.md` — write-time security checklist
- `docs/conventions/git-workflow.md` — branching, commits, and merge strategy

## Open architectural decisions
A few choices are deliberately not made yet, tracked here so implementation doesn't settle them by accident:

- **Database access pattern in Go** — `sqlc` (generated, compile-time-safe SQL) vs `GORM` (full ORM) vs `sqlx` (thin wrapper over `database/sql`)
- **Frontend styling approach** — Tailwind CSS vs CSS Modules vs shadcn/ui
- **JSON field casing** — `camelCase` (natural for the TS client) vs `snake_case` (natural for Go, needs a transform layer)
- **Docker image registry** — GHCR (default assumption, integrates with GitHub Actions) vs Docker Hub

This section should shrink to nothing as these get resolved and folded into the sections above.
