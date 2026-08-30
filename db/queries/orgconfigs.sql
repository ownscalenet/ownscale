-- name: CreateOrgConfig :one
INSERT INTO orgconfigs (id, organization_id, path, content)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GetOrgConfig :one
SELECT * FROM orgconfigs
WHERE organization_id = ? AND path = ?;

-- name: UpsertOrgConfig :one
INSERT INTO orgconfigs (id, organization_id, path, content)
VALUES (?, ?, ?, ?)
ON CONFLICT(organization_id, path)
DO UPDATE SET content = excluded.content
RETURNING *;
