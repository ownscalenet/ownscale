-- name: CreateWebauthnCredential :one
INSERT INTO webauthn_credentials (id, user_id, credential_id, public_key, attestation_type, transports, sign_count, backup_eligible, backup_state, name)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListWebauthnCredentialsByUser :many
SELECT * FROM webauthn_credentials WHERE user_id = ? ORDER BY created_at;

-- name: GetWebauthnCredentialByCredentialID :one
SELECT * FROM webauthn_credentials WHERE credential_id = ?;

-- name: GetWebauthnCredential :one
SELECT * FROM webauthn_credentials WHERE id = ?;

-- name: UpdateWebauthnCredentialSignCount :exec
UPDATE webauthn_credentials SET sign_count = ?, last_used_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: DeleteWebauthnCredential :exec
DELETE FROM webauthn_credentials WHERE id = ?;
