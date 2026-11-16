-- Tailnet domains are now directly under the content domain, so a label must
-- not equal any organization's subdomain.
-- +goose Up
UPDATE organizations SET tailnet_label = 'net' || lower(hex(randomblob(3)))
WHERE tailnet_label IN (SELECT subdomain FROM organizations);

-- +goose Down
SELECT 1;
