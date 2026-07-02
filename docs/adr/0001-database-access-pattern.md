# 0001 — Database access pattern: sqlc + goose

- **Status:** Accepted
- **Date:** 2026-07-02

## Context
Nabu's backend is Go over PostgreSQL. We need a way to write queries and a way to evolve the schema. Three properties matter for this project specifically:

- **Explicit control over SQL.** `ARCHITECTURE.md` positions Nabu as enterprise-grade with operators who tune performance directly; `CLAUDE.md` mandates "no speculative abstractions, no magic." Both argue against an ORM that hides the generated SQL.
- **A demanding hot-path query.** The task-list endpoint backs Kanban, Scrum, and backlog views over one unified task entity, with several *optional* filters (status, assignee, sprint, project) plus cursor-based pagination. This is dynamic query territory.
- **Heavy JSONB and enum use.** Tasks carry a `links` JSONB array; audit logs carry a `metadata` JSONB blob; `task_status` and RBAC roles are enum-like.

The three candidate access patterns were `sqlc` (raw SQL → generated type-safe Go), `sqlx` (thin wrapper, raw SQL, runtime struct scanning), and `GORM` (full ORM).

## Decision
We will use **`sqlc`** for database access and **`goose`** for schema migrations, against PostgreSQL.

- Queries live as raw SQL; `sqlc` generates type-safe Go from them.
- Schema evolves through `goose` SQL migration files. `sqlc` reads those same migration files as its schema source, so migrations are the single source of truth for the schema — there is no separate schema definition to keep in sync.
- Genuinely dynamic queries that `sqlc` cannot express cleanly (primarily the filtered task-list query) use `sqlc` nullable parameters where the query planner tolerates it, and a query builder (`squirrel`) only where it does not.

## Consequences
Easier: compile-time-checked, readable SQL for the bulk of the code; no ORM abstractions leaking into services; a single schema source of truth; two small Go-native single-binary tools that embed into the app and fit the single-binary deployment model.

Harder: a code-generation step in the build and in every contributor's workflow; dynamic queries need an escape hatch; `sqlc`'s type inference is only as good as its parse of the migration DDL.

### Limitations and risks
Drawn from a pre-mortem, ranked by likelihood × impact for Nabu:

1. **The hot path may bypass sqlc.** `sqlc`'s nullable-param trick for optional filters (`WHERE (@x IS NULL OR col = @x)`) frequently defeats the Postgres planner into sequential scans. Keeping the task-list query fast may push it into `squirrel` — leaving `sqlc` protecting trivial CRUD-by-id queries while the *risky* dynamic query is hand-built anyway, inverting the value proposition.
2. **"Compile-time safe" is against sqlc's parsed model, not the running database.** Where the parser and real Postgres disagree — JSONB operators, custom enum/domain types, extensions, partial indexes — `sqlc` errors at codegen or silently infers the wrong Go type. Code can compile green and break at runtime. Nabu's JSONB and enum usage makes this a live risk.
3. **Zero-downtime migrations collide with goose defaults.** `CREATE INDEX CONCURRENTLY` cannot run inside a transaction, but goose wraps migrations in one by default. An index migration written the normal way can lock a large `tasks` table during a self-hosted upgrade.
4. **Generated-code drift.** A contributor who forgets to run `sqlc generate` opens a PR with stale generated code; without a CI gate, queries and generated types silently diverge. Generated code also enlarges diffs, working against the "good first issue" goal.
5. **Down-migration rot.** Untested goose `Down` sections bit-rot and fail the one time a production rollback is actually needed.

### Mitigations
Each is an explicit, up-front action rather than tribal knowledge — see the corresponding Phase 1/2 items in `TASKS.md`:

- **Prototype the filtered task-list query first**, before the pattern is spread across every repository. Decide consciously where the sqlc/`squirrel` boundary sits and document it in `docs/conventions/backend-design.md`, rather than discovering it by accident. (Risks 1, 2)
- **CI drift check** — run `sqlc generate` and fail the build on any diff. Added with the first migration, non-negotiable. (Risk 4)
- **`EXPLAIN` the filtered task query in an integration test** against real Postgres (testcontainers, per `docs/conventions/testing-strategy.md`), so planner and type surprises surface as a failing test, not a production incident. (Risks 1, 2)
- **Migration convention**: index creation on core tables uses `CREATE INDEX CONCURRENTLY` with `-- +goose NO TRANSACTION`. Documented in `backend-design.md` before the first index exists. (Risk 3)

**Kill criterion.** If more than roughly a third of repository queries end up in `squirrel`, `sqlc` is paying for the cheap cases while we hand-write the expensive ones. At that point we standardize on `sqlx` and delete the codegen step. Because both are raw SQL behind the repository interface (per the `handler → service → repository` layering in `backend-design.md`), that migration is mechanical and contained to repository implementations.

## Alternatives considered
- **`sqlx`** — best flexibility for the dynamic task query and no codegen step, but no compile-time verification that a query matches its target struct; mismatches surface at runtime. Retained as the fallback if the kill criterion above is hit, since moving from `sqlc` to `sqlx` is low-cost.
- **`GORM`** — least SQL to write and fastest to start, but its abstractions leak on exactly the complex, RBAC-scoped, JSONB-laden queries Nabu has, and its generated SQL is harder to reason about and tune — working directly against the enterprise performance-tuning goal. It is also the most expensive option to reverse out of later, because its patterns do not stay behind the repository boundary.
