import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";

// client.ts's single-flight promise and sticky refresh-failure latch are
// module-level state. Sharing one instance across cases makes coverage
// order-dependent — a latch set by the first case silently disables the refresh
// path in every later one — so each case gets a fresh module graph.
async function loadApp() {
  vi.resetModules();
  const { default: App } = await import("./App");
  return App;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function unauthorized(): Response {
  return jsonResponse(
    { error: { code: "UNAUTHORIZED", message: "not authenticated" } },
    401,
  );
}

const user = {
  id: "8f1e0a1c-0000-4000-8000-000000000001",
  email: "admin@example.com",
  displayName: "Ada Lovelace",
};

interface StubOptions {
  authenticated?: boolean;
  /** Answer /users/me with this status instead of a session check. */
  meFails?: number;
  /** Answer login with a proxy's HTML error page rather than our envelope. */
  loginReturnsHtml?: boolean;
  logoutFails?: boolean;
}

// Mirrors the server: /users/me reflects whether a session exists, login
// establishes it, logout tears it down.
function stubApi({
  authenticated = false,
  meFails,
  loginReturnsHtml = false,
  logoutFails = false,
}: StubOptions = {}) {
  let session = authenticated;

  const fetchMock = vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    switch (path) {
      case "/api/v1/users/me":
        if (meFails !== undefined) {
          return jsonResponse(
            { error: { code: "INTERNAL", message: "boom" } },
            meFails,
          );
        }
        return session ? jsonResponse(user) : unauthorized();
      case "/api/v1/auth/refresh":
        return unauthorized();
      case "/api/v1/auth/login": {
        if (loginReturnsHtml) {
          return new Response("<html>502 Bad Gateway</html>", {
            status: 502,
            headers: { "Content-Type": "text/html" },
          });
        }
        const { password } = (await request.json()) as { password: string };
        if (password !== "correct-horse") return unauthorized();
        session = true;
        return jsonResponse(user);
      }
      case "/api/v1/auth/logout":
        if (logoutFails) throw new TypeError("Failed to fetch");
        session = false;
        return new Response(null, { status: 204 });
      case "/api/v1/tasks":
        return jsonResponse({ data: [], nextCursor: null });
      default:
        throw new Error(`unstubbed request: ${request.method} ${path}`);
    }
  });

  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

async function renderApp() {
  const App = await loadApp();
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>,
  );
  return queryClient;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

test("shows the login page when there is no session", async () => {
  const fetchMock = stubApi();
  await renderApp();

  expect(await screen.findByLabelText("Email")).toBeInTheDocument();
  // No task fetch fires before authentication.
  expect(
    fetchMock.mock.calls.filter(
      ([request]) => new URL(request.url).pathname === "/api/v1/tasks",
    ),
  ).toHaveLength(0);
});

test("maps an UNAUTHORIZED login to a credentials message", async () => {
  stubApi();
  const typist = userEvent.setup();
  await renderApp();

  await typist.type(await screen.findByLabelText("Email"), user.email);
  await typist.type(screen.getByLabelText("Password"), "wrong");
  await typist.click(screen.getByRole("button", { name: "Sign in" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Invalid email or password",
  );
});

test("an unreadable error body still maps to a coded message", async () => {
  stubApi({ loginReturnsHtml: true });
  const typist = userEvent.setup();
  await renderApp();

  await typist.type(await screen.findByLabelText("Email"), user.email);
  await typist.type(screen.getByLabelText("Password"), "correct-horse");
  await typist.click(screen.getByRole("button", { name: "Sign in" }));

  // A proxy's HTML page is not our envelope; it must still reach the UI as an
  // ApiError with a code, not as a shape error thrown out of the query fn.
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Something went wrong",
  );
});

test("a 500 from the session check is not treated as being signed out", async () => {
  const fetchMock = stubApi({ meFails: 500 });
  const typist = userEvent.setup();
  await renderApp();

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "connection problem, not a sign-out",
  );
  expect(screen.queryByLabelText("Email")).not.toBeInTheDocument();

  const meCalls = () =>
    fetchMock.mock.calls.filter(
      ([request]) => new URL(request.url).pathname === "/api/v1/users/me",
    ).length;
  const before = meCalls();

  await typist.click(screen.getByRole("button", { name: "Try again" }));
  await vi.waitFor(() => expect(meCalls()).toBe(before + 1));
});

test("logging in reveals the shell, and logging out returns to login", async () => {
  const fetchMock = stubApi();
  const typist = userEvent.setup();
  const queryClient = await renderApp();

  async function signIn() {
    await typist.type(await screen.findByLabelText("Email"), user.email);
    await typist.type(screen.getByLabelText("Password"), "correct-horse");
    await typist.click(screen.getByRole("button", { name: "Sign in" }));
  }
  const pathFetches = (path: string) =>
    fetchMock.mock.calls.filter(
      ([request]) => new URL(request.url).pathname === path,
    ).length;
  const taskFetches = () => pathFetches("/api/v1/tasks");

  // The 401 from the session probe attempts a silent refresh. It only does so
  // on fresh module state: an earlier case's sticky failure latch would
  // suppress it and quietly hollow out this case.
  await screen.findByLabelText("Email");
  expect(pathFetches("/api/v1/auth/refresh")).toBe(1);

  await signIn();
  expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();
  expect(taskFetches()).toBe(1);

  // A namespaced key under "me" holds outgoing-user data like any other query
  // and must not survive into the next session.
  queryClient.setQueryData(["me", "preferences"], { theme: "dark" });

  await typist.click(screen.getByRole("button", { name: "Log out" }));
  expect(await screen.findByLabelText("Email")).toBeInTheDocument();

  // Nothing the outgoing user fetched survives, so the next one cannot be shown
  // their data while a refetch is in flight.
  const leftovers = queryClient.getQueryCache().findAll({
    predicate: (query) =>
      !(query.queryKey.length === 1 && query.queryKey[0] === "me"),
  });
  expect(leftovers).toHaveLength(0);

  await signIn();
  expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();
  expect(taskFetches()).toBe(2);
});

test("a logout whose request fails still signs the user out locally", async () => {
  const fetchMock = stubApi({ authenticated: true, logoutFails: true });
  const typist = userEvent.setup();
  const queryClient = await renderApp();

  expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();
  await typist.click(screen.getByRole("button", { name: "Log out" }));

  expect(await screen.findByLabelText("Email")).toBeInTheDocument();
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "the server didn’t confirm it",
  );
  expect(
    queryClient.getQueryCache().findAll({
      predicate: (query) =>
        !(query.queryKey.length === 1 && query.queryKey[0] === "me"),
    }),
  ).toHaveLength(0);
  expect(
    fetchMock.mock.calls.filter(
      ([request]) => new URL(request.url).pathname === "/api/v1/auth/logout",
    ),
  ).toHaveLength(1);
});
