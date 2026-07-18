-- name: CreateTask :one
INSERT INTO tasks (project_id, title)
VALUES ($1, $2)
RETURNING id, project_id, title, status, created_at, updated_at;

-- name: ListTasks :many
-- The join is the org scope: a task is reachable only through a project in the
-- caller's org, so a client-supplied project_id can narrow the result but never
-- widen it (security-baseline.md).
SELECT t.id, t.project_id, t.title, t.status, t.created_at, t.updated_at
FROM tasks t
JOIN projects p ON p.id = t.project_id
WHERE p.org_id = sqlc.arg('org_id')
  AND (sqlc.narg('project_id')::uuid IS NULL OR t.project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('status')::task_status IS NULL OR t.status = sqlc.narg('status'))
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (t.created_at, t.id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY t.created_at DESC, t.id DESC
LIMIT sqlc.arg('page_size');
