-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0223 (Epic 57, story 57.14): a restricted account's time
-- rules, the seconds it watched each home day in each library, and the extra time a
-- guardian gave. Times of day are 'HH:MM:SS' text and days are 'YYYY-MM-DD' text.
--
-- Slot 0222's parental_requests gains the request kind 'time'. SQLite cannot change a
-- CHECK, so the table is built again with the new one and its rows and two indexes are
-- carried over (nothing refers to parental_requests).
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

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_requests_0223;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_requests_0223 (
    id            TEXT  PRIMARY KEY,
    child_id      TEXT  NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    subject_kind  TEXT  NOT NULL
        CHECK (subject_kind IN ('library', 'collection', 'series', 'channel', 'youtube_source', 'plugin', 'video', 'time')),
    subject_id    TEXT  NOT NULL CHECK (subject_id <> ''),
    status        TEXT  NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'denied', 'expired')),
    decided_by    TEXT  REFERENCES users(id) ON DELETE SET NULL,
    decided_at    TEXT,
    created_at    TEXT  NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO parental_requests_0223 (id, child_id, subject_kind, subject_id, status, decided_by, decided_at, created_at)
SELECT id, child_id, subject_kind, subject_id, status, decided_by, decided_at, created_at
  FROM parental_requests;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_requests;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE parental_requests_0223 RENAME TO parental_requests;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS parental_requests_one_pending
    ON parental_requests (child_id, subject_kind, subject_id) WHERE status = 'pending';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS parental_requests_by_status_idx
    ON parental_requests (status, created_at);
-- +goose StatementEnd

-- +goose Down
-- The requests for more time go (slot 0222's check allows none), and the table is built
-- again with slot 0222's check.
-- +goose StatementBegin
DROP TABLE IF EXISTS parental_requests_0223;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_requests_0223 (
    id            TEXT  PRIMARY KEY,
    child_id      TEXT  NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    subject_kind  TEXT  NOT NULL
        CHECK (subject_kind IN ('library', 'collection', 'series', 'channel', 'youtube_source', 'plugin', 'video')),
    subject_id    TEXT  NOT NULL CHECK (subject_id <> ''),
    status        TEXT  NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'denied', 'expired')),
    decided_by    TEXT  REFERENCES users(id) ON DELETE SET NULL,
    decided_at    TEXT,
    created_at    TEXT  NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO parental_requests_0223 (id, child_id, subject_kind, subject_id, status, decided_by, decided_at, created_at)
SELECT id, child_id, subject_kind, subject_id, status, decided_by, decided_at, created_at
  FROM parental_requests
 WHERE subject_kind <> 'time';
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_requests;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE parental_requests_0223 RENAME TO parental_requests;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS parental_requests_one_pending
    ON parental_requests (child_id, subject_kind, subject_id) WHERE status = 'pending';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS parental_requests_by_status_idx
    ON parental_requests (status, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_time_grants;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_time_usage;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_time_rules;
-- +goose StatementEnd
