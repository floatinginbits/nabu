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
- Server state (tasks, projects, sprints): fetched through the generated API client; recommend a request-caching layer (e.g. TanStack Query) on top of it rather than hand-rolled `useEffect` fetching, so loading/error/refetch states aren't reimplemented per component — **not yet confirmed, treat as a proposal**
- Truly global client state (current user/session, theme) is the only thing that belongs in app-level context

## Views over the unified task model
Kanban board, Scrum board, and backlog + milestones all read the same task entity — they differ in grouping and filtering, not in shape. Build one set of task primitives (`TaskCard`, `TaskList`, `TaskDetail`) and compose views around them rather than duplicating task rendering per view.

## Opinionated UI
Per the product decisions in `HANDOFF.md`: flexibility lives in the data model, not in UI configuration. Default to the sensible behavior; don't add a settings toggle to avoid making a UX decision. If two workflows genuinely need different behavior, that's a data-model question (is this a per-project setting?) before it's a component question.

## Accessibility baseline
- Semantic HTML first; ARIA only to fill real gaps
- Board drag-and-drop must have a keyboard-operable equivalent (e.g. a "move to..." menu), not just pointer drag
- Color is never the only signal for status (pair with icon/label) — enterprise users will run this through accessibility audits

## Styling
Open decision — see `HANDOFF.md` (Tailwind vs CSS Modules vs shadcn/ui). Don't introduce a styling approach ad hoc; flag it for that decision to be made once, then follow it everywhere.
