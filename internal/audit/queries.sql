-- name: InsertAuditLog :exec
INSERT INTO audit_logs (actor_id, org_id, project_id, action, entity_type, entity_id, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7);
