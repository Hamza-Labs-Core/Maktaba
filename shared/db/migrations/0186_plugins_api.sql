-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0186 - plugins: the plugin API's tables (Epic 54, stories 54.04-54.06, 54.08).
--
--   plugin_installs     one row per installed plugin (54.07): its version, digest, folder
--                       and validated manifest. The pipeline's plugin loader reads it; the
--                       API writes it (the lifecycle routes come with 54.07).
--   plugin_grants       what an admin granted a plugin (54.08): capabilities, libraries
--                       (none until an admin ticks one), and a generation that moves on
--                       every change. NOTIFY plugin_grants_changed tells the API's cache.
--   plugin_invocations  one row per running plugin job (54.04 §2): the sha256 of its
--                       token (never the token), the job, subject, libraries and grant
--                       generation it was minted with, its expiry and counters.
--   plugin_data         each plugin's own key-value data (54.04 §4), its own namespace.
--   plugin_writes       every change a plugin made to core data, with before and after,
--                       for "what it wrote", undo per plugin and purge (54.07 §7).
--   plugin_suggestions  changes a plugin suggests for an admin to accept (suggest.add).
--
-- job_stages.stage takes names up to 63 characters (a plugin stage is
-- p_<8 hex>_<name up to 40>, story 54.01 §4); chapters gain the source 'plugin'.
--
-- Only the home's API reads and writes these; maktaba_worker (the joined machines) gets
-- nothing but SELECT on plugin_installs (plugin stages run on the home only, README H12).
--
-- Additive: new tables, a wider check, one more allowed chapter source.
--
ALTER TABLE job_stages DROP CONSTRAINT IF EXISTS job_stages_stage_check;
ALTER TABLE job_stages ADD CONSTRAINT job_stages_stage_check CHECK (stage ~ '^[a-z][a-z0-9_]{1,62}$');

ALTER TABLE chapters DROP CONSTRAINT IF EXISTS chapters_source_check;
ALTER TABLE chapters ADD CONSTRAINT chapters_source_check
    CHECK (source IN ('embedded', 'inferred', 'manual', 'plugin'));

CREATE TABLE IF NOT EXISTS plugin_installs (
    plugin_id        text        PRIMARY KEY,
    version          text        NOT NULL,
    digest           text        NOT NULL,
    path             text        NOT NULL,
    manifest         jsonb       NOT NULL,
    review_level     text        NOT NULL DEFAULT 'none'
                     CHECK (review_level IN ('none', 'developer', 'store', 'development')),
    installed_at     timestamptz NOT NULL DEFAULT now(),
    previous_version text,
    previous_digest  text,
    previous_path    text,
    previous_manifest jsonb
);

CREATE TABLE IF NOT EXISTS plugin_grants (
    plugin_id     text        PRIMARY KEY,
    capabilities  text[]      NOT NULL DEFAULT '{}',
    libraries     uuid[]      NOT NULL DEFAULT '{}',
    all_libraries boolean     NOT NULL DEFAULT false,
    generation    bigint      NOT NULL DEFAULT 1,
    granted_by    text        NOT NULL DEFAULT '',
    granted_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS plugin_invocations (
    id               text        PRIMARY KEY,
    token_hash       text        NOT NULL UNIQUE,
    plugin_id        text        NOT NULL,
    version          text        NOT NULL,
    job_id           bigint      NOT NULL,
    stage            text        NOT NULL,
    subject_kind     text        NOT NULL,
    subject_id       text        NOT NULL,
    libraries        uuid[]      NOT NULL DEFAULT '{}',
    all_libraries    boolean     NOT NULL DEFAULT false,
    grants           text[]      NOT NULL DEFAULT '{}',
    grant_generation bigint      NOT NULL,
    exp              timestamptz NOT NULL,
    calls            bigint      NOT NULL DEFAULT 0,
    bytes_written    bigint      NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    ended_at         timestamptz
);

CREATE TABLE IF NOT EXISTS plugin_data (
    plugin_id    text        NOT NULL,
    subject_kind text        NOT NULL,
    subject_id   text        NOT NULL,
    key          text        NOT NULL,
    value        jsonb       NOT NULL,
    exported     boolean     NOT NULL DEFAULT false,
    bytes        integer     NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (plugin_id, subject_kind, subject_id, key)
);

CREATE TABLE IF NOT EXISTS plugin_writes (
    id          bigserial   PRIMARY KEY,
    plugin_id   text        NOT NULL,
    version     text        NOT NULL,
    job_id      bigint,
    target      text        NOT NULL,
    subject_id  uuid        NOT NULL,
    field       text        NOT NULL DEFAULT '',
    before      jsonb,
    after       jsonb,
    at          timestamptz NOT NULL DEFAULT now(),
    reverted_at timestamptz
);

CREATE TABLE IF NOT EXISTS plugin_suggestions (
    id           bigserial   PRIMARY KEY,
    plugin_id    text        NOT NULL,
    version      text        NOT NULL,
    job_id       bigint,
    subject_kind text        NOT NULL,
    subject_id   text        NOT NULL,
    field        text        NOT NULL,
    value        text        NOT NULL,
    reason       text        NOT NULL DEFAULT '',
    status       text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'accepted', 'declined')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    decided_at   timestamptz,
    decided_by   text
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS plugin_invocations_created_idx ON plugin_invocations (created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS plugin_writes_plugin_idx ON plugin_writes (plugin_id, at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION plugin_grants_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('plugin_grants_changed', NEW.plugin_id);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER plugin_grants_changed_trg
    AFTER INSERT OR UPDATE ON plugin_grants
    FOR EACH ROW EXECUTE FUNCTION plugin_grants_notify();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION plugin_installs_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('plugin_installs_changed', COALESCE(NEW.plugin_id, OLD.plugin_id));
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER plugin_installs_changed_trg
    AFTER INSERT OR UPDATE OR DELETE ON plugin_installs
    FOR EACH ROW EXECUTE FUNCTION plugin_installs_notify();
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        REVOKE ALL ON plugin_installs, plugin_grants, plugin_invocations, plugin_data,
            plugin_writes, plugin_suggestions FROM maktaba_worker;
        GRANT SELECT ON plugin_installs TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- dev only (README rule 6)
DROP INDEX IF EXISTS plugin_writes_plugin_idx;
DROP INDEX IF EXISTS plugin_invocations_created_idx;
DROP TRIGGER IF EXISTS plugin_installs_changed_trg ON plugin_installs;
DROP TRIGGER IF EXISTS plugin_grants_changed_trg ON plugin_grants;
DROP FUNCTION IF EXISTS plugin_installs_notify();
DROP FUNCTION IF EXISTS plugin_grants_notify();
DROP TABLE IF EXISTS plugin_suggestions, plugin_writes, plugin_data, plugin_invocations,
    plugin_grants, plugin_installs;
ALTER TABLE chapters DROP CONSTRAINT IF EXISTS chapters_source_check;
ALTER TABLE chapters ADD CONSTRAINT chapters_source_check
    CHECK (source IN ('embedded', 'inferred', 'manual'));
ALTER TABLE job_stages DROP CONSTRAINT IF EXISTS job_stages_stage_check;
ALTER TABLE job_stages ADD CONSTRAINT job_stages_stage_check CHECK (stage ~ '^[a-z][a-z0-9_]{1,39}$');
-- +goose StatementEnd
