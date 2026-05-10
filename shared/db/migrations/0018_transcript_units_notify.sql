-- +goose Up
-- +goose StatementBegin
--
-- Slot 0018 (Story 5.3 / plan-05-03) — `transcript_units.committed`
-- NOTIFY trigger.
--
-- Lets the live indexer (slot 0025) and the chapter inferer wake up
-- without polling. The Chroma worker drains units with
-- `indexed_at_in_chroma IS NULL` (column from slot 0025).
--
CREATE OR REPLACE FUNCTION transcript_units_committed_notify() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify(
        'transcript_units.committed',
        json_build_object(
            'unit_id',       NEW.id,
            'transcript_id', NEW.transcript_id,
            'seq',           NEW.seq
        )::text
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS transcript_units_committed_notify_trg ON transcript_units;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER transcript_units_committed_notify_trg
    AFTER INSERT ON transcript_units
    FOR EACH ROW
    EXECUTE FUNCTION transcript_units_committed_notify();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS transcript_units_committed_notify_trg ON transcript_units;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS transcript_units_committed_notify();
-- +goose StatementEnd
