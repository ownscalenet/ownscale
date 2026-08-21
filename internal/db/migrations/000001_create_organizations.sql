-- +goose Up
CREATE TABLE organizations (
    id TEXT PRIMARY KEY,
    subdomain TEXT NOT NULL UNIQUE,
    container_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);


-- +goose Down
DROP TABLE organizations;
