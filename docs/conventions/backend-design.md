# Backend Design Principles

Go service architecture for Nabu, beyond the baseline in `CLAUDE.md`. This is a living document — package layout in particular will firm up once the first services are scaffolded.

## Package layout (proposed)

```
cmd/
  nabu/           single entrypoint; a --mode flag (or subcommand) selects api server vs worker,
                  matching the "same binary, worker mode" decision in HANDOFF.md
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
- **Repositories** do data access only. Implementation depends on the DB access pattern decision in `HANDOFF.md` (`sqlc`/`GORM`/`sqlx`) — don't assume one before that's resolved.

## Dependency injection
Constructor injection everywhere (`NewTaskService(repo TaskRepository, notifier NotificationService) *TaskService`). No package-level global state, no `init()`-time singletons. Wiring happens once, in `cmd/nabu/main.go`.

## Configuration
- Loaded once at startup from environment variables, validated eagerly — fail fast on missing required config rather than panicking mid-request
- Tuning knobs called out in `HANDOFF.md` (pool sizes, worker concurrency, cache TTLs) get sane defaults so the app runs with zero required config beyond DB/Redis connection strings

## Migrations
Recommend a SQL-file-based migration tool (`golang-migrate` or `goose`) independent of whichever DB access pattern is chosen — migrations are plain SQL either way. **Not yet confirmed**, flag if this needs to be added to the open-decisions list.

## Background jobs
- Worker mode of the same binary, jobs queued via Redis (already decided in `HANDOFF.md`)
- One job type = one handler function, registered by name
- Jobs must be idempotent — the queue can and will redeliver on worker restart/crash

## Middleware chain
Request ID → structured logging (`slog`, includes request ID) → panic recovery → auth (token validation) → RBAC (role check) → handler. Keep this order fixed so logging always captures a request even if auth fails.
