-- +goose Up
-- +goose StatementBegin
--
-- Slot 0102 (plan 03-10) - transcription survives a worker restart. A transcription
-- worker decodes ~30 files side by side, so a restart used to throw away up to an hour
-- of decoding per worker; the transcribe stage now saves each file's progress about
-- once a minute (how far it has decoded, the sentences so far) and a new attempt
-- resumes from it. The fingerprint is the job's input fingerprint: a checkpoint of
-- another file, model or version is not used. Removed when the transcript is stored.
--
CREATE TABLE IF NOT EXISTS transcribe_checkpoints (
    video_id     uuid PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    fingerprint  text NOT NULL,
    seek_frames  integer NOT NULL CHECK (seek_frames >= 0),
    last_speech  real NOT NULL DEFAULT 0,
    language     text,
    segments     jsonb NOT NULL DEFAULT '[]'::jsonb,
    updated_at   timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS transcribe_checkpoints;
-- +goose StatementEnd
