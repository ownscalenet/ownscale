-- name: CreateUser :one
INSERT INTO users (id, provider, provider_user_id, email, name, avatar_url)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUserByProvider :one
SELECT * FROM users WHERE provider = ? AND provider_user_id = ?;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: SetUserOrganization :exec
UPDATE users SET organization_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: UpdateUserProfile :exec
UPDATE users SET email = ?, name = ?, avatar_url = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;
