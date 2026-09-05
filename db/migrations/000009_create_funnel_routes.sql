-- +goose Up
CREATE TABLE funnel_routes (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    target_node_id TEXT NOT NULL,
    target_port INTEGER NOT NULL,
    created_by TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(organization_id, label)
);

-- +goose Down
DROP TABLE funnel_routes;
