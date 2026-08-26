-- +goose Up
-- One OIDC client per organization: ownscale acts as the OIDC provider for
-- that organization's headscale and headplane containers (see
-- auth/oidcprovider and dockerctl). client_secret_hash is
-- a bcrypt hash; the plaintext secret is only ever handed to the
-- containers being provisioned, never persisted here.
CREATE TABLE oidc_clients (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    client_id TEXT NOT NULL UNIQUE,
    client_secret_hash TEXT NOT NULL,
    redirect_uris TEXT NOT NULL, -- JSON array of allowed redirect URIs
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX oidc_clients_organization_id_idx ON oidc_clients (organization_id);

-- +goose Down
DROP TABLE IF EXISTS oidc_clients;
