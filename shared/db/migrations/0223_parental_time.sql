-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0223 - time limits and time slots for restricted accounts (Epic 57, story 57.14).
--
--   parental_time_rules   one rule of a restricted account: days (a bitmask, bit 0 Sunday
--                         to bit 6 Saturday, of the home days it covers), an optional time
--                         slot (slot_start to slot_end in home time; slot_end before
--                         slot_start crosses midnight), an optional daily limit in minutes,
--                         and an optional library (the rule then covers only that library).
--                         A rule has a slot, a limit or both.
--   parental_time_usage   the seconds an account watched on one home day (the day ends at
--                         04:00 home time) in one library. The all-zero library id stands
--                         for playback with no library, so the three columns are the key.
--                         library_id has no foreign key: the day's count stays when a
--                         library is deleted.
--   parental_time_grants  more time a guardian gave for one home day: minutes, and
--                         used_seconds, the part already watched.
--
-- Every row hangs off parental_policies, so lifting a restriction removes the account's
-- rules, usage and grants with it. The lookups use the keys' indexes and the two below
-- (new, empty tables). Additive: an older release never reads the tables.
--
-- Slot 0222's parental_requests gains the request kind `time` (a child asks a guardian for
-- more minutes today): its subject_id is the minutes asked for, as text, 1 to 480 (the API
-- checks the number), and approving it writes a parental_time_grants row, not a rule. The
-- check parental_requests_subject_kind_valid is replaced NOT VALID and then validated, so
-- the table is not read under the exclusive lock. An older release never writes `time`.
-- Every statement is harmless twice.
--
CREATE TABLE IF NOT EXISTS parental_time_rules (
    id             uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid         NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    library_id     uuid         REFERENCES libraries(id) ON DELETE CASCADE,
    days           smallint     NOT NULL
        CONSTRAINT parental_time_rules_days_valid CHECK (days BETWEEN 1 AND 127),
    slot_start     time,
    slot_end       time,
    daily_minutes  integer
        CONSTRAINT parental_time_rules_daily_minutes_valid
        CHECK (daily_minutes IS NULL OR daily_minutes BETWEEN 0 AND 1440),
    created_by     uuid         REFERENCES users(id) ON DELETE SET NULL,
    updated_at     timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT parental_time_rules_slot_whole
        CHECK ((slot_start IS NULL) = (slot_end IS NULL) AND (slot_start IS NULL OR slot_start <> slot_end)),
    CONSTRAINT parental_time_rules_not_empty
        CHECK (slot_start IS NOT NULL OR daily_minutes IS NOT NULL)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- An index build that was cut short (the app quit, the machine slept) leaves the index
-- there but INVALID: never used, kept up on every write, and skipped by IF NOT EXISTS
-- below. It is dropped so that the build runs again.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
                WHERE c.relname = 'parental_time_rules_user_idx' AND c.relnamespace = 'public'::regnamespace
                  AND NOT i.indisvalid) THEN
        DROP INDEX IF EXISTS parental_time_rules_user_idx;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS parental_time_rules_user_idx ON parental_time_rules (user_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_time_usage (
    user_id     uuid     NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    day         date     NOT NULL,
    library_id  uuid     NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    seconds     integer  NOT NULL DEFAULT 0
        CONSTRAINT parental_time_usage_seconds_valid CHECK (seconds >= 0),
    PRIMARY KEY (user_id, day, library_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_time_grants (
    id            uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid         NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    day           date         NOT NULL,
    minutes       integer      NOT NULL
        CONSTRAINT parental_time_grants_minutes_valid CHECK (minutes BETWEEN 1 AND 1440),
    used_seconds  integer      NOT NULL DEFAULT 0
        CONSTRAINT parental_time_grants_used_valid CHECK (used_seconds >= 0 AND used_seconds <= minutes * 60),
    granted_by    uuid         REFERENCES users(id) ON DELETE SET NULL,
    created_at    timestamptz  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- An index build that was cut short (the app quit, the machine slept) leaves the index
-- there but INVALID: never used, kept up on every write, and skipped by IF NOT EXISTS
-- below. It is dropped so that the build runs again.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
                WHERE c.relname = 'parental_time_grants_user_day_idx' AND c.relnamespace = 'public'::regnamespace
                  AND NOT i.indisvalid) THEN
        DROP INDEX IF EXISTS parental_time_grants_user_day_idx;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS parental_time_grants_user_day_idx ON parental_time_grants (user_id, day);
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conname = 'parental_requests_subject_kind_valid'
                      AND conrelid = 'parental_requests'::regclass
                      AND pg_get_constraintdef(oid) LIKE '%''time''%') THEN
        ALTER TABLE parental_requests DROP CONSTRAINT IF EXISTS parental_requests_subject_kind_valid;
        ALTER TABLE parental_requests ADD CONSTRAINT parental_requests_subject_kind_valid
            CHECK (subject_kind IN ('library', 'collection', 'series', 'channel', 'youtube_source', 'plugin',
                'video', 'time')) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE parental_requests VALIDATE CONSTRAINT parental_requests_subject_kind_valid;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The requests for more time go first (waiting or answered: slot 0222's check allows none
-- of them), then the check is slot 0222's again. One statement, so it is whole or not done.
DO $$
BEGIN
    DELETE FROM parental_requests WHERE subject_kind = 'time';
    ALTER TABLE parental_requests DROP CONSTRAINT IF EXISTS parental_requests_subject_kind_valid;
    ALTER TABLE parental_requests ADD CONSTRAINT parental_requests_subject_kind_valid
        CHECK (subject_kind IN ('library', 'collection', 'series', 'channel', 'youtube_source', 'plugin', 'video'));
END $$;
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
