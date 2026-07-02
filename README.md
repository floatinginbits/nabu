# Nabu

> Self-hosted, open-source task tracker for software development teams.

Named after the Babylonian god of writing and scribes, Nabu is built for teams that want full control over their data and infrastructure — no vendor lock-in, no per-seat licensing, no cloud dependency.

## Features

- **Unified task model** — one task model that supports Kanban, Scrum, and backlog workflows via views
- **Sprint management** — time-boxed sprints with story points and progress tracking
- **Role-based access control** — org-level and project-level roles (admin, project lead, contributor, viewer)
- **Git/PR linking** — link pull requests and commits directly to tasks with inline status rendering
- **Full-text search** — fast, typo-tolerant search across all tasks and projects
- **Audit logging** — complete record of all changes for compliance and accountability
- **Observability built-in** — Prometheus metrics and pre-built Grafana dashboards out of the box

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go |
| Frontend | React + TypeScript |
| Database | PostgreSQL |
| Search | Meilisearch |
| Cache / Jobs | Redis |
| Observability | Prometheus + Grafana |
| Deployment | Docker / Kubernetes |

## Quick Start

```bash
# Clone the repository
git clone https://github.com/floatinginbits/nabu.git
cd nabu

# Start the full stack
docker compose up -d

# With observability (Prometheus + Grafana)
docker compose --profile observability up -d
```

Nabu will be available at `http://localhost:3000`.

## Deployment

Nabu is designed to be deployed as a single organization instance:

- Docker Compose (recommended for small teams)
- Kubernetes (recommended for larger organizations)
- Environment variable reference for tuning performance

See [ARCHITECTURE.md](ARCHITECTURE.md#deployment) for the deployment model; a step-by-step operator guide will follow once the stack is scaffolded.

## Contributing

Contributions are welcome. Please open an issue before submitting a pull request for significant changes.

## License

MIT — see [LICENSE](LICENSE) for details.
