# Frontend ↔ Backend Integration

How the two sides of Nabu stay in sync, beyond the baseline API conventions in `ARCHITECTURE.md` (`/api/v1`, error envelope, cursor pagination).

## Spec-first workflow
The OpenAPI spec is the source of truth, not a byproduct of either side's code:
1. A change to the API contract starts with editing the OpenAPI spec
2. Backend types/validation and frontend client are both generated from it (`oapi-codegen` or similar for Go, `openapi-typescript`/`orval` or similar for the TS client — **tooling not yet confirmed**)
3. Handwriting request/response types on either side is a sign the spec is out of date, not a shortcut to take

Once CI exists, a generation-drift check (regenerate from spec, fail the build on uncommitted diff) should enforce this automatically.

## Field casing
API request and response bodies use **camelCase** (`storyPoints`, `nextCursor`, `createdAt`). The primary consumer is the TypeScript client, where camelCase is idiomatic and where the generated types carry whatever casing the spec declares.

This is a wire-format choice, independent of the database: Postgres columns stay snake_case (see `data-model.md`) and sqlc-generated structs stay inside the repository (ADR-0001). The API DTO layer that services map to is separate, and its Go structs carry explicit `json:"camelCase"` tags — there is no automatic name derivation and no runtime transform layer, just the tags you write on the DTOs regardless.

## Error handling
The envelope is `{ "error": { "code": "...", "message": "..." } }`. The frontend switches on `code` (a stable machine-readable identifier), never parses `message` (human-readable, may change wording). Proposed baseline codes: `VALIDATION_ERROR`, `NOT_FOUND`, `UNAUTHORIZED`, `FORBIDDEN`, `CONFLICT`, `INTERNAL`. New codes are additive; don't repurpose an existing one for a new meaning.

## Auth flow (client side)
Tokens live in HTTP-only cookies — the frontend never reads or stores them directly. On a `401`:
1. Attempt a silent refresh (call the refresh endpoint)
2. Retry the original request once
3. If the retry also fails, redirect to login

This retry-once logic belongs in the API client wrapper, not duplicated per call site.

## Pagination
List endpoints return `{ data: [...], nextCursor: string | null }`. The frontend treats `nextCursor` as an opaque token — never construct or decode one, just pass it back on the next request.

## Versioning
Breaking changes bump the whole API to `/api/v2`, not per-endpoint versioning. Deprecated endpoints get a `Deprecation` response header for at least one release cycle before removal, so integrations (including Nabu's own frontend) have warning.
