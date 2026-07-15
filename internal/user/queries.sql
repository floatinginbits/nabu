-- name: CreateUser :one
INSERT INTO users (email, display_name, password_hash)
VALUES ($1, $2, $3)
RETURNING id, email, display_name, password_hash, created_at, updated_at;

-- name: GetUserByEmail :one
SELECT id, email, display_name, password_hash, created_at, updated_at
FROM users
WHERE lower(email) = lower($1);

-- name: GetUserByID :one
SELECT id, email, display_name, password_hash, created_at, updated_at
FROM users
WHERE id = $1;

-- name: CountUsers :one
SELECT count(*) FROM users;
