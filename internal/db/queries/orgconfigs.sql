-- name: CreateOrgConfig :one
INSERT INTO orgconfigs (id, organization_id, container_type, path, content)
VALUES (?, ?, ?, ?, ?)
RETURNING *;
