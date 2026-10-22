-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0146 (Epic 38, follow-up of slot 0145): the second
-- listen's tries counted from their end (ended_at, wait_s), the model and the operator's
-- round they were made in, and the models and capabilities the home has seen. Timestamps
-- as TEXT, JSON as TEXT.
--
ALTER TABLE relisten_attempts ADD COLUMN IF NOT EXISTS ended_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE relisten_attempts ADD COLUMN IF NOT EXISTS wait_s REAL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE relisten_attempts ADD COLUMN IF NOT EXISTS model TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE relisten_attempts ADD COLUMN IF NOT EXISTS relisten_round INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS relisten_seen (
    kind        TEXT NOT NULL,
    key         TEXT NOT NULL,
    detail      TEXT NOT NULL DEFAULT '{}',
    first_seen  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
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
