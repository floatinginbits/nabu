-- +goose Up
CREATE TABLE audit_logs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Nullable, and SET NULL rather than CASCADE: an audit row has to outlive
    -- the user it describes, or deleting an account erases its own trail. A
    -- failed login has no user at all (ADR-0004).
    actor_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    org_id      uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    -- Nullable: org-level actions (login, logout) belong to no project.
    project_id  uuid REFERENCES projects (id) ON DELETE SET NULL,
    action      text NOT NULL,
    entity_type text NOT NULL,
    -- Nullable: a failed login names no entity that exists.
    entity_id   uuid,
    metadata    jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Inline rather than CONCURRENTLY: the table is created empty in this same
-- migration, so there is nothing to lock — backend-design.md's CONCURRENTLY
-- rule is about indexing tables that already hold data. DESC because "who
-- changed what in this org, most recent first" is the read this table exists
-- for; org_id leads because every read is org-scoped (ADR-0005).
CREATE INDEX audit_logs_org_created_at_idx ON audit_logs (org_id, created_at DESC);

-- +goose Down
DROP TABLE audit_logs;
