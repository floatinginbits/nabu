# 0002 — Frontend styling: shadcn/ui (Tailwind + Radix)

- **Status:** Accepted
- **Date:** 2026-07-02

## Context
Nabu's frontend is React + TypeScript on Vite. Four things shape the styling/component choice:

- **Accessibility is an enterprise requirement.** `docs/conventions/frontend-design.md` commits to keyboard-operable interactions, focus management, and passing accessibility audits. The hardest part of that is interactive primitives — dialogs, dropdowns, comboboxes, menus — with correct ARIA and keyboard behavior.
- **"Opinionated, guided UI" is a product principle**, and "simple UI for now" means velocity matters for the MVP.
- **No-lock-in / self-hosted / OSS ethos** — owning our code is preferred over depending on a component library's release cycle.
- **Contributor familiarity** — it is an open-source project.

The candidates were CSS Modules (scoped plain CSS, no design system or components), Tailwind CSS (utility classes + design tokens, but no components), and shadcn/ui (accessible components built on Radix primitives + Tailwind, distributed as source you copy into your own repo).

## Decision
We will use **shadcn/ui** — which in practice means **Tailwind CSS for styling and Radix UI for interactive primitives**, with shadcn as the starting scaffold whose component source we copy into our tree and own.

Two scoping decisions are part of this choice, not afterthoughts:

1. **shadcn covers app chrome and forms** — dialogs, menus, inputs, toasts, comboboxes, and the like. The **task board is a separate, deliberately-owned subsystem**: drag-and-drop comes from a dedicated library (e.g. `dnd-kit`), and its accessible keyboard-operable interaction is our own pattern, not something shadcn/Radix provides.
2. **Theme tokens (CSS variables) are set up from day one**, even for the initial simple UI, so branding and dark mode are not a later cross-component retrofit.

## Consequences
Easier: accessible interactive primitives out of the box (the biggest error-prone area, solved by Radix); a consistent design system and token scale immediately; high velocity for the MVP; components live in our repo, so we modify them freely with no forced upgrade churn and no runtime dependency on a component library's cadence.

Harder: it commits us to Tailwind's utility-class model and to Radix as a foundation — this is not a neutral pick, the coupling *is* the commitment. Copied-in components are ours to maintain and update. And the choice deliberately does not cover the board, our most complex screen.

### Limitations and risks
From a pre-mortem, ranked by likelihood × impact for Nabu:

1. **shadcn doesn't touch the board, and the a11y win doesn't reach it.** The Kanban/Scrum/backlog boards are the core, most-visible screen and the hardest interaction. shadcn/Radix provides none of the drag-and-drop, and keyboard-accessible DnD — the single hardest a11y problem in the app — is not something Radix solves. The accessibility rationale that justified this stack does not automatically extend to the screen that needs it most.
2. **The copy-in model is a security/patch blind spot.** Vendored component source is not an npm dependency, so `npm audit` and the Dependabot setup in `security-baseline.md` do not cover it. When upstream ships a component-level a11y or security fix, there is no automated signal — someone must know to re-pull. This contradicts our stated security posture unless handled deliberately.
3. **Tailwind version churn and contributor barrier.** We are pinned to a coordinated set (Tailwind + Radix + `cva` + `tailwind-merge`); a future Tailwind major becomes our migration across every vendored component, with no upstream to lean on. Utility-class markup also raises the entry barrier for CSS-literate-but-not-Tailwind contributors, against the "good first issue" goal.
4. **The Radix foundation is one we don't control.** Our a11y bet rests on Radix; if its maintenance stalls, swapping the primitive layer means rewriting every interactive component.
5. **Generic look / opinion collision.** shadcn ships with its own recognizable defaults; a distinct Nabu identity means restyling components (eroding the velocity that justified the choice).

### Mitigations
Each is an up-front action, tracked in `TASKS.md`:

- **Prototype the accessible, keyboard-operable board first**, before the stack is spread everywhere — same discipline as prototyping the task-list query in ADR-0001. If accessible DnD can't be built cleanly, the a11y rationale weakens and we reconsider early. (Risk 1)
- **Document the boundary in `frontend-design.md`:** shadcn for chrome/forms, the board as an owned subsystem with its own accessible-DnD pattern. (Risk 1)
- **Close the security gap:** track which shadcn components were pulled and at what upstream version, and add a recurring manual review of Radix/shadcn advisories to `security-baseline.md`, since Dependabot won't see copied code. (Risk 2)
- **Establish theme tokens (CSS variables) on day one** so branding/dark mode isn't a retrofit. (Risk 5)

**Kill criterion.** If we end up restyling the *majority* of shadcn components to fit Nabu's identity (losing the velocity that justified it), or the accessible board can't be built on a compatible foundation, the value proposition has collapsed. Fall back to Tailwind + Radix used directly (or a lighter headless primitive set) without the shadcn copy-in layer — cheaper than it sounds, since Tailwind and Radix are the substrate either way and only the scaffold is shed.

## Alternatives considered
- **Tailwind CSS alone** — gives the token system and has the largest contributor pool, but no components: every accessible dialog/menu/combobox is hand-built, which is exactly the error-prone work we want to avoid. Remains the fallback substrate if the shadcn scaffold is shed (see kill criterion).
- **CSS Modules** — scoped plain CSS, universally familiar, but provides neither a design-token system nor components nor accessible primitives; slowest to build the opinionated, consistent UI the product calls for, and puts all accessibility work on us.
