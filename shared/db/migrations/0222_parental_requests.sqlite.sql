-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0222 (Epic 57, stories 57.06 and 57.08): a child's
-- requests to a guardian, the parent PINs and the wrong tries at them, and a child's
-- activity. SQLite has no machine copies, so no exclusion list.
--
CREATE TABLE IF NOT EXISTS parental_requests (
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
CREATE UNIQUE INDEX IF NOT EXISTS parental_requests_one_pending
    ON parental_requests (child_id, subject_kind, subject_id) WHERE status = 'pending';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS parental_requests_by_status_idx
    ON parental_requests (status, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_pins (
    guardian_id   TEXT     PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    pin_hash      TEXT     NOT NULL,
    failed        INTEGER  NOT NULL DEFAULT 0,
    locks         INTEGER  NOT NULL DEFAULT 0,
    locked_until  TEXT,
    updated_at    TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_pin_failures (
    id           TEXT  PRIMARY KEY,
    child_id     TEXT  NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    guardian_id  TEXT  REFERENCES users(id) ON DELETE SET NULL,
    at           TEXT  NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS parental_pin_failures_child_idx
    ON parental_pin_failures (child_id, at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_events (
    id            TEXT  PRIMARY KEY,
    child_id      TEXT  NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    kind          TEXT  NOT NULL
        CHECK (kind IN ('watched', 'blocked', 'asked', 'approved', 'denied', 'pin_locked', 'signal',
                        'time_up', 'time_granted')),
    subject_kind  TEXT  NOT NULL DEFAULT '',
    subject_id    TEXT  NOT NULL DEFAULT '',
    actor_id      TEXT  REFERENCES users(id) ON DELETE SET NULL,
    dedupe        TEXT,
    at            TEXT  NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS parental_events_at_idx ON parental_events (at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS parental_events_child_idx
    ON parental_events (child_id, at DESC, id DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS parental_events_once
    ON parental_events (child_id, kind, dedupe) WHERE dedupe IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS parental_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_pin_failures;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_pins;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_requests;
-- +goose StatementEnd
