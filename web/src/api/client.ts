import createClient from "openapi-fetch";

import type { paths } from "./schema";

// Same-origin: the Go binary serves both the SPA and /api/v1 in deployment,
// and the Vite dev server proxies /api to it in development.
export const client = createClient<paths>({ baseUrl: "/" });
