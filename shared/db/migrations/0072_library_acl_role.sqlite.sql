-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0072. SQLite ALTER TABLE ADD COLUMN
-- accepts a constant DEFAULT and a column-level CHECK, so the closed
-- vocabulary is enforced identically.
--
ALTER TABLE library_acl
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'read'
    CHECK (role IN ('read', 'write', 'admin'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE library_acl DROP COLUMN role;
-- +goose StatementEnd
