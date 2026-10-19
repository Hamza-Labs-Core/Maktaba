-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Runs outside a transaction because the indexes are built CONCURRENTLY.
--
-- Slot 0127 (Epic 33, stories 33.2 and 33.10) - TV releases, TV changes and the routing
-- record (specs/epics/33-tv-staging-hot-swap/design.md sections 3, 8.1 and 11).
--
-- tv_releases is one frozen version of the TV: the streaming binary (version + sha256),
-- the settings snapshot (rules.tv + rules.tv_output, the stored overrides of each), the
-- lineup digest and the schedule generation (sched_gen) it reads. Content-addressed by
-- digest: building a release equal to an existing one reuses it. Never edited; deleted
-- only by the GC (story 33.3). kinds / created_by etc. are JSON arrays (jsonb) so the
-- SQLite twin stores the same shape.
--
-- tv_release_channels is one channel of a release: def = the full channels row the
-- release wants (id, library_id, number, name, slug, logo_path, category, mode,
-- mode_config, source_filter, transition, enabled, sort_order), art = the hand-set art
-- (channel_art_overrides rows by kind, with the file's sha256), sched_changed = its
-- schedule differs from the base release after boundary_at (the divergence boundary,
-- design 4.3; NULL for a channel copied whole). channel_id has no foreign key: a
-- release is a record of what aired, and a direct write (staging off) may delete a
-- channel an old release names.
--
-- tv_changes is a TV change (design 11): state machine draft -> staged -> verifying ->
-- ready -> approved -> swapping -> live -> superseded, side exits failed / cancelled /
-- rolled_back. diff.pending holds what the producer (write_channels, the channel
-- editor, a settings save) wants; the stage job builds the release from the live state
-- plus it. anchor_at is the planned swap instant the generation was built for (the
-- boundaries are computed at it); approved_by is a JSON array of user ids.
--
-- tv_change_events is every transition and admin action (audit_log gets the admin
-- actions too, category tv_change).
--
-- tv_routing is the routing record: scope 'cluster' (or 'host:<id>' for a binary-only
-- release) -> live release + generation, the previous ones for rollback, the routing
-- epoch. No row = staging never used: tv_live_gen() (slot 0128) answers 0.
--
-- tv_sched_gen_seq allocates schedule generations; generation 0 is everything that
-- existed before this epic.
--
CREATE TABLE IF NOT EXISTS tv_releases (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    digest            TEXT        NOT NULL UNIQUE,
    kinds             JSONB       NOT NULL DEFAULT '[]'::jsonb,
    sched_gen         BIGINT      NOT NULL UNIQUE,
    streaming_version TEXT        NOT NULL DEFAULT '',
    streaming_sha256  TEXT        NOT NULL DEFAULT '',
    settings          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    lineup_digest     TEXT        NOT NULL DEFAULT '',
    base_release_id   UUID        REFERENCES tv_releases(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by        TEXT
);

CREATE TABLE IF NOT EXISTS tv_release_channels (
    release_id    UUID        NOT NULL REFERENCES tv_releases(id) ON DELETE CASCADE,
    channel_id    UUID        NOT NULL,
    def           JSONB       NOT NULL,
    art           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    sched_changed BOOLEAN     NOT NULL DEFAULT false,
    boundary_at   TIMESTAMPTZ,
    PRIMARY KEY (release_id, channel_id)
);

CREATE TABLE IF NOT EXISTS tv_changes (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    number          BIGSERIAL   UNIQUE,
    release_id      UUID        REFERENCES tv_releases(id) ON DELETE SET NULL,
    base_release_id UUID        REFERENCES tv_releases(id) ON DELETE SET NULL,
    state           TEXT        NOT NULL DEFAULT 'draft' CHECK (state IN (
                        'draft', 'staged', 'verifying', 'ready', 'approved', 'swapping',
                        'live', 'superseded', 'failed', 'cancelled', 'rolled_back')),
    kinds           JSONB       NOT NULL DEFAULT '[]'::jsonb,
    title           TEXT        NOT NULL DEFAULT '',
    source          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    change_set      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    digest          TEXT,
    diff            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    verify_report   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    auto_rule       TEXT,
    approved_by     JSONB       NOT NULL DEFAULT '[]'::jsonb,
    approved_at     TIMESTAMPTZ,
    anchor_at       TIMESTAMPTZ,
    swap_at         TIMESTAMPTZ,
    swap_wait       TEXT        CHECK (swap_wait IN ('boundary', 'immediate')),
    created_by      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    staged_at       TIMESTAMPTZ,
    ready_at        TIMESTAMPTZ,
    swapped_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    end_reason      TEXT,
    superseded_by   UUID        REFERENCES tv_changes(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS tv_change_events (
    id            BIGSERIAL   PRIMARY KEY,
    change_id     UUID        NOT NULL REFERENCES tv_changes(id) ON DELETE CASCADE,
    at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_user_id TEXT,
    actor_source  TEXT        NOT NULL DEFAULT 'system',
    from_state    TEXT,
    to_state      TEXT,
    action        TEXT        NOT NULL,
    detail        JSONB       NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS tv_routing (
    scope               TEXT        PRIMARY KEY,
    live_release_id     UUID        REFERENCES tv_releases(id) ON DELETE SET NULL,
    live_gen            BIGINT      NOT NULL DEFAULT 0,
    previous_release_id UUID        REFERENCES tv_releases(id) ON DELETE SET NULL,
    previous_gen        BIGINT,
    epoch               BIGINT      NOT NULL DEFAULT 0,
    switched_at         TIMESTAMPTZ,
    previous_until      TIMESTAMPTZ
);

CREATE SEQUENCE IF NOT EXISTS tv_sched_gen_seq START WITH 1;
-- +goose StatementEnd

-- +goose StatementBegin
-- the controller's hot reads: the open changes, a change's history (CONCURRENTLY: the
-- migration-lint rule; the tables are new and empty)
CREATE INDEX CONCURRENTLY IF NOT EXISTS tv_changes_state_idx ON tv_changes (state, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS tv_change_events_change_idx
    ON tv_change_events (change_id, id);
-- +goose StatementEnd

-- +goose StatementBegin
-- the pipeline stages of the TV change flow (engine/graph.py), seeded so a fresh database
-- matches the graph before the first worker syncs it: build a change's release and
-- generation, the swap commit, the daily clean-up
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement)
VALUES
    ('tv_stage', 'Stage TV change', ARRAY['catalog']::text[], 'cpu', 'channels', 3, 1, 0, 0, 2, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'home'),
    ('tv_swap', 'Swap TV change', ARRAY['catalog']::text[], 'cpu', 'channels', 4, 1, 0, 0, 1, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'home'),
    ('tv_gc', 'TV staging clean-up', ARRAY['catalog']::text[], 'cpu', 'channels', 5, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'home')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage IN ('tv_stage', 'tv_swap', 'tv_gc');
-- +goose StatementEnd

-- +goose StatementBegin
DROP SEQUENCE IF EXISTS tv_sched_gen_seq;
DROP TABLE IF EXISTS tv_routing;
DROP TABLE IF EXISTS tv_change_events;
DROP TABLE IF EXISTS tv_changes;
DROP TABLE IF EXISTS tv_release_channels;
DROP TABLE IF EXISTS tv_releases;
-- +goose StatementEnd
