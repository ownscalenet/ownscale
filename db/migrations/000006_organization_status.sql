-- +goose Up
ALTER TABLE organizations ADD COLUMN status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'active', 'failed'));

-- Every organization that already exists at the time this migration runs
-- was created by the old fully-synchronous provisioning path, so it's
-- already provisioned; only orgs created after this migration can start
-- out 'pending'.
UPDATE organizations SET status = 'active';

-- +goose Down
ALTER TABLE organizations DROP COLUMN status;
