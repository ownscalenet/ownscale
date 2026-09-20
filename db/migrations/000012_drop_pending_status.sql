-- +goose NO TRANSACTION
-- +goose Up
-- Organizations are now only ever inserted once fully provisioned, so
-- "pending" and "failed" no longer occur; narrow the CHECK constraint to
-- match. SQLite can't ALTER a CHECK constraint in place, so the table is
-- rebuilt, same as 000011. NO TRANSACTION because PRAGMA foreign_keys
-- only takes effect between transactions.
PRAGMA foreign_keys=OFF;

-- Leftover "pending"/"failed" rows (e.g. from a deploy that crashed
-- before recovering them) never represent a usable organization -- delete
-- them outright rather than trying to force them into the new
-- constraint.
DELETE FROM organizations WHERE status IN ('pending', 'failed');

CREATE TABLE organizations_new (
    id TEXT PRIMARY KEY,
    subdomain TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    status TEXT NOT NULL
        CHECK (status IN ('active', 'ready', 'update_pending'))
);

INSERT INTO organizations_new SELECT * FROM organizations;

DROP TABLE organizations;

ALTER TABLE organizations_new RENAME TO organizations;

PRAGMA foreign_keys=ON;

-- +goose Down
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
