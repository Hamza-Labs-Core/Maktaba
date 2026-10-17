-- +goose Up
-- SQLite parity sibling for slot 0098: no trigram indexes; title search scans
-- with LIKE. Nothing to create.
SELECT 1;

-- +goose Down
SELECT 1;
