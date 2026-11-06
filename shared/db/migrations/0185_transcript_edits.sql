-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0185 - word review, part 2 (Epic 52, story 52.9): a reviewer's fix is a transcript
-- edit, never a new speech recognition run.
--
-- 1. transcripts.revision: bumped by every edit and undo; subtitle_gen and word_check
--    include it in their input fingerprints, so the subtitle files and the flags follow.
--    A constant default: metadata-only.
-- 2. transcript_edits: one row per edit (a character span of one sentence, in code
--    points, its text before and after), the flag it resolved, who, when, the batch of a
--    bulk fix, when it was undone, and the edit it was carried over from when the
--    transcript was replaced (transcribe's carry-over).
--
-- Additive: one column, one table, one index.
--
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS revision INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS transcript_edits (
    id            UUID        PRIMARY KEY,
    transcript_id UUID        NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    segment_seq   INTEGER     NOT NULL,
    char_start    INTEGER     NOT NULL,
    char_len      INTEGER     NOT NULL,
    before        TEXT        NOT NULL,
    after         TEXT        NOT NULL,
    flag_id       UUID,
    user_id       UUID,
    batch_id      UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    undone_at     TIMESTAMPTZ,
    carried_from  UUID
);
-- +goose StatementEnd

-- +goose StatementBegin
COMMENT ON TABLE transcript_edits IS
    'Slot 0185 (Epic 52): word fixes to a transcript sentence (character spans in code points), with undo and carry-over';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_edits_segment_idx
    ON transcript_edits (transcript_id, segment_seq);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS transcript_edits_batch_idx
    ON transcript_edits (batch_id) WHERE batch_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- transcribe and the second listen carry the fixes over on any machine
        GRANT SELECT, INSERT, UPDATE ON transcript_edits TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS transcript_edits;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE transcripts DROP COLUMN IF EXISTS revision;
-- +goose StatementEnd
