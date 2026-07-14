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
});
