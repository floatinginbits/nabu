# Data Model Conventions

Domain entity shape and DB conventions for Nabu, beyond the product decisions in `ARCHITECTURE.md`. Treat this as a first draft — it will firm up when the schema is actually implemented.

## Task entity (core fields)
- `id`, `title`, `description`, `status`, `project_id`, `assignee_id` (nullable)
- `sprint_id` (nullable) — optional container, per the unified task model
- `story_points` (nullable) — optional, Scrum-only usage
- `links` — JSONB array of `{ type, url, title, status }`, populated by pasted PR/issue URLs in v1 and by webhook automation in v2 without a schema change
- `created_at`, `updated_at`, plus standard audit fields (see below)

Kanban, Scrum, and backlog + milestones are all queries/views over this one table with different groupings and filters — never fork it into per-workflow tables.

## RBAC
- Roles: `admin` > `project_lead` > `contributor` > `viewer`, defined at org level with an optional per-project override
- Enforced at the service layer (see `backend-design.md`), never inferred purely from what the UI shows or hides
- Store role assignments as `(user_id, org_id, role)` and `(user_id, project_id, role)` — project-level rows override the org-level default for that project only

## Audit log
- Minimal v1 schema, but complete enough to not require a migration later: `actor_id`, `action`, `entity_type`, `entity_id`, `metadata` (JSONB — before/after snapshot or diff), `created_at`
- Every state-changing service method writes an audit row — this is a cross-cutting concern, not something each feature remembers to add individually

## Naming conventions
- `snake_case` for tables and columns (Postgres convention); the API exposes camelCase independently (see `api-contract.md`), mapped at the DTO layer — DB casing is not a wire-format decision
- Plural table names (`tasks`, `projects`, `audit_logs`)
- Foreign keys named `<singular_entity>_id` (`project_id`, `assignee_id`)

## JSONB usage
Reserve JSONB for genuinely schema-less or evolving data (`links`, audit `metadata`, future custom fields) — not as a shortcut around normalizing data that has real relational structure. If you're querying inside a JSONB blob by a specific key regularly, that's a signal it should be a column instead.
