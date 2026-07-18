-- +goose Up
CREATE TABLE organizations (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- v1 is single-org (ARCHITECTURE.md). The column exists on every scoped table
-- so multi-tenancy becomes a feature rather than a backfill; this index makes
-- the singleton a schema guarantee instead of a convention nobody enforces.
CREATE UNIQUE INDEX organizations_singleton_idx ON organizations ((true));

INSERT INTO organizations (name) VALUES ('Default');

CREATE TABLE projects (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    key        text NOT NULL,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Case-insensitive like users.email (data-model.md): 'gen' and 'GEN' are the
-- same project key to anyone typing one.
CREATE UNIQUE INDEX projects_org_key_idx ON projects (org_id, lower(key));

-- Seeded unconditionally, not as a side effect of the tasks backfill below: a
-- fresh install has zero tasks and still needs a project to create the first
-- task in.
INSERT INTO projects (org_id, key, name)
SELECT id, 'GEN', 'General' FROM organizations;

ALTER TABLE tasks ADD COLUMN project_id uuid REFERENCES projects (id) ON DELETE CASCADE;

UPDATE tasks SET project_id = (SELECT id FROM projects WHERE lower(key) = 'gen');

-- Plain SET NOT NULL rather than a NOT VALID CHECK: goose runs this file in one
-- transaction, so ADD COLUMN already holds ACCESS EXCLUSIVE across the UPDATE
-- either way, and the CHECK form leaves pg_attribute.attnotnull false — which
-- makes sqlc generate uuid.NullUUID for a column that is always populated.
ALTER TABLE tasks ALTER COLUMN project_id SET NOT NULL;

-- +goose Down
ALTER TABLE tasks DROP COLUMN project_id;
DROP TABLE projects;
DROP TABLE organizations;
