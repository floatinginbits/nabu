# Architecture Decision Records

An ADR captures a single significant architectural decision: the context that forced it, the choice made, and the consequences accepted. They are immutable once accepted — a decision that changes gets a *new* ADR that supersedes the old one, rather than an edit, so the history of why the system is shaped the way it is stays intact.

`ARCHITECTURE.md` describes the system as it is *now*; ADRs record *why* it got that way. When they appear to conflict, `ARCHITECTURE.md` is the current truth and the ADR is the historical reasoning.

## Format
Each record is `NNNN-short-title.md`, numbered sequentially. Use `0000-template.md` as the starting point. Status is one of `Proposed`, `Accepted`, `Superseded by NNNN`, or `Deprecated`.

## Index
- [0001](0001-database-access-pattern.md) — Database access pattern: sqlc + goose — **Accepted**
- [0002](0002-frontend-styling.md) — Frontend styling: shadcn/ui (Tailwind + Radix) — **Accepted**
- [0003](0003-auth-session-design.md) — Session and token design for local authentication — **Accepted**
- [0004](0004-audit-logging.md) — Best-effort audit logging at the service layer — **Accepted**
- [0005](0005-organization-scoping.md) — Organization scoping: a singleton org from day one — **Accepted**
