# 0003 — Session and token design for local authentication

- **Status:** Accepted
- **Date:** 2026-07-15

## Context
ARCHITECTURE.md fixes the shape of authentication: a short-lived JWT access token validated statelessly on every request, plus a long-lived opaque refresh token stored in Postgres, both carried in HTTP-only cookies, designed so a future SAML/SSO integration mints the same token pair without an auth-layer rebuild. That leaves a set of concrete decisions the architecture does not pin down — signing algorithm, token lifetimes, what the claims carry, how refresh rotation and revocation work, and how cookie-based auth defends against CSRF. This ADR records them so the next domains (RBAC, task CRUD) build on a settled contract rather than re-deriving it.

The users domain already landed (migration 00002): `password_hash NOT NULL`, bcrypt cost 12, emails unique on `lower(email)`. This ADR covers everything from a validated credential onward.

## Decision

**JWT library and algorithm.** We will use `github.com/golang-jwt/jwt/v5` with **HS256**, signed by a required `NABU_AUTH_SECRET` (minimum 32 bytes, validated eagerly at startup). Only this service validates its own tokens, so asymmetric signing buys nothing today; a future IdP integration mints tokens through our issuer rather than having a third party verify ours.

**Access token.** 15-minute TTL. Claims are `sub` (user UUID), `iat`, `exp`, and `iss: "nabu"`, validated on every request (signature, expiry, issuer). It carries **no role claims** — RBAC reads current assignments from the database so a role or permission change takes effect on the next request, with no stale-claim window. Cookie `nabu_access`: `HttpOnly; Secure; SameSite=Lax; Path=/`.

**Refresh token.** 32 bytes from `crypto/rand`, base64url-encoded. Postgres stores only its SHA-256 hash (a 256-bit random value needs no slow hash) alongside `user_id`, `family_id`, `expires_at`, `created_at`, `revoked_at`, `replaced_by`, and `replaced_at` (the rotation timestamp the grace check below reads). 30-day TTL, sliding: every refresh rotates to a new token with a fresh 30-day expiry, and there is no absolute session cap in v1 (server-side expiry makes adding one a small change later). Cookie `nabu_refresh`: `HttpOnly; Secure; SameSite=Strict; Path=/api/v1/auth` — it is never sent on ordinary API calls.

**Rotation, reuse detection, and the concurrent-refresh race.** Each refresh marks the presented row replaced and links its successor via `replaced_by`. Presenting an already-replaced token is the stolen-cookie signal: it revokes the whole `family_id`. Two tabs refreshing at once would otherwise trip that detection, so refresh takes the row `FOR UPDATE` and, within a short (10s) grace window, mints the late arrival a *sibling* token in the same family instead of revoking it. It is a sibling rather than a re-issue of the already-minted successor because only the successor's hash is stored — its plaintext is unrecoverable by design, so there is nothing to hand back. The cost is that a token presented twice inside the grace window yields two live sessions in one family; both remain revocable together, and outside the window reuse detection still kills the family.

**Whose clock measures the grace window.** The **app instance's**, not the database's: the instance that rotates stamps `replaced_at` from its own wall clock, and the instance handling the next refresh compares against its own. The window is therefore measured across two clocks whenever more than one instance is running, so the comparison is on the *magnitude* of the difference — a genuinely concurrent refresh reads as milliseconds either side of zero depending on which way the clocks lean, while a stale replay sits far outside the window in either direction. This keeps the policy inputs (`graceWindow`, `now`) injected from the service, which is what makes the state machine testable without manipulating database time. The alternative — letting Postgres own both the stamp and the comparison (`now()` / `clock_timestamp()`), which it could, since the row is already locked `FOR UPDATE` on that database — would be exact under any skew and is the better answer if skew ever proves real. It is deliberately deferred, not overlooked: it costs the injected-clock test seam, and v1 self-hosts as a single app instance where both timestamps come from one process and the question cannot arise. Note the table already mixes clocks: `revoked_at` is stamped by the database while `replaced_at` is stamped by the app.

**Revocation.** Logout marks the family's rows revoked (`revoked_at`) rather than deleting them, so a revoked session stays visible to the audit log and to active-session listing until the worker's prune sweep removes it; force-logout and active-session listing are queries and updates over these rows — which is the entire reason the refresh token is opaque and server-side rather than a second JWT.

**CSRF.** Cookie-borne credentials are sent automatically by the browser, so we defend state-changing requests two ways: `SameSite` on the cookies (Lax on access, Strict on refresh) as the baseline, plus middleware that requires the header `X-Nabu-Csrf` on every non-GET `/api` request. Only its presence is checked, never its value — the protection comes from the header being a custom one at all, since a cross-site attacker cannot attach it without a CORS preflight we reject (we serve no CORS policy, so there is none to grant). The client wrapper sends `X-Nabu-Csrf: 1` on every request. A request that lacks it gets its own error code, `CSRF_REQUIRED`, rather than `FORBIDDEN`, which stays reserved for RBAC denials.

**Local development.** The `Secure` cookie flag defaults on; `NABU_COOKIE_SECURE=false` opts out for plain-HTTP Compose development.

## Consequences
The authentication hot path touches no database: a request carries a JWT that verifies with the in-memory secret, and only login, refresh, and logout hit Postgres. Revocation, force-logout, and active-session visibility all work because the refresh side is stateful. SSO later plugs in as another way to mint the same pair, as ARCHITECTURE.md requires.

### Limitations and risks
- A leaked access token is valid until it expires (≤15 min) and cannot be revoked — the price of stateless validation.
- The refresh table grows one row per rotation until expiry; without cleanup it accumulates unboundedly.
- The grace-window logic is the subtlest code in the design; a bug there either breaks legitimate concurrent refreshes or weakens reuse detection.
- HS256 means the signing secret is symmetric: anyone who can read it can mint valid access tokens, so it must be handled as a first-class secret (config only, never logged, rotatable).

### Mitigations
- The ≤15-min access-token window bounds the damage from a leaked access token; the refresh side stays fully revocable.
- A worker-mode job prunes expired and replaced refresh rows (folded into the M2 worker slice); until the worker lands, the table is small enough that growth is not yet a problem.
- Reuse detection plus family revocation is the leading indicator that a refresh token was stolen; the grace window is covered by explicit concurrent-refresh tests, and its failure mode (a spurious family revocation) logs out a session rather than exposing data.
- `NABU_AUTH_SECRET` is validated for length at startup and never logged; rotating it invalidates outstanding access tokens (acceptable — refresh re-issues within 15 min).

## Alternatives considered
- **Stateless refresh (a second long-lived JWT).** Rejected: it cannot be revoked, which defeats the force-logout and active-session requirements in ARCHITECTURE.md — the exact reason the refresh token is opaque and stored.
- **Asymmetric signing (RS256/EdDSA).** Rejected for v1: no external party verifies our tokens, so the key-management overhead buys nothing until one does. Revisit if an external verifier ever appears.
- **Roles embedded in the access-token claims.** Rejected: it would make role changes take up to a full access-token lifetime to apply and reintroduce a stale-permission window; reading assignments from the DB keeps RBAC immediate.
- **Double-submit CSRF token cookie.** Rejected as redundant given HTTP-only cookies plus a required custom header; the header check achieves the same protection without a readable token cookie.
