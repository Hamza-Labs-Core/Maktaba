-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0048 (Epic 9 / Story 9.11) — voiceprint columns + library scope.
--
ALTER TABLE speakers
    ADD COLUMN IF NOT EXISTS library_id UUID REFERENCES libraries(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE speakers ADD COLUMN IF NOT EXISTS voiceprint BYTEA;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE speakers ADD COLUMN IF NOT EXISTS unknown_index INTEGER;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS speakers_library_idx
    ON speakers (library_id)
    WHERE library_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS speakers_library_idx;
ALTER TABLE speakers DROP COLUMN IF EXISTS unknown_index;
ALTER TABLE speakers DROP COLUMN IF EXISTS voiceprint;
ALTER TABLE speakers DROP COLUMN IF EXISTS library_id;
-- +goose StatementEnd
