-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0221 (Epic 57, stories 57.02 and 57.04): restricted
-- accounts, their guardians and rules, and each sign-in's age signal. SQLite has no
-- NOTIFY: a single process reads the tables directly.
--
CREATE TABLE IF NOT EXISTS parental_policies (
    user_id     TEXT     PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    max_tier    INTEGER  NOT NULL CHECK (max_tier IN (-1, 0, 9, 13, 16, 18)),
    unrated     TEXT     NOT NULL DEFAULT 'hide'
        CHECK (unrated IN ('hide', 'tier_0', 'tier_9', 'tier_13', 'tier_16', 'tier_18')),
    allow_only  INTEGER  NOT NULL DEFAULT 0,
    version     INTEGER  NOT NULL DEFAULT 1,
    updated_by  TEXT     REFERENCES users(id) ON DELETE SET NULL,
    updated_at  TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_guardians (
    child_id     TEXT  NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    guardian_id  TEXT  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_by   TEXT  REFERENCES users(id) ON DELETE SET NULL,
    created_at   TEXT  NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (child_id, guardian_id),
    UNIQUE (guardian_id, child_id),
    CHECK (child_id <> guardian_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_rules (
    id            TEXT  PRIMARY KEY,
    user_id       TEXT  NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    effect        TEXT  NOT NULL CHECK (effect IN ('allow', 'block')),
    subject_kind  TEXT  NOT NULL
        CHECK (subject_kind IN ('library', 'collection', 'series', 'channel', 'youtube_source', 'plugin', 'video')),
    subject_id    TEXT  NOT NULL CHECK (subject_id <> ''),
    expires_at    TEXT,
    created_by    TEXT  REFERENCES users(id) ON DELETE SET NULL,
    created_at    TEXT  NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (user_id, subject_kind, subject_id, effect)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS session_age_signals (
    refresh_family_id  TEXT     PRIMARY KEY,
    user_id            TEXT     NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    lower              INTEGER,
    upper              INTEGER,
    declaration        TEXT,
    movie_ceiling      INTEGER,
    tv_ceiling         INTEGER,
    region             TEXT,
    tier_limit         INTEGER
        CHECK (tier_limit IS NULL OR tier_limit IN (-1, 0, 9, 13, 16, 18)),
    pending_looser     TEXT,
    observed_at        TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (user_id, refresh_family_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS parental_policies_guard_admin
BEFORE INSERT ON parental_policies
FOR EACH ROW WHEN (SELECT is_admin FROM users WHERE id = NEW.user_id) = 1
BEGIN
    SELECT RAISE(ABORT, 'parental_restricted_admin');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS parental_policies_guard_guardian
BEFORE INSERT ON parental_policies
FOR EACH ROW WHEN EXISTS (SELECT 1 FROM parental_guardians WHERE guardian_id = NEW.user_id)
BEGIN
    SELECT RAISE(ABORT, 'parental_restricted_guardian');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS users_restricted_admin_guard
BEFORE UPDATE OF is_admin ON users
FOR EACH ROW WHEN NEW.is_admin = 1
    AND EXISTS (SELECT 1 FROM parental_policies WHERE user_id = NEW.id)
BEGIN
    SELECT RAISE(ABORT, 'parental_restricted_admin');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER IF NOT EXISTS parental_guardians_guard
BEFORE INSERT ON parental_guardians
FOR EACH ROW WHEN EXISTS (SELECT 1 FROM parental_policies WHERE user_id = NEW.guardian_id)
BEGIN
    SELECT RAISE(ABORT, 'parental_restricted_guardian');
END;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO app_settings (key, value) VALUES ('parental.enabled', 'true');
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO app_settings (key, value) VALUES ('parental.unrated_on_signal', '"hide"');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM app_settings WHERE key IN ('parental.enabled', 'parental.unrated_on_signal');
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS users_restricted_admin_guard;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS session_age_signals;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_rules;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_guardians;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS parental_policies;
-- +goose StatementEnd
