-- +goose Up
-- +goose StatementBegin
--
-- Slot 0157 (Epic 16, story 16.8) - feature-flag resolution: the admin's overrides and
-- the beta cohorts. A flag and its default are declared in the API binary
-- (api/internal/flags); these rows only change what a user's bundle says.
--
-- `scope_value` is NULL for a global override, a tier name, a cohort name or a user
-- id; its shape per scope is checked here. A user-scoped row names its user in
-- `scope_value` (text, not a reference): a deleted user's override applies to no one.
-- `value` is a JSON scalar (the API refuses anything else). An override can turn a paid
-- feature off but never on: the license alone grants one (the API's resolver).
--
-- The API keeps each user's resolved bundle for 60 s and drops them all at every write
-- it makes; a home runs one API process, so no NOTIFY trigger here. No secondary index:
-- the tables hold a handful of rows (beta_cohorts' key serves a user's cohorts).
--
-- Not in a machine's copy: slot 0155's cluster_copy_excluded() names both tables.
-- maktaba_worker keeps the default rights (as slot 0154): the rows hold no secret.
--
CREATE TABLE IF NOT EXISTS feature_flag_overrides (
    id           UUID        PRIMARY KEY,
    flag_key     TEXT        NOT NULL,
    scope        TEXT        NOT NULL CHECK (scope IN ('global', 'tier', 'user', 'cohort')),
    scope_value  TEXT,
    value        JSONB       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ,
    created_by   UUID        REFERENCES users(id) ON DELETE SET NULL,
    CONSTRAINT feature_flag_overrides_scope_value_chk CHECK (
        (scope = 'global' AND scope_value IS NULL) OR
        (scope = 'tier'   AND scope_value IN ('free', 'home', 'pro')) OR
        (scope = 'user'   AND scope_value ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') OR
        (scope = 'cohort' AND scope_value ~ '^[a-z0-9_-]{1,64}$')
    )
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS beta_cohorts (
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cohort     TEXT        NOT NULL CHECK (cohort ~ '^[a-z0-9_-]{1,64}$'),
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, cohort)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- audit_log's category check gains 'flags' (every admin write to the overrides and the
-- cohorts is audited under it). The list is read from the constraint as it stands and
-- 'flags' put in front, so a category another slot added is kept whatever the order the
-- slots land in. NOT VALID as before: existing rows are not re-read.
DO $$
DECLARE
    def text;
BEGIN
    SELECT pg_get_constraintdef(c.oid) INTO def
      FROM pg_constraint c
     WHERE c.conname = 'audit_log_category_check' AND c.conrelid = 'audit_log'::regclass;
    IF def IS NOT NULL AND position('''flags''' IN def) = 0 AND position('ARRAY[' IN def) > 0 THEN
        ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_category_check;
        EXECUTE 'ALTER TABLE audit_log ADD CONSTRAINT audit_log_category_check '
            || replace(replace(def, ' NOT VALID', ''), 'ARRAY[', 'ARRAY[''flags''::text, ')
            || ' NOT VALID';
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS beta_cohorts;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS feature_flag_overrides;
-- +goose StatementEnd
