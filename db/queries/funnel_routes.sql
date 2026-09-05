-- name: CreateFunnelRoute :one
INSERT INTO funnel_routes (id, organization_id, label, target_node_id, target_port, created_by)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListFunnelRoutesByOrg :many
SELECT * FROM funnel_routes WHERE organization_id = ? ORDER BY created_at;

-- name: GetFunnelRoute :one
SELECT * FROM funnel_routes WHERE id = ? AND organization_id = ?;

-- name: DeleteFunnelRoute :exec
DELETE FROM funnel_routes WHERE id = ? AND organization_id = ?;

-- name: GetFunnelRouteBySubdomainAndLabel :one
SELECT funnel_routes.* FROM funnel_routes
JOIN organizations ON organizations.id = funnel_routes.organization_id
WHERE organizations.subdomain = ? AND funnel_routes.label = ?;
