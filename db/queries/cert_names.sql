-- name: CertNameExists :one
SELECT EXISTS(SELECT 1 FROM cert_names WHERE organization_id = ? AND name = ?);

-- name: CountCertNamesThisWeek :one
SELECT COUNT(*) FROM cert_names
WHERE organization_id = ? AND first_seen_at > datetime('now', '-7 days');

-- name: CreateCertName :exec
INSERT INTO cert_names (organization_id, name) VALUES (?, ?);

-- name: DeleteCertName :exec
DELETE FROM cert_names WHERE organization_id = ? AND name = ?;
