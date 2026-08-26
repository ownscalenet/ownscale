-- +goose Up
ALTER TABLE users ADD COLUMN email_verified INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN last_login_at DATETIME;

-- +goose Down
ALTER TABLE users DROP COLUMN last_login_at;
ALTER TABLE users DROP COLUMN email_verified;
