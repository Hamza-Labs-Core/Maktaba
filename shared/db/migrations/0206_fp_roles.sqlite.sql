-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0206: SQLite has no roles; first-party plugin runners need
-- a Postgres home, so this slot changes nothing here.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
