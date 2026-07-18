-- +goose NO TRANSACTION
-- +goose Up
-- Backs the project-filtered list query. tasks is live by now, so this follows
-- the CONCURRENTLY convention in backend-design.md.
--
-- tasks_created_at_id_idx (migration 00001) stays: projectId is an *optional*
-- filter, so the default board view sends no project, and a leading-project_id
-- index cannot supply ORDER BY created_at DESC, id DESC when project_id is
-- unconstrained. Measured at 50k rows: sequential scan + top-N sort 14.8 ms
-- against index scan 0.155 ms. Both indexes earn their keep, and
-- TestPostgresListQueryUsesIndex covers one case each.
CREATE INDEX CONCURRENTLY tasks_project_created_at_id_idx
    ON tasks (project_id, created_at DESC, id DESC);

-- +goose Down
-- NO TRANSACTION is file-global in goose, so this direction is non-transactional
-- too: a partial failure leaves an INVALID index to drop by hand.
DROP INDEX CONCURRENTLY tasks_project_created_at_id_idx;
