-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0223 (Epic 57, story 57.14): a restricted account's time
-- rules, the seconds it watched each home day in each library, and the extra time a
-- guardian gave. Times of day are 'HH:MM:SS' text and days are 'YYYY-MM-DD' text.
--
CREATE TABLE IF NOT EXISTS parental_time_rules (
    id             TEXT     PRIMARY KEY,
    user_id        TEXT     NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    library_id     TEXT     REFERENCES libraries(id) ON DELETE CASCADE,
    days           INTEGER  NOT NULL CHECK (days BETWEEN 1 AND 127),
    slot_start     TEXT,
    slot_end       TEXT,
    daily_minutes  INTEGER  CHECK (daily_minutes IS NULL OR daily_minutes BETWEEN 0 AND 1440),
    created_by     TEXT     REFERENCES users(id) ON DELETE SET NULL,
    updated_at     TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK ((slot_start IS NULL) = (slot_end IS NULL) AND (slot_start IS NULL OR slot_start <> slot_end)),
    CHECK (slot_start IS NOT NULL OR daily_minutes IS NOT NULL)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS parental_time_rules_user_idx ON parental_time_rules (user_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_time_usage (
    user_id     TEXT     NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    day         TEXT     NOT NULL,
    library_id  TEXT     NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    seconds     INTEGER  NOT NULL DEFAULT 0 CHECK (seconds >= 0),
    PRIMARY KEY (user_id, day, library_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_time_grants (
    id            TEXT     PRIMARY KEY,
    user_id       TEXT     NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    day           TEXT     NOT NULL,
    minutes       INTEGER  NOT NULL CHECK (minutes BETWEEN 1 AND 1440),
    used_seconds  INTEGER  NOT NULL DEFAULT 0 CHECK (used_seconds >= 0 AND used_seconds <= minutes * 60),
    granted_by    TEXT     REFERENCES users(id) ON DELETE SET NULL,
    created_at    TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS parental_time_grants_user_day_idx ON parental_time_grants (user_id, day);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS parental_time_grants;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_time_usage;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_time_rules;
-- +goose StatementEnd
