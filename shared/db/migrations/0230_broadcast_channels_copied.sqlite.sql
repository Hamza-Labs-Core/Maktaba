-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0230 (Epic 50, story 50.23): a machine's copy holds
-- broadcast_channels. A SQLite home has no machines and no copies (slot 0147), so there is
-- nothing to change; the slot exists so the numbering stays the same on both.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
