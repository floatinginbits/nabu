-- +goose Up
CREATE TYPE task_status AS ENUM ('todo', 'in_progress', 'done');

CREATE TABLE tasks (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title      text NOT NULL,
    status     task_status NOT NULL DEFAULT 'todo',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Backs cursor pagination (created_at, id descending). Created inline rather
-- than CONCURRENTLY because the table is created empty in this same
-- migration; later indexes on live tables follow the CONCURRENTLY /
-- NO TRANSACTION convention in backend-design.md.
CREATE INDEX tasks_created_at_id_idx ON tasks (created_at DESC, id DESC);

-- +goose Down
DROP TABLE tasks;
DROP TYPE task_status;
