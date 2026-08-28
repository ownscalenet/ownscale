-- name: CreateUser :one
INSERT INTO users (id, provider, provider_user_id, email, name, avatar_url, email_verified)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUserByProvider :one
SELECT * FROM users WHERE provider = ? AND provider_user_id = ?;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;

-- name: SetUserOrganization :exec
UPDATE users SET organization_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: SetUserOrganizationAndRole :exec
UPDATE users SET organization_id = ?, role = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: UpdateUserProfile :exec
UPDATE users SET email = ?, name = ?, avatar_url = ?, email_verified = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: TouchUserLastLogin :exec
UPDATE users SET last_login_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: ListUsersByOrganization :many
SELECT * FROM users WHERE organization_id = ? ORDER BY created_at;

-- name: UpdateUserRole :exec
UPDATE users SET role = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: LinkUserHeadscaleUser :exec
UPDATE users SET headscale_user_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: UnlinkUserHeadscaleUser :exec
UPDATE users SET headscale_user_id = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: RemoveUserFromOrganization :exec
UPDATE users SET organization_id = NULL, role = 'member', headscale_user_id = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: CountOwnersInOrganization :one
SELECT COUNT(*) FROM users WHERE organization_id = ? AND role = 'owner';

-- name: ListUsersWithOrganization :many
SELECT users.*, organizations.subdomain AS org_subdomain
FROM users
LEFT JOIN organizations ON users.organization_id = organizations.id
ORDER BY organizations.subdomain, users.email;
