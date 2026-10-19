-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Runs outside a transaction because the indexes are built CONCURRENTLY.
--
-- Slot 0132 (Epic 35, slot S1: stories 35.3, 35.4, 35.13) - automatic updates of the
-- home and its machines.
--
-- release_catalog is every release of the allowed channel newer than the watermark that
-- the home's update poller has seen (story 35.3 AC2): its GitHub fields, the signed
-- release.json (manifest) once verified, and verify_state. A release that fails
-- verification is kept (failed, with the reason) so it is reported once and never
-- installed; withdrawn_at is set when the GitHub release disappeared, turned prerelease
-- on stable or changed its checksums.txt (AC6); blocked_reason after too many failed
-- runs; held_at when it breaks the previous home release and waits for an admin (AC4).
--
-- update_runs / update_run_steps / update_targets are the runs, their steps (on the home
-- and on each machine) and the machines of a run (stories 35.4, 35.12); update_watermark
-- is the cluster's highest committed version (design section 3.5), one row.
--
-- Additive only. The home API is the only reader and writer: nothing for maktaba_worker
-- (Epic 31 section 3.3 rule for cluster tables).
--
CREATE TABLE IF NOT EXISTS release_catalog (
    version         TEXT        PRIMARY KEY,
    tag             TEXT        NOT NULL,
    channel         TEXT        NOT NULL DEFAULT 'stable' CHECK (channel IN ('stable', 'beta')),
    published_at    TIMESTAMPTZ,
    prerelease      BOOLEAN     NOT NULL DEFAULT false,
    release_url     TEXT        NOT NULL DEFAULT '',
    notes           TEXT        NOT NULL DEFAULT '',
    assets          JSONB       NOT NULL DEFAULT '[]'::jsonb,
    manifest        JSONB,
    checksums_sha256 TEXT       NOT NULL DEFAULT '',
    verify_state    TEXT        NOT NULL DEFAULT 'unverified'
                                CHECK (verify_state IN ('unverified', 'verified', 'failed')),
    verify_error    JSONB,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    verified_at     TIMESTAMPTZ,
    withdrawn_at    TIMESTAMPTZ,
    blocked_reason  TEXT        NOT NULL DEFAULT '',
    held_at         TIMESTAMPTZ,
    approved_at     TIMESTAMPTZ,
    approved_by     TEXT,
    attempts        INTEGER     NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS update_runs (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    version        TEXT        NOT NULL,
    from_version   TEXT        NOT NULL DEFAULT '',
    trigger        TEXT        NOT NULL CHECK (trigger IN ('auto', 'manual', 'catch_up', 'retry')),
    state          TEXT        NOT NULL DEFAULT 'planned',
    step           TEXT        NOT NULL DEFAULT '',
    started_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at    TIMESTAMPTZ,
    error          JSONB,
    backup         JSONB,
    actor_user_id  TEXT,
    journal_digest TEXT        NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS update_run_steps (
    run_id      UUID        NOT NULL REFERENCES update_runs(id) ON DELETE CASCADE,
    seq         INTEGER     NOT NULL,
    host_id     TEXT        NOT NULL DEFAULT 'home',
    step        TEXT        NOT NULL,
    state       TEXT        NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    detail      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (run_id, seq)
);

CREATE TABLE IF NOT EXISTS update_targets (
    run_id       UUID        NOT NULL REFERENCES update_runs(id) ON DELETE CASCADE,
    node_id      UUID        NOT NULL,
    ord          INTEGER     NOT NULL DEFAULT 0,
    platform     TEXT        NOT NULL DEFAULT '',
    state        TEXT        NOT NULL DEFAULT 'pending',
    from_version TEXT        NOT NULL DEFAULT '',
    to_version   TEXT        NOT NULL DEFAULT '',
    attempts     INTEGER     NOT NULL DEFAULT 0,
    error        JSONB,
    prepared_at  TIMESTAMPTZ,
    applied_at   TIMESTAMPTZ,
    healthy_at   TIMESTAMPTZ,
    PRIMARY KEY (run_id, node_id)
);

CREATE TABLE IF NOT EXISTS update_watermark (
    id                INTEGER     PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    highest_installed TEXT        NOT NULL,
    set_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- At most one run is active at a time (design section 5.1): a second planned run is
-- refused by the database, not only by the scheduler.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS update_runs_one_active_idx ON update_runs ((true))
    WHERE finished_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS update_runs_started_idx ON update_runs (started_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- audit_log's category check (slots 0054, 0130) gains 'update' (README R9.3: every
-- run, step, admin action and machine result is audited under category update).
-- NOT VALID as before: existing rows are not re-read.
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_category_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_category_check
    CHECK (category IN (
        'auth', 'library', 'admin', 'data', 'config',
        'keys', 'device', 'security', 'integrity', 'subscription',
        'tv_change', 'tv_remote', 'update'))
    NOT VALID;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- slot 0103's default privileges gave the group role rights on new tables: the
        -- home API is the only reader and writer of the update tables.
        REVOKE ALL ON release_catalog, update_runs, update_run_steps, update_targets,
            update_watermark FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_category_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_category_check
    CHECK (category IN (
        'auth', 'library', 'admin', 'data', 'config',
        'keys', 'device', 'security', 'integrity', 'subscription',
        'tv_change', 'tv_remote'))
    NOT VALID;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS update_watermark;
DROP TABLE IF EXISTS update_targets;
DROP TABLE IF EXISTS update_run_steps;
DROP TABLE IF EXISTS update_runs;
DROP TABLE IF EXISTS release_catalog;
-- +goose StatementEnd
