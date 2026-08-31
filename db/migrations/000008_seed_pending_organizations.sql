-- +goose Up

-- Seeds 6 organizations left unprovisioned (status stays at its default,
-- 'pending'), so the background provisioner's startup recovery
-- (orgs.Provisioner.recoverPending) picks them up and provisions them the
-- next time the server starts.
INSERT INTO organizations (id, subdomain) VALUES
    ('seed-aaaa', 'aaaa'),
    ('seed-bbbb', 'bbbb'),
    ('seed-cccc', 'cccc'),
    ('seed-dddd', 'dddd'),
    ('seed-eeee', 'eeee'),
    ('seed-ffff', 'ffff');

-- +goose Down
DELETE FROM organizations WHERE subdomain IN ('aaaa', 'bbbb', 'cccc', 'dddd', 'eeee', 'ffff');
