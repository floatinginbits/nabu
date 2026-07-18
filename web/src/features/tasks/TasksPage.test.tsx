import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";

import type { components } from "@/api/schema";

import { TasksPage } from "./TasksPage";

type Task = components["schemas"]["Task"];
type Project = components["schemas"]["Project"];
type CreateTaskRequest = components["schemas"]["CreateTaskRequest"];

const projects: Project[] = [
  makeProject("GEN", "General"),
  makeProject("PLAT", "Platform"),
];

function makeProject(key: string, name: string): Project {
  return {
    id: crypto.randomUUID(),
    key,
    name,
    createdAt: "2026-07-15T10:00:00Z",
    updatedAt: "2026-07-15T10:00:00Z",
  };
}

function makeTask(
  title: string,
  status: Task["status"] = "todo",
  projectId: string = projects[0].id,
): Task {
  return {
    id: crypto.randomUUID(),
    projectId,
    title,
    status,
    createdAt: "2026-07-15T10:00:00Z",
    updatedAt: "2026-07-15T10:00:00Z",
  };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// Routes fetch calls the way the real API would respond; tests own the task
// list state.
function stubApi(initialTasks: Task[]) {
  const tasks = [...initialTasks];
  // A Request body can only be read once, so record it here rather than
  // re-reading it off the recorded call in an assertion.
  const createRequests: CreateTaskRequest[] = [];
  const fetchMock = vi.fn(async (input: Request) => {
    const request = input;
    const path = new URL(request.url).pathname;
    if (path === "/api/v1/projects" && request.method === "GET") {
      return jsonResponse({ data: projects });
    }
    if (path === "/api/v1/tasks" && request.method === "GET") {
      return jsonResponse({ data: tasks, nextCursor: null });
    }
    if (path === "/api/v1/tasks" && request.method === "POST") {
      const body = (await request.json()) as CreateTaskRequest;
      createRequests.push(body);
      const { title, projectId } = body;
      if (title === "reject me") {
        return jsonResponse(
          { error: { code: "VALIDATION_ERROR", message: "title is required" } },
          422,
        );
      }
      const task = makeTask(title, "todo", projectId);
      tasks.unshift(task);
      return jsonResponse(task, 201);
    }
    throw new Error(`unstubbed request: ${request.method} ${path}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return createRequests;
}

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <TasksPage />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

test("renders tasks from the API with status labels", async () => {
  stubApi([makeTask("Write the docs"), makeTask("Ship it", "done")]);
  renderPage();

  expect(await screen.findByText("Write the docs")).toBeInTheDocument();
  expect(screen.getByText("Ship it")).toBeInTheDocument();
  expect(screen.getByText("To do")).toBeInTheDocument();
  expect(screen.getByText("Done")).toBeInTheDocument();
});

test("shows the empty state when there are no tasks", async () => {
  stubApi([]);
  renderPage();

  expect(await screen.findByText(/no tasks yet/i)).toBeInTheDocument();
});

test("creates a task in the first project by default", async () => {
  const createRequests = stubApi([]);
  const user = userEvent.setup();
  renderPage();

  await screen.findByText(/no tasks yet/i);
  await screen.findByRole("option", { name: "General" });
  await user.type(screen.getByLabelText("Task title"), "Dogfood Nabu");
  await user.click(screen.getByRole("button", { name: "Add task" }));

  expect(await screen.findByText("Dogfood Nabu")).toBeInTheDocument();

  expect(createRequests).toEqual([
    { title: "Dogfood Nabu", projectId: projects[0].id },
  ]);

  // Input clears after a successful create.
  expect(screen.getByLabelText("Task title")).toHaveValue("");
});

test("creates a task in the selected project", async () => {
  const createRequests = stubApi([]);
  const user = userEvent.setup();
  renderPage();

  await screen.findByRole("option", { name: "Platform" });
  await user.selectOptions(screen.getByLabelText("Project"), projects[1].id);
  await user.type(screen.getByLabelText("Task title"), "Split the monolith");
  await user.click(screen.getByRole("button", { name: "Add task" }));

  expect(await screen.findByText("Split the monolith")).toBeInTheDocument();

  expect(createRequests).toEqual([
    { title: "Split the monolith", projectId: projects[1].id },
  ]);
});

test("surfaces a validation error from the API", async () => {
  stubApi([]);
  const user = userEvent.setup();
  renderPage();

  await screen.findByText(/no tasks yet/i);
  await screen.findByRole("option", { name: "General" });
  await user.type(screen.getByLabelText("Task title"), "reject me");
  await user.click(screen.getByRole("button", { name: "Add task" }));

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "title is required",
  );
  // Input keeps its value so the user can fix and resubmit.
  expect(screen.getByLabelText("Task title")).toHaveValue("reject me");
});
