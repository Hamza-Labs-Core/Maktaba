-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0190 (a restart's seq only goes up). SQLite has no
-- joined machines (no roles): only the seq rule applies.
--
CREATE TRIGGER IF NOT EXISTS component_restarts_seq_up
BEFORE UPDATE OF seq ON component_restarts
FOR EACH ROW WHEN NEW.seq < OLD.seq
BEGIN
    SELECT RAISE(ABORT, 'component_restarts: seq cannot go down');
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS component_restarts_seq_up;
-- +goose StatementEnd
