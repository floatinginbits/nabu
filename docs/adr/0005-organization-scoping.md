# 0005 — Organization scoping: a singleton org from day one

- **Status:** Accepted
- **Date:** 2026-07-18

## Context
`ARCHITECTURE.md` states that one deployment serves one organization and lists multi-tenancy as explicitly out of scope for v1. It also requires RBAC "at both org level and project level", and `docs/conventions/security-baseline.md` requires that every query be scoped by the `project_id`/`org_id` derived from the authenticated session, precisely so a future multi-tenant mode does not need a rewrite. Those two statements pull in opposite directions: the first says an org is not a thing v1 has, the second says every row already belongs to one.

The projects domain forced the question. Projects group tasks, RBAC role assignments will hang off projects and off whatever owns them, and the task-list query is scoped per request. Something has to be the owning scope, and the choice is either a real column now or a synthesized one later.

The cost asymmetry is what decides it. Adding an owning-scope column later is not a single migration: it is a `NOT NULL` column plus a backfill plus a new composite unique index on every scoped table (`projects`, `tasks`, role assignments, audit logs), applied to live self-hosted databases we do not operate and cannot inspect, with each table's uniqueness constraints changing meaning at the same time (`projects.key` unique globally becomes unique per org). Adding it now costs one column on tables that are empty in every deployment that exists.

## Decision
We will introduce an `organizations` table now, with `org_id` on projects and on the role assignments that follow, while keeping v1's *behavior* single-org.

**The table is a schema-enforced singleton.** Migration 00004 creates `organizations`, seeds one row, and enforces the singleton with a unique index on a constant expression:

```sql
CREATE UNIQUE INDEX organizations_singleton_idx ON organizations ((true));
```

A second row is rejected by the database, not by a convention in a code comment or a check somebody remembers to write. This matters more than it looks: without it, "v1 is single-org" is an assumption the runtime is free to violate quietly, and any code that resolves *the* org — `SingletonOrgID` at startup, every test fixture that reads `SELECT id FROM organizations` — would be resolving one of several rows and silently picking a winner. With it, those call sites are correct by construction. Lifting the restriction later is a one-line `DROP INDEX`, which is the cheapest possible migration.

**Runtime code must not branch on org identity.** The org is a value that flows — resolved once from the session and passed to queries as a scope — never a value anything compares, special-cases, or treats as "the default". There is no `if orgID == defaultOrg` anywhere, no org-aware code path, no configuration naming an org. The singleton is schema shape, not behavior: the only difference between v1 and a future multi-org deployment should be how many rows the table has and how the org is resolved during authentication.

**The org comes from the session, never from the request.** `actor.Actor` carries `OrgID` alongside `UserID`, populated by the auth middleware from the server's own resolution of the session. Each domain service reads it from the context itself (`actor.FromContext`) and passes it to its repository; handlers never extract it and never pass a scope down, because choosing a tenancy scope is a decision, not request translation (`backend-design.md`). A client-supplied `projectId` is an argument to be validated against the session's org, never an authorization to read that project.

**Cross-org reads are indistinguishable from missing rows.** `project.Service.GetByID` returns `ErrNotFound` for a project in another org rather than a forbidden error, so the API cannot be used to probe for the existence of rows the caller cannot see.

## Consequences
Multi-tenancy becomes a feature rather than a schema migration across every table: the columns, the composite indexes, and the scoping in every query are already there, and what remains is org resolution at login, org lifecycle management, and RBAC at the org level. The security-baseline requirement that every query be session-scoped becomes checkable rather than aspirational — a repository method without an org parameter is visibly wrong. Tests exercise the same scoping code paths the multi-tenant version would.

Harder: every scoped query carries a parameter that does nothing observable in v1, every new table has to decide whether it is org-scoped, and reviewers must resist the reasonable-sounding suggestion to simplify away a column that "always has the same value". The singleton index is itself a surprise to anyone meeting the schema for the first time — an index on `(true)` is not a common sight.

### Limitations and risks
- **The scoping could be theatre.** Columns and parameters exist, but if a query is ever written without the org predicate, it works perfectly in every single-org deployment and leaks across orgs the day a second org exists. Single-org testing cannot catch this by observing behavior.
- **v1 code may drift into org-awareness.** A shortcut that reads "there is only one org" — caching the id as global state, defaulting it, comparing against it — reintroduces exactly the coupling this ADR pays a cost to avoid, and it will not fail any test.
- **Unique constraints are easy to get wrong in the direction that hurts later.** A globally-unique `projects.key` would be indistinguishable from a per-org one until a second org exists, and correcting it then means resolving real collisions in customer data.
- **`SingletonOrgID` is load-bearing.** Startup resolves the org and the auth middleware stamps it onto every actor; that path becomes a lie the moment the singleton index is dropped, and dropping it is exactly what the multi-org migration will do.

### Mitigations
- Org scoping is asserted directly in tests rather than inferred: `internal/project` tests use an org id that resolves to nothing and require `ErrNotFound`, and the service tests assert the repository received the *session's* org. The pattern to copy for each new scoped table is a "another org's row is invisible" case.
- The unique index is `(org_id, lower(key))` from the start — per-org, not global — so no collision has to be resolved later.
- The `actor.FromContext`-in-the-service convention is enforced by there being exactly one way to obtain an org, with one shared `actor.ErrNoActor` sentinel; a service that wants a scope has one place to get it, and no other layer offers one.
- **Leading indicator that this decision has failed:** any `if` whose condition is an org id, any config value naming an org, or a repository method whose data-returning query does not take an org. Any of those means the singleton has leaked from schema into behavior, and the multi-org path is no longer a `DROP INDEX` plus login changes.

## Alternatives considered
- **No org column at all — projects are the top-level scope.** Simplest thing that satisfies v1, and honest about ARCHITECTURE.md's "no multi-tenancy". Rejected on migration cost: RBAC already requires org-level roles (ARCHITECTURE.md's enterprise baseline), so an org has to be modelled for roles regardless, and adding the column later means a backfill plus a uniqueness-semantics change on every scoped table across databases operated by self-hosters we cannot coordinate with. The saving is one column on empty tables; the cost is the riskiest class of migration we could schedule.
- **Full multi-tenancy now** — org CRUD, org-scoped auth, per-org RBAC administration, an org switcher in the UI. Rejected as building an unrequested feature: it is a large surface (invitations, org-level roles, cross-org user identity, per-org configuration) with no v1 consumer, and it directly contradicts ARCHITECTURE.md's out-of-scope list. The schema shape is what is expensive to change later; the feature is not.
- **A nullable `org_id`, populated when multi-tenancy arrives.** Rejected: nullable scope columns are the worst of both — the query planner and `sqlc` both see an optional value (nullable columns generate `uuid.NullUUID`, spreading null handling through services), and "unscoped" stays a representable state, which is the state that leaks data.
- **A `WHERE org_id = current_setting(...)` row-level-security policy instead of application-level scoping.** Rejected for v1: Postgres RLS enforces the boundary in the one place that cannot be bypassed, which is genuinely stronger, but it requires per-request session configuration on a pooled connection (an easy thing to get wrong with pgxpool), obscures scoping from the SQL that `sqlc` compiles, and cannot be tested without a real database. Worth revisiting if multi-tenancy becomes real — the explicit `org_id` predicates are a prerequisite for it either way, not an obstacle.
