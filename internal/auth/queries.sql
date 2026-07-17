-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (family_id, user_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetRefreshTokenByHashForUpdate :one
-- Locks the row for the rotation transaction so two concurrent refreshes of
-- the same token serialize (ADR-0003).
SELECT * FROM refresh_tokens
WHERE token_hash = $1
FOR UPDATE;

-- name: MarkRefreshTokenReplaced :exec
-- replaced_at is passed explicitly (not now()) so it shares the transaction's
-- clock with the grace-window check in the repository.
UPDATE refresh_tokens
SET replaced_by = @successor_id, replaced_at = @replaced_at
WHERE id = @id;

-- name: RevokeRefreshTokenFamily :exec
-- Revokes every not-yet-revoked token in a family in one statement; used on
-- reuse detection inside the rotation transaction.
UPDATE refresh_tokens
SET revoked_at = now()
WHERE family_id = $1 AND revoked_at IS NULL;

-- name: RevokeRefreshTokenFamilyByHash :exec
-- Logout: revoke the whole family the presented token belongs to. A missing
-- hash matches no family, so logout is idempotent.
UPDATE refresh_tokens
SET revoked_at = now()
WHERE family_id = (SELECT rt.family_id FROM refresh_tokens rt WHERE rt.token_hash = $1)
  AND revoked_at IS NULL;
