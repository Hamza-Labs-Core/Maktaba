-- +goose Up
-- +goose StatementBegin
--
-- Slot 0146 (Epic 38, follow-up of slot 0145) - the second listen's tries, counted from
-- their end, and started over when something changes.
--
--   relisten_attempts.ended_at        when the try's job was over (NULL while it runs): the
--                                     wait is counted from here, not from its queueing
--   relisten_attempts.wait_s          that wait (seconds); next_at = ended_at + wait_s
--   relisten_attempts.model           the second-listen model the tries were made with
--   relisten_attempts.relisten_round  rules.transcription.relisten_round when they were made:
--                                     the operator raises it to start them all over
--   relisten_seen                     the second-listen models and capabilities (what a
--                                     machine's second listen runs on: a key and its parts)
--                                     the home has seen: one seen for the first time starts
--                                     the stopped tries over, once
--
-- Workers write both (slot 0103's default privileges). Additive.
--
ALTER TABLE relisten_attempts ADD COLUMN IF NOT EXISTS ended_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE relisten_attempts ADD COLUMN IF NOT EXISTS wait_s DOUBLE PRECISION;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE relisten_attempts ADD COLUMN IF NOT EXISTS model TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE relisten_attempts ADD COLUMN IF NOT EXISTS relisten_round INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS relisten_seen (
    kind        TEXT        NOT NULL,
    key         TEXT        NOT NULL,
    detail      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    first_seen  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, key)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS relisten_seen;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE relisten_attempts DROP COLUMN IF EXISTS relisten_round;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE relisten_attempts DROP COLUMN IF EXISTS model;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE relisten_attempts DROP COLUMN IF EXISTS wait_s;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE relisten_attempts DROP COLUMN IF EXISTS ended_at;
-- +goose StatementEnd
