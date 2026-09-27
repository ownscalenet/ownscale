-- +goose Up
CREATE TABLE organizations (
    id TEXT PRIMARY KEY,
    subdomain TEXT NOT NULL UNIQUE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'update_pending'))
);


-- +goose Down
DROP TABLE organizations;
