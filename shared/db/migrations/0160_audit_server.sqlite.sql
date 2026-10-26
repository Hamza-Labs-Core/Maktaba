-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0160 (Epic 45, story 45.3). SQLite's audit_log has
-- no category check (slot 0054): nothing to widen.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
