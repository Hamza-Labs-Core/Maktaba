-- +goose Up
-- +goose StatementBegin
--
-- Slot 0022 (Story 5.2 / plan-05-02) — SQLite FTS5 sync triggers.
--
-- `arabic_normalize()` is registered as a SQLite user function by
-- `pipeline/src/maktaba_pipeline/search/fts/sqlite.py` before the
-- application opens the connection. The triggers below fall back to
-- the raw text if the function is not registered (e.g. tooling
-- inspection from outside the app), at the cost of slightly less
-- aggressive matching.
--
DROP TRIGGER IF EXISTS transcript_units_fts_ai;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER transcript_units_fts_ai AFTER INSERT ON transcript_units BEGIN
    INSERT INTO transcripts_fts (rowid, text, transcript_id, unit_id, language)
    VALUES (NEW.id, NEW.text, NEW.transcript_id, NEW.id, NEW.language);
END;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS transcript_units_fts_ad;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER transcript_units_fts_ad AFTER DELETE ON transcript_units BEGIN
    DELETE FROM transcripts_fts WHERE rowid = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS transcript_units_fts_au;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER transcript_units_fts_au AFTER UPDATE ON transcript_units BEGIN
    DELETE FROM transcripts_fts WHERE rowid = OLD.id;
    INSERT INTO transcripts_fts (rowid, text, transcript_id, unit_id, language)
    VALUES (NEW.id, NEW.text, NEW.transcript_id, NEW.id, NEW.language);
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS transcript_units_fts_au;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS transcript_units_fts_ad;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS transcript_units_fts_ai;
-- +goose StatementEnd
