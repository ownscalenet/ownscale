-- name: CreateUser :one
INSERT INTO users (id, provider, provider_user_id, email, name, avatar_url, email_verified)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUserByProvider :one
SELECT * FROM users WHERE provider = ? AND provider_user_id = ?;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: SetUserOrganization :exec
UPDATE users SET organization_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: UpdateUserProfile :exec
UPDATE users SET email = ?, name = ?, avatar_url = ?, email_verified = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: TouchUserLastLogin :exec
UPDATE users SET last_login_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?;
