-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0237: Postgres tables whose replica identity holds a
-- generated column go back to REPLICA IDENTITY DEFAULT. SQLite has no replica identity
-- and no publication (a SQLite home has no machines' copies, slot 0147), so there is
-- nothing to change; the slot exists so the numbering stays the same on both.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
