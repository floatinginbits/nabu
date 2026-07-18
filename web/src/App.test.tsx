import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";

import App from "./App";

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

// Mirrors the server: /users/me reflects whether a session exists, login
// establishes it, logout tears it down.
function stubApi({ authenticated = false } = {}) {
  let session = authenticated;

  const fetchMock = vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    switch (path) {
      case "/api/v1/users/me":
        return session ? jsonResponse(user) : unauthorized();
      case "/api/v1/auth/refresh":
        return unauthorized();
      case "/api/v1/auth/login": {
        const { password } = (await request.json()) as { password: string };
        if (password !== "correct-horse") return unauthorized();
        session = true;
        return jsonResponse(user);
      }
      case "/api/v1/auth/logout":
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

function renderApp() {
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
  renderApp();

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
  renderApp();

  await typist.type(await screen.findByLabelText("Email"), user.email);
  await typist.type(screen.getByLabelText("Password"), "wrong");
  await typist.click(screen.getByRole("button", { name: "Sign in" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Invalid email or password",
  );
});

test("logging in reveals the shell, and logging out returns to login", async () => {
  const fetchMock = stubApi();
  const typist = userEvent.setup();
  const queryClient = renderApp();

  async function signIn() {
    await typist.type(await screen.findByLabelText("Email"), user.email);
    await typist.type(screen.getByLabelText("Password"), "correct-horse");
    await typist.click(screen.getByRole("button", { name: "Sign in" }));
  }
  const taskFetches = () =>
    fetchMock.mock.calls.filter(
      ([request]) => new URL(request.url).pathname === "/api/v1/tasks",
    ).length;

  await signIn();
  expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();
  expect(taskFetches()).toBe(1);

  await typist.click(screen.getByRole("button", { name: "Log out" }));
  expect(await screen.findByLabelText("Email")).toBeInTheDocument();

  // Nothing the outgoing user fetched survives, so the next one cannot be shown
  // their data while a refetch is in flight.
  const leftovers = queryClient
    .getQueryCache()
    .findAll({ predicate: (query) => query.queryKey[0] !== "me" });
  expect(leftovers).toHaveLength(0);

  await signIn();
  expect(await screen.findByText("Ada Lovelace")).toBeInTheDocument();
  expect(taskFetches()).toBe(2);
});
