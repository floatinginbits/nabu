import createClient from "openapi-fetch";
import type { Middleware } from "openapi-fetch";

import type { paths } from "./schema";

// Resolve fetch per call, not at client creation, so tests can stub it. The
// 401 retry below goes through this too, or a stubbed fetch would miss it.
const doFetch = (request: Request) => globalThis.fetch(request);

// Same-origin: the Go binary serves both the SPA and /api/v1 in deployment,
// and the Vite dev server proxies /api to it in development. The origin is
// explicit (not a bare "/") because Node's fetch — used by Vitest — rejects
// relative Request URLs that browsers would accept.
export const client = createClient<paths>({
  baseUrl: window.location.origin,
  fetch: doFetch,
  // The server requires this header on every state-changing /api request: a
  // cross-site attacker cannot set it without a preflight we reject, which is
  // what makes the cookie-borne session safe from CSRF (ADR-0003). Sending it
  // on safe methods too costs nothing and keeps the rule in one place.
  headers: { "X-Nabu-Csrf": "1" },
});

let onUnauthenticated: (() => void) | null = null;

/** Registered by AuthProvider; keeps React state out of this module. */
export function setOnUnauthenticated(handler: (() => void) | null) {
  onUnauthenticated = handler;
}

let inFlight: Promise<boolean> | null = null;
// Survives past inFlight settling. Clearing inFlight in .finally() alone only
// dedupes 401s arriving *during* the refresh window; the sequence that trips
// the server's token-reuse detection — which revokes the whole family — is
// staggered: A 401s, refresh fails, latch clears, B 401s 50ms later and fires
// a second refresh.
let refreshFailed = false;

/** A successful login is the only thing that clears the failure latch. */
export function resetAuthState() {
  inFlight = null;
  refreshFailed = false;
}

function refreshSession(): Promise<boolean> {
  if (inFlight) return inFlight;

  const attempt = (async () => {
    try {
      // Exactly /api/v1/auth/refresh: the refresh cookie is scoped
      // Path=/api/v1/auth, so any other spelling omits the cookie and every
      // session dies at the access token's TTL, looking like a token bug.
      const { response } = await client.POST("/api/v1/auth/refresh");
      if (!response.ok) {
        refreshFailed = true;
        return false;
      }
      return true;
    } catch {
      refreshFailed = true;
      return false;
    }
  })();

  inFlight = attempt;
  void attempt.finally(() => {
    if (inFlight === attempt) inFlight = null;
  });
  return attempt;
}

// The Request handed to onResponse has already been sent, so its body stream is
// disturbed and it cannot be replayed. Stash an unsent clone at request time,
// correlated by the middleware's request id.
const pendingRequests = new Map<string, Request>();

const refreshMiddleware: Middleware = {
  onRequest({ request, id }) {
    pendingRequests.set(id, request.clone());
  },
  onError({ id }) {
    pendingRequests.delete(id);
  },
  async onResponse({ response, id, schemaPath }) {
    const original = pendingRequests.get(id);
    pendingRequests.delete(id);

    if (response.status !== 401) return;
    // Matched on schemaPath, not a parsed URL pathname. A 401 from refresh
    // (expired or revoked family) or from login (wrong password) must never
    // trigger a refresh: replaying a rotated token outside the server's grace
    // window revokes the entire token family, so a typo'd password would force
    // a logout and emit a warning that poisons the real token-theft signal.
    if (schemaPath.startsWith("/api/v1/auth/")) return;
    if (refreshFailed || !original) return;

    if (!(await refreshSession())) {
      onUnauthenticated?.();
      return;
    }

    const retried = await doFetch(original);
    if (retried.status === 401) {
      refreshFailed = true;
      onUnauthenticated?.();
    }
    return retried;
  },
};

client.use(refreshMiddleware);
