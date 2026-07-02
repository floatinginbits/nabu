# Git Workflow

Branching and commit conventions for Nabu, expanding on the decisions in `HANDOFF.md`.

## Branching model
Trunk-based development: `master` is always deployable, all work happens on short-lived branches off `master`, merged back frequently. No long-lived `develop` or per-release branches — a release is just a tag on `master` (see Release Process in `HANDOFF.md`).

**Branch naming:** `<type>/<short-description>`, where `<type>` is one of:
- `feature/` — new functionality
- `fix/` — bug fix
- `chore/` — tooling, deps, non-functional maintenance
- `docs/` — documentation only

e.g. `feature/task-board-dnd`, `fix/refresh-token-race`. Keep the description short and specific; the branch name isn't the changelog.

**Branch lifetime:** short means days, not weeks. If a branch is still open after a week, that's a signal to either merge what's done and follow up separately, or split the work — not to keep accumulating on one branch. There's no hotfix-branch process distinct from this: a production fix is just a `fix/` branch merged through the same PR flow, fast-tracked by priority rather than by process.

**Keeping a branch current:** if `master` has moved since your branch started and you need the update (or hit a conflict), merge `master` into your branch — don't rebase. Since PRs squash-merge (below), your branch's internal commit history disappears at merge time anyway, so there's no value in keeping it clean via rebase, only risk in rewriting history you might be sharing with a collaborator or an agent working the same branch.

**Force-pushing:** never on `master` (enforced by branch protection). On your own feature branch, force-pushing after a local amend/reset is fine as long as you're not overwriting a collaborator's or agent's in-flight commits on that same branch — check before you push if you're not sure you're the only one working it.

## Merge strategy
**Squash merge** on every PR into `master`. The PR's commits collapse into a single commit on `master`, using the PR title as the commit message — this is why the PR title must itself be a valid Conventional Commit (see below). Benefits:
- `master` history is linear and one-commit-per-change, which is what the changelog generator (Conventional Commits → CHANGELOG) reads
- Commit hygiene during a branch's lifetime doesn't matter — "wip", "fix typo", "actually fix it" commits are fine, they never reach `master`
- `git bisect` on `master` lands you on one complete, reviewed change per step

## Commit and PR title format
Conventional Commits: `<type>[(scope)]: <description>`

**Types:** `feat`, `fix`, `chore`, `docs`, `refactor` (matches `HANDOFF.md`)

**Scope** (optional but encouraged): a backend package name (`internal/task` → `task`) or frontend feature folder (`src/features/board` → `board`). Omit it for changes that don't map to one area (e.g. a repo-wide `chore:`).

Examples:
- `feat(task): add cursor pagination to list endpoint`
- `fix(auth): refresh token race on concurrent requests`
- `docs: document git workflow conventions`
- `chore: bump golangci-lint to v2`

**Breaking changes:** append `!` after the type/scope (`feat(api)!: rename status field`) and explain the break in the PR description — since PRs squash to a single commit, put the `BREAKING CHANGE:` footer in the squash commit message (edit it at merge time if GitHub's default doesn't include it).

Since PR titles become `master`'s commit messages, get the title right before merging, not just the branch name.

## PR description
Every PR includes:
- **What changed** — one or two sentences, the *why* more than the *what* (the diff shows what)
- **How to test** — steps to verify, or "covered by tests in X" if that's sufficient
- **Screenshots** — for any UI change
- **Linked issue** — `Closes #123` if applicable, so the issue auto-closes on merge

Keep PRs small and scoped to one concern — a PR that mixes a feature with an unrelated refactor is harder to review and harder to revert cleanly.

## Agent-authored commits
The subagents in `.claude/agents/` and any local Claude Code session follow this same workflow: work on a properly prefixed branch, Conventional Commits format, never commit directly to `master`. Commits authored with agent assistance get a `Co-Authored-By:` trailer identifying the agent, same as any other tool-assisted commit.
