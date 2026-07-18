-- name: ListProjects :many
SELECT id, org_id, key, name, created_at, updated_at
FROM projects
WHERE org_id = $1
ORDER BY lower(key);

-- name: GetProjectByID :one
SELECT id, org_id, key, name, created_at, updated_at
FROM projects
WHERE id = $1 AND org_id = $2;

-- name: GetSingletonOrgID :one
SELECT id FROM organizations;
