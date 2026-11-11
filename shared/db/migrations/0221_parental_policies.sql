-- +goose Up
-- +goose StatementBegin
--
-- Slot 0221 - restricted accounts and guardians (Epic 57, stories 57.02 and 57.04).
--
--   parental_policies     one row makes a viewer a restricted account: max_tier (-1 nothing
--                         by rating, 0 all ages, 9, 13, 16, 18), unrated (hide, or the tier
--                         unrated content is treated as), allow_only (only allowed items),
--                         version (raised by every change to the policy, its rules or its
--                         guardians; a stale PUT answers 409).
--   parental_guardians    the users an admin named to look after a restricted account.
--                         Admins look after every child without a row. Lifting the
--                         restriction removes the rows.
--   parental_rules        allow or block a library, collection, series, channel, YouTube
--                         source, plugin or video for a restricted account, optionally until
--                         expires_at. created_by is kept as null when that user is deleted.
--   session_age_signals   the age band and Screen Time ceilings a device gave for its sign-in
--                         (a refresh-token family), and the tier limit they map to (story
--                         57.04 writes it).
--   app_settings          parental.enabled, parental.unrated_on_signal.
--
-- Three rules are held by triggers, because each spans two tables:
--   parental_restricted_admin     a user with a policy is not an admin (a policy on an
--                                 admin, or is_admin set on a restricted user, is refused);
--   parental_restricted_guardian  a guardian has no policy, and a user with a policy is no
--                                 one's guardian.
-- Each raises check_violation with that constraint name, which the API answers with 409.
-- Every change to a policy, a rule or a guardian sends NOTIFY parental_policy with the
-- child's id, so each API process drops its cached policy.
--
-- The lookups by guardian and by child use the unique constraints' indexes (new tables: no
-- separate CREATE INDEX). Additive: an older release never reads the tables.
--
CREATE TABLE IF NOT EXISTS parental_policies (
    user_id     uuid         PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    max_tier    smallint     NOT NULL
        CONSTRAINT parental_policies_max_tier_valid CHECK (max_tier IN (-1, 0, 9, 13, 16, 18)),
    unrated     text         NOT NULL DEFAULT 'hide'
        CONSTRAINT parental_policies_unrated_valid
        CHECK (unrated IN ('hide', 'tier_0', 'tier_9', 'tier_13', 'tier_16', 'tier_18')),
    allow_only  boolean      NOT NULL DEFAULT false,
    version     integer      NOT NULL DEFAULT 1,
    updated_by  uuid         REFERENCES users(id) ON DELETE SET NULL,
    updated_at  timestamptz  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_guardians (
    child_id     uuid         NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    guardian_id  uuid         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_by   uuid         REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz  NOT NULL DEFAULT now(),
    PRIMARY KEY (child_id, guardian_id),
    CONSTRAINT parental_guardians_by_guardian UNIQUE (guardian_id, child_id),
    CONSTRAINT parental_guardians_not_self CHECK (child_id <> guardian_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_rules (
    id            uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid         NOT NULL REFERENCES parental_policies(user_id) ON DELETE CASCADE,
    effect        text         NOT NULL
        CONSTRAINT parental_rules_effect_valid CHECK (effect IN ('allow', 'block')),
    subject_kind  text         NOT NULL
        CONSTRAINT parental_rules_subject_kind_valid
        CHECK (subject_kind IN ('library', 'collection', 'series', 'channel', 'youtube_source', 'plugin', 'video')),
    subject_id    text         NOT NULL
        CONSTRAINT parental_rules_subject_id_set CHECK (subject_id <> ''),
    expires_at    timestamptz,
    created_by    uuid         REFERENCES users(id) ON DELETE SET NULL,
    created_at    timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT parental_rules_one_per_subject UNIQUE (user_id, subject_kind, subject_id, effect)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS session_age_signals (
    refresh_family_id  uuid         PRIMARY KEY,
    user_id            uuid         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    lower              smallint,
    upper              smallint,
    declaration        text,
    movie_ceiling      integer,
    tv_ceiling         integer,
    region             text,
    tier_limit         smallint
        CONSTRAINT session_age_signals_tier_limit_valid
        CHECK (tier_limit IS NULL OR tier_limit IN (-1, 0, 9, 13, 16, 18)),
    pending_looser     jsonb,
    observed_at        timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT session_age_signals_by_user UNIQUE (user_id, refresh_family_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- A policy is refused on an admin and on a guardian. The user's row is locked first, so a
-- concurrent change of is_admin (or a guardian row naming this user) waits and is checked
-- against the committed policy.
CREATE OR REPLACE FUNCTION parental_policies_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    admin boolean;
BEGIN
    SELECT is_admin INTO admin FROM users WHERE id = NEW.user_id FOR UPDATE;
    IF admin THEN
        RAISE EXCEPTION 'parental: an admin cannot be restricted'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'parental_restricted_admin';
    END IF;
    IF EXISTS (SELECT 1 FROM parental_guardians WHERE guardian_id = NEW.user_id) THEN
        RAISE EXCEPTION 'parental: a guardian cannot be restricted'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'parental_restricted_guardian';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER parental_policies_guard_trg
    BEFORE INSERT OR UPDATE OF user_id ON parental_policies
    FOR EACH ROW EXECUTE FUNCTION parental_policies_guard();
-- +goose StatementEnd

-- +goose StatementBegin
-- A restricted user is never made an admin. AFTER, so the row is already locked by the
-- update and the policy read sees what is committed.
CREATE OR REPLACE FUNCTION users_restricted_admin_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.is_admin AND EXISTS (SELECT 1 FROM parental_policies WHERE user_id = NEW.id) THEN
        RAISE EXCEPTION 'parental: a restricted account cannot be an admin'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'parental_restricted_admin';
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER users_restricted_admin_guard_trg
    AFTER UPDATE OF is_admin ON users
    FOR EACH ROW WHEN (NEW.is_admin) EXECUTE FUNCTION users_restricted_admin_guard();
-- +goose StatementEnd

-- +goose StatementBegin
-- A restricted user is no one's guardian. The guardian's row is locked as the policy
-- guard locks it, so the two checks cannot pass each other.
CREATE OR REPLACE FUNCTION parental_guardians_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM 1 FROM users WHERE id = NEW.guardian_id FOR UPDATE;
    IF EXISTS (SELECT 1 FROM parental_policies WHERE user_id = NEW.guardian_id) THEN
        RAISE EXCEPTION 'parental: a restricted account cannot be a guardian'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'parental_restricted_guardian';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER parental_guardians_guard_trg
    BEFORE INSERT OR UPDATE OF guardian_id ON parental_guardians
    FOR EACH ROW EXECUTE FUNCTION parental_guardians_guard();
-- +goose StatementEnd

-- +goose StatementBegin
-- NOTIFY parental_policy '<child id>' on every change (one per transaction and child:
-- Postgres folds the repeats).
CREATE OR REPLACE FUNCTION parental_policy_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    child uuid;
BEGIN
    IF TG_TABLE_NAME = 'parental_guardians' THEN
        IF TG_OP = 'DELETE' THEN child := OLD.child_id; ELSE child := NEW.child_id; END IF;
    ELSE
        IF TG_OP = 'DELETE' THEN child := OLD.user_id; ELSE child := NEW.user_id; END IF;
    END IF;
    PERFORM pg_notify('parental_policy', child::text);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER parental_policies_notify_trg
    AFTER INSERT OR UPDATE OR DELETE ON parental_policies
    FOR EACH ROW EXECUTE FUNCTION parental_policy_notify();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER parental_rules_notify_trg
    AFTER INSERT OR UPDATE OR DELETE ON parental_rules
    FOR EACH ROW EXECUTE FUNCTION parental_policy_notify();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER parental_guardians_notify_trg
    AFTER INSERT OR UPDATE OR DELETE ON parental_guardians
    FOR EACH ROW EXECUTE FUNCTION parental_policy_notify();
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO app_settings (key, value)
    VALUES ('parental.enabled', 'true'::jsonb),
           ('parental.unrated_on_signal', '"hide"'::jsonb)
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM app_settings WHERE key IN ('parental.enabled', 'parental.unrated_on_signal');
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS users_restricted_admin_guard_trg ON users;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS users_restricted_admin_guard();
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

-- +goose StatementBegin
DROP FUNCTION IF EXISTS parental_guardians_guard();
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS parental_policies_guard();
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS parental_policy_notify();
-- +goose StatementEnd
