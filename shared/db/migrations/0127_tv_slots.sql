-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Runs outside a transaction because the index is built CONCURRENTLY.
--
-- Slot 0127 (Epic 33, story 33.1) - the TV slots: on each streaming host the channel
-- engine runs in slot processes (maktaba-streaming slot --slot blue|green|amber),
-- supervised by maktaba-server, behind the front door that owns every client connection
-- (specs/epics/33-tv-staging-hot-swap/design.md section 2).
--
-- tv_slots is one row per host and slot instance. role says what the instance does
-- (live: the front door routes viewers to it; standby: the hot standby running the live
-- release, warm on the watched channels, the failover target; staging: a candidate
-- release, story 33.5; previous: the old live slot kept warm after a swap; draining;
-- idle). role and role_epoch are written by the front door only (failover, and the swap
-- of story 33.7), under the host's advisory lock, each change one epoch up; state, pid,
-- version, base_url, warm, encoders_used and beat_at by the slot itself through
-- tv_slot_beat, every 5 s. A slot whose beat is older than 15 s is unhealthy.
-- release_id binds a slot to a TV release (story 33.2); a slot started for another
-- release runs no station (release_mismatch).
--
CREATE TABLE IF NOT EXISTS tv_slots (
    host_id        TEXT        NOT NULL,
    slot           TEXT        NOT NULL CHECK (slot IN ('blue', 'green', 'amber')),
    role           TEXT        NOT NULL DEFAULT 'idle'
                               CHECK (role IN ('idle', 'staging', 'live', 'standby', 'previous', 'draining')),
    role_epoch     BIGINT      NOT NULL DEFAULT 0,
    state          TEXT        NOT NULL DEFAULT 'stopped'
                               CHECK (state IN ('stopped', 'starting', 'healthy', 'unhealthy', 'crashed')),
    release_id     UUID,
    base_url       TEXT,
    pid            INTEGER,
    version        TEXT,
    warm           JSONB       NOT NULL DEFAULT '[]'::jsonb,
    encoders_used  INTEGER     NOT NULL DEFAULT 0,
    started_at     TIMESTAMPTZ,
    beat_at        TIMESTAMPTZ,
    last_exit      JSONB,
    PRIMARY KEY (host_id, slot)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- at most one live slot per host
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS tv_slots_one_live ON tv_slots (host_id) WHERE role = 'live';
-- +goose StatementEnd

-- +goose StatementBegin
-- A slot's heartbeat: its own row's state, process and warm set; answers the role the
-- front door gave it and the release it is bound to. The host must be this login's own
-- (tv_host_guard, slot 0123: the home's user, or a node for its own host id).
CREATE OR REPLACE FUNCTION tv_slot_beat(p_host text, p_slot text, p_info jsonb)
RETURNS TABLE (role text, role_epoch bigint, release_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    PERFORM tv_host_guard(p_host);
    INSERT INTO tv_slots AS s (host_id, slot, state, base_url, pid, version, warm, encoders_used,
                               started_at, beat_at)
    VALUES (p_host, p_slot, COALESCE(NULLIF(p_info->>'state', ''), 'healthy'),
            NULLIF(p_info->>'base_url', ''), (p_info->>'pid')::int, NULLIF(p_info->>'version', ''),
            COALESCE(p_info->'warm', '[]'::jsonb), COALESCE((p_info->>'encoders_used')::int, 0),
            COALESCE((p_info->>'started_at')::timestamptz, now()), now())
    ON CONFLICT (host_id, slot) DO UPDATE SET
        state = EXCLUDED.state,
        base_url = EXCLUDED.base_url,
        pid = EXCLUDED.pid,
        version = EXCLUDED.version,
        warm = EXCLUDED.warm,
        encoders_used = EXCLUDED.encoders_used,
        started_at = EXCLUDED.started_at,
        beat_at = now();
    RETURN QUERY SELECT s.role, s.role_epoch, s.release_id FROM tv_slots s
                  WHERE s.host_id = p_host AND s.slot = p_slot;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE EXECUTE ON FUNCTION tv_slot_beat(text, text, jsonb) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- a node reads the slots and beats its own; roles are the home's front door's
        REVOKE ALL ON tv_slots FROM maktaba_worker;
        GRANT SELECT ON tv_slots TO maktaba_worker;
        GRANT EXECUTE ON FUNCTION tv_slot_beat(text, text, jsonb) TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS tv_slot_beat(text, text, jsonb);
DROP INDEX IF EXISTS tv_slots_one_live;
DROP TABLE IF EXISTS tv_slots;
-- +goose StatementEnd
