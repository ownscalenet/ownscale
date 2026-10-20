-- +goose Up
ALTER TABLE organizations ADD COLUMN tailnet_label TEXT NOT NULL DEFAULT '';
UPDATE organizations SET tailnet_label = 'net' || lower(hex(randomblob(3)));
CREATE UNIQUE INDEX organizations_tailnet_label ON organizations(tailnet_label);

-- +goose Down
DROP INDEX organizations_tailnet_label;
ALTER TABLE organizations DROP COLUMN tailnet_label;
