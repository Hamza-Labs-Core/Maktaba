-- +goose Up
-- +goose StatementBegin
--
-- Slot 0131 (Epic 33, stories 33.7 and 33.9) - the swap in the front door and the
-- rollback (specs/epics/33-tv-staging-hot-swap/design.md sections 8 and 10).
--
-- tv_swap_ops is one swap or rollback operation for the front door to run on the TV
-- slots. The swap commit (pipeline tvrelease/commit.py, the tv_swap stage) writes a
-- 'swap' operation in the same transaction that applies the release's lineup and flips
-- tv_routing to its generation; an admin's warm rollback (API) and the front door's
-- automatic rollback (a crash of the new live slot inside the grace) write a 'rollback'
-- operation, and the reverse commit (the tv_rollback stage) marks it committed. scope is
-- 'cluster' (every host's front door; the home's today) or 'host:<id>' (a binary-only
-- release of one host). release_id is the release the operation puts on the air,
-- from_release_id the one it replaces. state: planned (written) -> switching (a front
-- door took it: host_id, from_slot, to_slot) -> done | failed (its target slot was lost
-- or never answered: the controller rolls the commit back) | cancelled (a rollback came
-- first). detail carries the counts (switched, skipped) and errors.
--
-- tv_channel_switch is one channel of an operation: its planned instant (switch_at: the
-- end of the programme on the air for a watched channel with swap_wait = boundary,
-- capped at boundary_max_wait_minutes; the commit instant otherwise; a rollback: at
-- once), its state (planned -> switched | skipped) and whether the channel leaves the
-- lineup (removed: its streams end at switch_at). The front door moves a channel nobody
-- watches to "now" and spreads the instants of a rolling swap (it writes switch_at
-- back).
--
CREATE TABLE IF NOT EXISTS tv_swap_ops (
    id              BIGSERIAL   PRIMARY KEY,
    change_id       UUID,
    kind            TEXT        NOT NULL CHECK (kind IN ('swap', 'rollback')),
    scope           TEXT        NOT NULL DEFAULT 'cluster',
    release_id      UUID,
    from_release_id UUID,
    epoch           BIGINT      NOT NULL DEFAULT 0,
    reason          TEXT        NOT NULL DEFAULT '',
    state           TEXT        NOT NULL DEFAULT 'planned'
                                CHECK (state IN ('planned', 'switching', 'done', 'failed', 'cancelled')),
    committed       BOOLEAN     NOT NULL DEFAULT false,
    created_by      TEXT        NOT NULL DEFAULT '',
    host_id         TEXT,
    from_slot       TEXT,
    to_slot         TEXT,
    detail          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at      TIMESTAMPTZ,
    done_at         TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS tv_channel_switch (
    op_id       BIGINT      NOT NULL REFERENCES tv_swap_ops(id) ON DELETE CASCADE,
    change_id   UUID,
    channel_id  UUID        NOT NULL,
    switch_at   TIMESTAMPTZ NOT NULL,
    state       TEXT        NOT NULL DEFAULT 'planned'
                            CHECK (state IN ('planned', 'switching', 'switched', 'skipped')),
    removed     BOOLEAN     NOT NULL DEFAULT false,
    switched_at TIMESTAMPTZ,
    detail      JSONB       NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (op_id, channel_id)
);

-- the reverse commit (story 33.9): a warm rollback's lineup, settings and generation
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement)
VALUES
    ('tv_rollback', 'Roll back TV change', ARRAY['catalog']::text[], 'cpu', 'channels', 6, 1, 0, 0, 2, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'home')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- the home's front door and API run them; a node only reads
        REVOKE ALL ON tv_swap_ops, tv_channel_switch FROM maktaba_worker;
        GRANT SELECT ON tv_swap_ops, tv_channel_switch TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'tv_rollback';
DROP TABLE IF EXISTS tv_channel_switch;
DROP TABLE IF EXISTS tv_swap_ops;
-- +goose StatementEnd
