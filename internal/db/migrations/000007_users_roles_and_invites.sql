-- +goose Up
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'member';
ALTER TABLE users ADD COLUMN headscale_user_id TEXT;

-- Every user that already exists at the time this migration runs is, today,
-- always the sole creator of their organization (there's no invite flow
-- yet), so they're the de facto owner.
UPDATE users SET role = 'owner' WHERE organization_id IS NOT NULL;

-- Prevents linking the same headscale user to two different ownscale users
-- within an organization; NULLs (unlinked) are unconstrained.
CREATE UNIQUE INDEX users_org_headscale_user_idx ON users (organization_id, headscale_user_id)
    WHERE headscale_user_id IS NOT NULL;

CREATE TABLE invites (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email TEXT,
    role TEXT NOT NULL DEFAULT 'member',
    token TEXT NOT NULL UNIQUE,
    created_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL,
    accepted_at DATETIME,
    accepted_by_user_id TEXT REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX invites_organization_id_idx ON invites (organization_id);

-- +goose Down
DROP TABLE IF EXISTS invites;
DROP INDEX IF EXISTS users_org_headscale_user_idx;
ALTER TABLE users DROP COLUMN headscale_user_id;
ALTER TABLE users DROP COLUMN role;
