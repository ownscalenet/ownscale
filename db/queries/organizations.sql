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

-- name: ListPendingOrganizations :many
SELECT * FROM organizations WHERE status = 'pending' ORDER BY created_at;

-- name: CountActiveOrganizations :one
SELECT COUNT(*) FROM organizations WHERE status = 'active';

-- name: UpdateOrganizationStatus :one
UPDATE organizations SET status = ? WHERE id = ?
RETURNING *;

-- name: DeleteOrganization :exec
DELETE FROM organizations WHERE id = ?;
