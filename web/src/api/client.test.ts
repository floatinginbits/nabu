import { afterEach, expect, test, vi } from "vitest";

// The refresh single-flight promise and the sticky failure latch are
// module-level, so they leak between cases in this file. Every case reloads
// the module rather than sharing one instance.
async function loadClient() {
  vi.resetModules();
  return import("./client");
}

function unauthorized(): Response {
  return new Response(
    JSON.stringify({
      error: { code: "UNAUTHORIZED", message: "session expired" },
    }),
    { status: 401, headers: { "Content-Type": "application/json" } },
  );
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const emptyTaskList = { data: [], nextCursor: null };

interface StubOptions {
  refreshSucceeds?: boolean;
  /** Status for a failing refresh: 401/403 is a verdict, anything else isn't. */
  refreshFailureStatus?: number;
  /** Number of leading /api/v1/tasks calls answered with a 401. */
  unauthorizedTaskCalls?: number;
}

function stubFetch({
  refreshSucceeds = true,
  refreshFailureStatus = 401,
  unauthorizedTaskCalls = 1,
}: StubOptions = {}) {
  const postedTaskBodies: unknown[] = [];
  const taskRequestHeaders: (string | null)[] = [];
  let taskCalls = 0;

  const fetchMock = vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;

    if (path === "/api/v1/auth/refresh") {
      if (refreshSucceeds) return new Response(null, { status: 204 });
      return refreshFailureStatus === 401
        ? unauthorized()
        : new Response(null, { status: refreshFailureStatus });
    }
    if (path === "/api/v1/auth/login") {
      return unauthorized();
    }
    if (path === "/api/v1/tasks") {
      taskCalls += 1;
      taskRequestHeaders.push(request.headers.get("X-Nabu-Csrf"));
      // Read the body before deciding the status, as a real server does: that
      // is what disturbs the stream and makes an uncloned retry fail here.
      if (request.method === "POST")
        postedTaskBodies.push(await request.json());
      if (taskCalls <= unauthorizedTaskCalls) return unauthorized();
      if (request.method === "POST") return jsonResponse({ id: "t1" }, 201);
      return jsonResponse(emptyTaskList);
    }
    throw new Error(`unstubbed request: ${request.method} ${path}`);
  });

  vi.stubGlobal("fetch", fetchMock);

  const refreshCalls = () =>
    fetchMock.mock.calls.filter(
      ([request]) => new URL(request.url).pathname === "/api/v1/auth/refresh",
    ).length;

  return { fetchMock, refreshCalls, postedTaskBodies, taskRequestHeaders };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

test("a 401 triggers one refresh and the retried request succeeds", async () => {
  const { refreshCalls } = stubFetch();
  const { client } = await loadClient();

  const { data, error } = await client.GET("/api/v1/tasks");

  expect(error).toBeUndefined();
  expect(data).toEqual(emptyTaskList);
  expect(refreshCalls()).toBe(1);
});

test("a failed refresh surfaces the original 401 and does not refresh twice", async () => {
  const { refreshCalls } = stubFetch({ refreshSucceeds: false });
  const { client, setOnUnauthenticated } = await loadClient();
  const onUnauthenticated = vi.fn();
  setOnUnauthenticated(onUnauthenticated);

  const { error, response } = await client.GET("/api/v1/tasks");

  expect(response.status).toBe(401);
  expect(error?.error.code).toBe("UNAUTHORIZED");
  expect(refreshCalls()).toBe(1);
  expect(onUnauthenticated).toHaveBeenCalledTimes(1);
});

test("concurrent 401s share a single refresh", async () => {
  const { refreshCalls } = stubFetch({ unauthorizedTaskCalls: 2 });
  const { client } = await loadClient();

  const results = await Promise.all([
    client.GET("/api/v1/tasks"),
    client.GET("/api/v1/tasks"),
  ]);

  for (const result of results) {
    expect(result.error).toBeUndefined();
    expect(result.data).toEqual(emptyTaskList);
  }
  expect(refreshCalls()).toBe(1);
});

test("a 401 arriving after a failed refresh issues no further refresh", async () => {
  const { refreshCalls } = stubFetch({
    refreshSucceeds: false,
    unauthorizedTaskCalls: Infinity,
  });
  const { client } = await loadClient();

  await client.GET("/api/v1/tasks");
  expect(refreshCalls()).toBe(1);

  // Staggered, so the single-flight promise has long since settled and been
  // cleared: only the sticky latch can suppress this one.
  await new Promise((resolve) => setTimeout(resolve, 50));
  const second = await client.GET("/api/v1/tasks");

  expect(second.response.status).toBe(401);
  expect(refreshCalls()).toBe(1);
});

test("an unavailable refresh neither signs the user out nor latches", async () => {
  const { refreshCalls } = stubFetch({
    refreshSucceeds: false,
    refreshFailureStatus: 502,
    unauthorizedTaskCalls: Infinity,
  });
  const { client, setOnUnauthenticated } = await loadClient();
  const onUnauthenticated = vi.fn();
  setOnUnauthenticated(onUnauthenticated);

  await client.GET("/api/v1/tasks");
  // A proxy hiccup is no evidence about the token, so the session stands and
  // the next 401 is still allowed to try.
  expect(onUnauthenticated).not.toHaveBeenCalled();

  await new Promise((resolve) => setTimeout(resolve, 50));
  await client.GET("/api/v1/tasks");

  expect(refreshCalls()).toBe(2);
  expect(onUnauthenticated).not.toHaveBeenCalled();
});

test("the retried request keeps the CSRF header", async () => {
  const { taskRequestHeaders, refreshCalls } = stubFetch();
  const { client } = await loadClient();

  const { error } = await client.GET("/api/v1/tasks");

  expect(error).toBeUndefined();
  expect(refreshCalls()).toBe(1);
  // The retry replays a clone of the original Request; a hand-rebuilt one would
  // drop the header and be rejected as CSRF_REQUIRED on any non-GET.
  expect(taskRequestHeaders).toEqual(["1", "1"]);
});

test("a 401 from login never triggers a refresh", async () => {
  const { refreshCalls } = stubFetch();
  const { client } = await loadClient();

  const { response } = await client.POST("/api/v1/auth/login", {
    body: { email: "admin@example.com", password: "wrong" },
  });

  expect(response.status).toBe(401);
  expect(refreshCalls()).toBe(0);
});

test("a retried POST replays its body", async () => {
  const { postedTaskBodies, refreshCalls } = stubFetch();
  const { client } = await loadClient();

  const body = {
    title: "Dogfood Nabu",
    projectId: "8f1e0c2a-5b3d-4e6f-9a7b-1c2d3e4f5a6b",
  };
  const { error } = await client.POST("/api/v1/tasks", { body });

  expect(error).toBeUndefined();
  expect(refreshCalls()).toBe(1);
  // Once on the 401, once on the retry. The request handed to onResponse has a
  // disturbed body stream; only the clone stashed in onRequest can be replayed.
  expect(postedTaskBodies).toEqual([body, body]);
});
