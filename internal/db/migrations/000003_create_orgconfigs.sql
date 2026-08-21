-- +goose Up
CREATE TABLE orgconfigs (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    container_type TEXT NOT NULL,
    path TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(organization_id, container_type, path)
);


-- +goose Down
DROP TABLE orgconfigs;
