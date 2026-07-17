import createClient from "openapi-fetch";

import type { paths } from "./schema";

// Same-origin: the Go binary serves both the SPA and /api/v1 in deployment,
// and the Vite dev server proxies /api to it in development. The origin is
// explicit (not a bare "/") because Node's fetch — used by Vitest — rejects
// relative Request URLs that browsers would accept.
export const client = createClient<paths>({
  baseUrl: window.location.origin,
  // Resolve fetch per call, not at client creation, so tests can stub it.
  fetch: (request) => globalThis.fetch(request),
  // The server requires this header on every state-changing /api request: a
  // cross-site attacker cannot set it without a preflight we reject, which is
  // what makes the cookie-borne session safe from CSRF (ADR-0003). Sending it
  // on safe methods too costs nothing and keeps the rule in one place.
  headers: { "X-Nabu-Csrf": "1" },
});
