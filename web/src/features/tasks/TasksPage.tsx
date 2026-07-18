import { useState } from "react";
import type { FormEvent } from "react";

import { useCreateTask, useProjects, useTasks } from "@/api/hooks";
import type { components } from "@/api/schema";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

type Task = components["schemas"]["Task"];

// Icon + label pairs: color is never the only status signal
// (frontend-design.md accessibility baseline).
const statusPresentation: Record<
  Task["status"],
  { label: string; icon: string; className: string }
> = {
  todo: { label: "To do", icon: "○", className: "text-muted-foreground" },
  in_progress: {
    label: "In progress",
    icon: "◐",
    className: "text-blue-600 dark:text-blue-400",
  },
  done: {
    label: "Done",
    icon: "●",
    className: "text-green-700 dark:text-green-400",
  },
};

export function TasksPage() {
  const tasks = useTasks();
  const projects = useProjects();
  const createTask = useCreateTask();
  const [title, setTitle] = useState("");
  const [selectedProjectId, setSelectedProjectId] = useState<string | null>(
    null,
  );

  const projectList = projects.data?.data ?? [];
  // The first project stands in until the user picks one, so the form is
  // submittable as soon as the list loads without an effect to seed state.
  const projectId = selectedProjectId ?? projectList[0]?.id ?? "";

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    const trimmed = title.trim();
    if (trimmed === "" || projectId === "") return;
    createTask.mutate(
      { title: trimmed, projectId },
      { onSuccess: () => setTitle("") },
    );
  }

  return (
    <main className="mx-auto max-w-2xl space-y-6 p-8">
      <header>
        <h1 className="text-2xl font-semibold">Nabu</h1>
        <p className="text-muted-foreground">
          Task tracking for software teams
        </p>
      </header>

      <form onSubmit={onSubmit} className="flex gap-2">
        <label htmlFor="project" className="sr-only">
          Project
        </label>
        <select
          id="project"
          value={projectId}
          onChange={(event) => setSelectedProjectId(event.target.value)}
          disabled={projectList.length === 0}
          className="border-input bg-transparent dark:bg-input/30 h-9 rounded-md border px-3 py-1 text-sm shadow-xs focus-visible:ring-[3px] focus-visible:outline-none disabled:opacity-50"
        >
          {projectList.map((project) => (
            <option key={project.id} value={project.id}>
              {project.name}
            </option>
          ))}
        </select>
        <Input
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          placeholder="What needs doing?"
          aria-label="Task title"
        />
        <Button
          type="submit"
          disabled={createTask.isPending || projectId === ""}
        >
          Add task
        </Button>
      </form>
      {createTask.isError && (
        <p role="alert" className="text-destructive text-sm">
          {createTask.error.message}
        </p>
      )}

      {tasks.isPending ? (
        <p className="text-muted-foreground">Loading tasks…</p>
      ) : tasks.isError ? (
        <p role="alert" className="text-destructive">
          {tasks.error.message}
        </p>
      ) : tasks.data.data.length === 0 ? (
        <p className="text-muted-foreground">
          No tasks yet — add the first one above.
        </p>
      ) : (
        <ul className="space-y-2">
          {tasks.data.data.map((task) => (
            <TaskItem key={task.id} task={task} />
          ))}
        </ul>
      )}
    </main>
  );
}

interface TaskItemProps {
  task: Task;
}

function TaskItem({ task }: TaskItemProps) {
  const status = statusPresentation[task.status];
  return (
    <li>
      <Card className="py-3">
        <CardContent className="flex items-center justify-between gap-4 px-4">
          <span>{task.title}</span>
          <span className={`shrink-0 text-sm ${status.className}`}>
            <span aria-hidden="true">{status.icon} </span>
            {status.label}
          </span>
        </CardContent>
      </Card>
    </li>
  );
}
