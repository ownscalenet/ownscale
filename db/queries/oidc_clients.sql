-- name: CreateOIDCClient :one
INSERT INTO oidc_clients (id, organization_id, client_id, client_secret_hash, redirect_uris)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetOIDCClientByClientID :one
SELECT * FROM oidc_clients WHERE client_id = ?;

-- name: GetOIDCClientByOrganizationID :one
SELECT * FROM oidc_clients WHERE organization_id = ?;
