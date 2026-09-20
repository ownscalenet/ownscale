-- name: CreateOrganization :one
INSERT INTO organizations (id, subdomain, status)
VALUES (?, ?, ?)
RETURNING *;

-- name: GetOrganizationBySubdomain :one
SELECT * FROM organizations WHERE subdomain = ?;

-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = ?;

-- name: ListOrganizations :many
SELECT * FROM organizations ORDER BY subdomain;

-- name: ListReadyOrganizations :many
SELECT * FROM organizations WHERE status = 'ready' ORDER BY created_at;

-- name: CountActiveOrganizations :one
-- Counts active and ready organizations.
SELECT COUNT(*) FROM organizations WHERE status IN ('active', 'ready');

-- name: UpdateOrganizationStatus :one
UPDATE organizations SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
RETURNING *;

-- name: DeleteOrganization :exec
DELETE FROM organizations WHERE id = ?;
