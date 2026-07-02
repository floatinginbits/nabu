# Backend Design Principles

Go service architecture for Nabu, beyond the baseline in `CLAUDE.md`. This is a living document — package layout in particular will firm up once the first services are scaffolded.

## Package layout (proposed)

```
cmd/
  nabu/           single entrypoint; a --mode flag (or subcommand) selects api server vs worker,
                  matching the "same binary, worker mode" decision in ARCHITECTURE.md
internal/
  http/           handlers + middleware — request/response translation only, no business logic
  task/           task domain: service + repository interfaces + implementation
  auth/           token issuance/validation, session handling
  rbac/           role definitions and permission checks
  notification/   NotificationService interface + the v1 no-op implementation
  store/          shared DB access setup (pool, migrations runner)
  config/         env var loading and validation
```

Each domain package (`task`, `auth`, ...) owns its own repository interface and implementation — don't create a single generic `store` package that every domain reaches into.

## Layering
`handler → service → repository`
- **Handlers** parse the request, call one service method, translate the result/error to a response. No business logic, no direct DB access.
- **Services** hold business logic and orchestrate repositories. Depend on repository *interfaces*, not concrete implementations, so they're testable without a database.
- **Repositories** do data access only, using `sqlc`-generated queries ([ADR-0001](../adr/0001-database-access-pattern.md)). Keep `sqlc`-generated types *inside* the repository — services speak in domain structs — so the access pattern stays swappable behind the interface.

### Dynamic queries
`sqlc` handles static queries well but not conditional `WHERE` clauses. For queries with optional filters — chiefly the task-list endpoint (status/assignee/sprint/project + cursor pagination) — prefer `sqlc` nullable params where the Postgres planner tolerates it, and fall back to the `squirrel` query builder only where it does not. Per ADR-0001, if more than roughly a third of queries end up in `squirrel`, that's the signal to reconsider the access pattern rather than keep patching.

## Dependency injection
Constructor injection everywhere (`NewTaskService(repo TaskRepository, notifier NotificationService) *TaskService`). No package-level global state, no `init()`-time singletons. Wiring happens once, in `cmd/nabu/main.go`.

## Configuration
- Loaded once at startup from environment variables, validated eagerly — fail fast on missing required config rather than panicking mid-request
- Tuning knobs called out in `ARCHITECTURE.md` (pool sizes, worker concurrency, cache TTLs) get sane defaults so the app runs with zero required config beyond DB/Redis connection strings

## Migrations
Schema evolves through `goose` SQL migration files ([ADR-0001](../adr/0001-database-access-pattern.md)), which double as `sqlc`'s schema source — migrations are the single source of truth for the schema, no separate schema definition to keep in sync.

- Define tables with plain DDL `sqlc` can parse — don't use goose's Go-based migrations for schema changes, or `sqlc` can't derive types from them.
- **Index creation on core tables** (`tasks`, and anything else large in a live deployment) uses `CREATE INDEX CONCURRENTLY` with a `-- +goose NO TRANSACTION` annotation. goose wraps migrations in a transaction by default, and `CONCURRENTLY` cannot run inside one — skipping this risks locking a big table during a self-hosted upgrade.
- Every migration needs a working `Down` section, exercised at least once — an untested rollback is not a rollback.

## Background jobs
- Worker mode of the same binary, jobs queued via Redis (see `ARCHITECTURE.md`)
- One job type = one handler function, registered by name
- Jobs must be idempotent — the queue can and will redeliver on worker restart/crash

## Middleware chain
Request ID → structured logging (`slog`, includes request ID) → panic recovery → auth (token validation) → RBAC (role check) → handler. Keep this order fixed so logging always captures a request even if auth fails.
