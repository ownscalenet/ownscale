-- +goose NO TRANSACTION
-- +goose Up
-- SQLite can't ALTER a CHECK constraint in place, so the organizations
-- table is rebuilt with the widened status list. This runs outside a
-- transaction (NO TRANSACTION) because PRAGMA foreign_keys only takes
-- effect between transactions.
PRAGMA foreign_keys=OFF;

CREATE TABLE organizations_new (
    id TEXT PRIMARY KEY,
    subdomain TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'active', 'failed', 'update_pending', 'ready'))
);

INSERT INTO organizations_new SELECT * FROM organizations;

DROP TABLE organizations;

ALTER TABLE organizations_new RENAME TO organizations;

PRAGMA foreign_keys=ON;

-- +goose Down
PRAGMA foreign_keys=OFF;

-- The narrower constraint below can't hold "ready" rows; they're
-- pool-provisioned organizations, so "active" is the closest equivalent.
UPDATE organizations SET status = 'active' WHERE status = 'ready';

CREATE TABLE organizations_new (
    id TEXT PRIMARY KEY,
    subdomain TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'active', 'failed', 'update_pending'))
);

INSERT INTO organizations_new SELECT * FROM organizations;

DROP TABLE organizations;

ALTER TABLE organizations_new RENAME TO organizations;

PRAGMA foreign_keys=ON;
