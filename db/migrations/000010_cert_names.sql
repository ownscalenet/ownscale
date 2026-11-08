-- +goose Up
-- Names that got an ACME challenge, so renewals, which Let's Encrypt doesn't
-- rate limit, aren't counted as new certificates.
CREATE TABLE cert_names (
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    first_seen_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (organization_id, name)
);

-- +goose Down
DROP TABLE cert_names;
