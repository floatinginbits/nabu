# Security Baseline

Write-time checklist for Nabu — the companion to the `security-reviewer` agent's review-time checklist. The goal is to avoid these issues at write time rather than catch them after the fact.

## Auth
- Always validate JWT signature, expiry, and issuer server-side on every request — never trust a client-supplied claim without re-verification
- Refresh tokens are opaque, stored server-side, and must be revocable by deleting/invalidating the stored row — there is no way to "expire early" a stateless JWT, which is exactly why the refresh token isn't one
- Access and refresh tokens live only in `HttpOnly`, `Secure`, `SameSite` cookies — never in a response body read by JS, never in `localStorage`, never in a URL query param

## Input handling
- Parameterized queries always, regardless of which DB access pattern is chosen — no string-concatenated SQL, ever
- Validate/sanitize at the API boundary using the OpenAPI schema (reject malformed input before it reaches business logic)
- Let React's default escaping handle output; avoid `dangerouslySetInnerHTML` — if a feature seems to need it (e.g. rendering task descriptions with formatting), sanitize server-side first and treat this as an explicit, reviewed exception

## Secrets
- Config only, never hardcoded or committed — env vars or a secret store, per `backend-design.md`
- Never logged: redact tokens, passwords, and API keys in structured log output, including in error messages that might include a request payload
- Rotatable without a code deploy

## Multi-tenancy boundary
Even though v1 is single-org, every query must be scoped by the `project_id`/`org_id` derived from the authenticated session — never trust a client-supplied `project_id` alone as authorization to access that project's data. This is what keeps the RBAC model meaningful and keeps the codebase ready for a future multi-tenant mode without a rewrite.

## Dependency hygiene
Dependabot should be configured for Go modules, npm packages, and Docker base images, weekly and grouped by ecosystem. Treat its security-advisory PRs as high priority — don't let them sit unreviewed behind feature work.

## Least privilege
- DB users/service accounts scoped to only the permissions they need (the app's DB role shouldn't have superuser)
- RBAC checks happen at the service layer for every state-changing action — a hidden button in the UI is not an authorization control
