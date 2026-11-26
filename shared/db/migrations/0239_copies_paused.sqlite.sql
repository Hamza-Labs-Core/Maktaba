-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0239: Postgres exposes cluster_copies_paused() and
-- configures TCP keepalives on machine replication roles. SQLite has no replication roles
-- or pg_hba.conf, so there is nothing to change; the slot keeps numbering aligned.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
