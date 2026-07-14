# Frontend Design Principles

React + TypeScript conventions for Nabu, beyond the baseline in `CLAUDE.md`. This is a living document — expect it to firm up once real components exist.

## Folder structure (proposed)

```
src/
  api/          generated OpenAPI client + thin wrapper hooks (useTasks, useProjects, ...)
  components/   shared, presentational components with no feature-specific logic
  features/     one folder per domain concept (tasks, sprints, boards, auth) —
                feature-local components, hooks, and state live here
  routes/       route-level components (one per page), compose features/components
  hooks/        cross-feature hooks (useAuth, useTheme) — if a hook is feature-specific,
                it lives in that feature folder instead
  lib/          framework-agnostic utilities (formatting, validation helpers)
```

A component, its test, and its styles are colocated (`TaskCard.tsx`, `TaskCard.test.tsx`) rather than split into parallel `components/` and `__tests__/` trees.

## Component conventions
- One component per file, filename matches the component name (PascalCase)
- Props typed via an explicit `interface`, not inline object types, so they're referenceable elsewhere
- Presentational components (`components/`) take data and callbacks as props and own no server state; feature components own the data-fetching and pass down props
- Prefer composition over configuration: a component that's growing a long list of boolean props is usually two components

## State management
- Local UI state: `useState`/`useReducer`, no global store for anything that doesn't need to be global
- Server state (tasks, projects, sprints): fetched through the generated API client (`openapi-typescript` + `openapi-fetch`, generated from `api/openapi.yaml` — see `api-contract.md`), with **TanStack Query** (confirmed with the M1 scaffold) as the caching layer on top, so loading/error/refetch states aren't reimplemented per component. Wrapper hooks (`useTasks`, `useCreateTask`, ...) live in `src/api`
- Truly global client state (current user/session, theme) is the only thing that belongs in app-level context

## Views over the unified task model
Kanban board, Scrum board, and backlog + milestones all read the same task entity — they differ in grouping and filtering, not in shape. Build one set of task primitives (`TaskCard`, `TaskList`, `TaskDetail`) and compose views around them rather than duplicating task rendering per view.

## Opinionated UI
Per the product decisions in `ARCHITECTURE.md`: flexibility lives in the data model, not in UI configuration. Default to the sensible behavior; don't add a settings toggle to avoid making a UX decision. If two workflows genuinely need different behavior, that's a data-model question (is this a per-project setting?) before it's a component question.

## Accessibility baseline
- Semantic HTML first; ARIA only to fill real gaps — for interactive primitives (dialogs, menus, comboboxes) use the shadcn/Radix components rather than hand-rolling ARIA
- Board drag-and-drop must have a keyboard-operable equivalent (e.g. a "move to..." menu), not just pointer drag — this is the board subsystem's own responsibility (Radix does not cover DnD), so build and test it deliberately
- Color is never the only signal for status (pair with icon/label) — enterprise users will run this through accessibility audits

## Styling
shadcn/ui — Tailwind CSS for styling, Radix UI for interactive primitives, with shadcn component source copied into our tree and owned ([ADR-0002](../adr/0002-frontend-styling.md)). Don't introduce a second styling approach ad hoc.

**Scope boundary:** shadcn covers app chrome and forms (dialogs, menus, inputs, toasts, comboboxes). The **task board is a separate, owned subsystem** — drag-and-drop comes from a dedicated library (e.g. `dnd-kit`) and its accessible keyboard interaction is our own pattern, not something shadcn/Radix provides. Don't assume shadcn covers the board.

**Theme tokens:** define colors, spacing, and radii as CSS variables from the start, even for the initial simple UI, so branding and dark mode aren't a later cross-component retrofit.

**Maintaining copied components:** shadcn components live in our repo, so `npm audit`/Dependabot don't cover them. Record which components were pulled and at what upstream version, and watch Radix/shadcn advisories manually (see `security-baseline.md`).
