-- name: CreateOrgConfig :one
INSERT INTO orgconfigs (id, organization_id, container_type, path, content)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetOrgConfig :one
SELECT * FROM orgconfigs
WHERE organization_id = ? AND container_type = ? AND path = ?;

-- name: UpsertOrgConfig :one
INSERT INTO orgconfigs (id, organization_id, container_type, path, content)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(organization_id, container_type, path)
DO UPDATE SET content = excluded.content
RETURNING *;
