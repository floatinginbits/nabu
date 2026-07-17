-- +goose Up
-- Opaque refresh tokens, stored as SHA-256 hashes (ADR-0003). A family_id
-- groups every token descended from one login, so reuse of a rotated token
-- can revoke the whole family in one statement. replaced_by records the
-- rotation chain (active-session/audit story); replaced_at drives the
-- concurrent-refresh grace check with a single-row read.
CREATE TABLE refresh_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    family_id   uuid NOT NULL,
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash  bytea NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    revoked_at  timestamptz,
    replaced_by uuid REFERENCES refresh_tokens (id) ON DELETE SET NULL,
    replaced_at timestamptz
);

-- The lookup path is by token hash; uniqueness also enforces that a 256-bit
-- random value never collides.
CREATE UNIQUE INDEX refresh_tokens_token_hash_idx ON refresh_tokens (token_hash);

-- Family revocation and the worker's expiry sweep both scan by these.
CREATE INDEX refresh_tokens_family_id_idx ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_expires_at_idx ON refresh_tokens (expires_at);

-- +goose Down
DROP TABLE refresh_tokens;
