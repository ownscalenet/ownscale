-- name: CreateInvite :one
INSERT INTO invites (id, organization_id, email, role, token, created_by, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetInviteByToken :one
SELECT * FROM invites WHERE token = ?;

-- name: GetInvite :one
SELECT * FROM invites WHERE id = ?;

-- name: ListInvitesByOrganization :many
SELECT * FROM invites WHERE organization_id = ? AND accepted_at IS NULL ORDER BY created_at;

-- name: MarkInviteAccepted :exec
UPDATE invites SET accepted_at = CURRENT_TIMESTAMP, accepted_by_user_id = ? WHERE id = ?;

-- name: DeleteInvite :exec
DELETE FROM invites WHERE id = ?;
