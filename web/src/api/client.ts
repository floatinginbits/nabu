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

/**
 * `rejected` — the server authoritatively refused the token; `unavailable` —
 * the attempt never got an answer (5xx, offline, DNS). Only the first is a
 * statement about the session, so only the first may latch.
 */
type RefreshOutcome = "ok" | "rejected" | "unavailable";

let inFlight: Promise<RefreshOutcome> | null = null;
// Survives past inFlight settling. Clearing inFlight in .finally() alone only
// dedupes 401s arriving *during* the refresh window; the sequence that trips
// the server's token-reuse detection — which revokes the whole family — is
// staggered: A 401s, refresh fails, latch clears, B 401s 50ms later and fires
// a second refresh.
let refreshFailed = false;

/** Called on login and logout — the only things that clear the failure latch. */
export function resetAuthState() {
  inFlight = null;
  refreshFailed = false;
}

function refreshSession(): Promise<RefreshOutcome> {
  if (inFlight) return inFlight;

  const attempt = (async (): Promise<RefreshOutcome> => {
    try {
      // Exactly /api/v1/auth/refresh: the refresh cookie is scoped
      // Path=/api/v1/auth, so any other spelling omits the cookie and every
      // session dies at the access token's TTL, looking like a token bug.
      const { response } = await client.POST("/api/v1/auth/refresh");
      if (response.ok) return "ok";
      // A 502 from a deploy or a proxy hiccup says nothing about the token.
      // Latching on it would strand every open tab out of a live session until
      // the user logged in again, which reads as a random logout.
      if (response.status !== 401 && response.status !== 403) {
        return "unavailable";
      }
      refreshFailed = true;
      return "rejected";
    } catch {
      // Thrown, not answered: offline, DNS, connection reset.
      return "unavailable";
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
//
// Entries are removed in onResponse/onError, which openapi-fetch guarantees to
// run for every onRequest — but only while this is the sole registered
// middleware. A second one that short-circuits by returning a Response from its
// onRequest would skip ours and leak an entry per call.
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

    const outcome = await refreshSession();
    if (outcome !== "ok") {
      // Only a rejection is evidence the session is gone. On `unavailable` the
      // caller sees the original 401 and a later request can try again.
      if (outcome === "rejected") onUnauthenticated?.();
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
