-- name: CreateOrganization :one
INSERT INTO organizations (id, subdomain)
VALUES (?, ?)
RETURNING *;

-- name: GetOrganizationBySubdomain :one
SELECT * FROM organizations WHERE subdomain = ?;

-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = ?;

-- name: ListOrganizations :many
SELECT * FROM organizations ORDER BY subdomain;

-- name: DeleteOrganization :exec
DELETE FROM organizations WHERE id = ?;
