-- +goose Up
-- +goose StatementBegin
--
-- Slot 0048 (Epic 9 / Story 9.11) — voiceprint columns + library scope.
--
-- The architecture and Story 9.11 require speakers to be scoped per
-- library and to carry a voiceprint d-vector for cosine matching. The
-- existing slot-0035 table is per-video; we extend it with library
-- scope and a voiceprint blob without breaking the existing FK chain.
--
ALTER TABLE speakers ADD COLUMN library_id TEXT REFERENCES libraries(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE speakers ADD COLUMN voiceprint BLOB;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE speakers ADD COLUMN unknown_index INTEGER;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS speakers_library_idx
    ON speakers (library_id)
    WHERE library_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS speakers_library_idx;
ALTER TABLE speakers DROP COLUMN unknown_index;
ALTER TABLE speakers DROP COLUMN voiceprint;
ALTER TABLE speakers DROP COLUMN library_id;
-- +goose StatementEnd
