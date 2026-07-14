-- name: CreateTask :one
INSERT INTO tasks (title)
VALUES ($1)
RETURNING id, title, status, created_at, updated_at;

-- name: ListTasks :many
SELECT id, title, status, created_at, updated_at
FROM tasks
WHERE (sqlc.narg('status')::task_status IS NULL OR status = sqlc.narg('status'))
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (created_at, id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('page_size');
