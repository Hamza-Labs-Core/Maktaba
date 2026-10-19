-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Runs outside a transaction because the index is built CONCURRENTLY.
--
-- Slot 0123 (Epic 32, stories 32.1 and 32.2) - Maktaba TV across the cluster
-- (specs/epics/32-tv-streaming-cluster-remote/design.md sections 2 and 3).
--
-- streaming_hosts (slot 0117, tv-core) is extended rather than replaced: one row per
-- streaming host (the home's instance id, or a joined node's cluster_nodes.id), written
-- only through tv_host_beat (a node only for its own row). The admin's switches live on
-- the row: enabled ("Stream on this machine"; the home always, nodes off until switched
-- on), draining, capacity_override, weight, and a stream base URL the admin may pin.
--
-- channel_placements is what the home decided: the one host that may encode a channel,
-- fenced by a cluster-wide generation number (tv_placement_generation_seq). A station runs
-- only while the row names its host with its generation.
--
-- tv_cluster_secret holds the cluster stream key (32 random bytes) the front door signs
-- its requests to nodes with. It is created by the home at first boot and handed to each
-- host by tv_host_beat; the machines' group role cannot read the table.
--
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS draining BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS stream_base_url_source TEXT NOT NULL DEFAULT 'reported';
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS encoders JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS capacity_override INTEGER;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS weight INTEGER NOT NULL DEFAULT 1;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS active JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS load JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS can_push JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS can_live BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS unfit TEXT;
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS version TEXT;
ALTER TABLE streaming_hosts DROP CONSTRAINT IF EXISTS streaming_hosts_url_source_chk;
ALTER TABLE streaming_hosts ADD CONSTRAINT streaming_hosts_url_source_chk
    CHECK (stream_base_url_source IN ('reported', 'admin'));
ALTER TABLE streaming_hosts DROP CONSTRAINT IF EXISTS streaming_hosts_capacity_override_chk;
ALTER TABLE streaming_hosts ADD CONSTRAINT streaming_hosts_capacity_override_chk
    CHECK (capacity_override IS NULL OR capacity_override BETWEEN 0 AND 64);
-- existing rows: the home keeps streaming; nodes stay off until the admin switches them on
UPDATE streaming_hosts SET enabled = (role = 'home') WHERE NOT enabled;

CREATE SEQUENCE IF NOT EXISTS tv_placement_generation_seq;

CREATE TABLE IF NOT EXISTS channel_placements (
    channel_id   UUID        PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    host_id      TEXT        NOT NULL REFERENCES streaming_hosts(host_id) ON DELETE CASCADE,
    generation   BIGINT      NOT NULL,
    state        TEXT        NOT NULL CHECK (state IN ('placing', 'live', 'moving', 'released')),
    reason       TEXT        NOT NULL,
    placed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    acked_at     TIMESTAMPTZ,
    released_at  TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS tv_cluster_secret (
    id          SMALLINT    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    stream_key  BYTEA       NOT NULL,
    previous    BYTEA,
    rotated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS channel_placements_host_idx
    ON channel_placements (host_id) WHERE state <> 'released';
-- +goose StatementEnd

-- +goose StatementBegin
-- Who may act for a streaming host: the login that owns streaming_hosts (the home's own
-- user) or a superuser for any host; any other login only for the approved cluster node
-- whose database role it is (the guard of cluster_node_guard, slot 0103). Fails closed.
CREATE OR REPLACE FUNCTION tv_host_guard(p_host text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = session_user AND rolsuper)
       OR session_user = (SELECT tableowner FROM pg_tables
                           WHERE schemaname = 'public' AND tablename = 'streaming_hosts') THEN
        RETURN true;   -- the home (may act for any host)
    END IF;
    IF NOT EXISTS (SELECT 1 FROM cluster_nodes
                   WHERE id::text = p_host AND db_role = session_user AND state = 'approved') THEN
        RAISE EXCEPTION 'streaming host % is not this login''s', p_host
            USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN false;      -- a node, for its own row
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A streaming host's heartbeat (every 5 s): writes the caller's own row and returns the
-- admin's switches and the cluster stream key.
--   info: {host, role, version, encoder, encoders[], capacity, active[], load{},
--          stream_base_url, can_push{}, can_live, unfit, nvenc_free, vt_free}
--   returns: {enabled, draining, capacity_override, weight, stream_base_url,
--             stream_key_b64, previous_key_b64, rotated_at}
CREATE OR REPLACE FUNCTION tv_host_beat(p_host text, p_info jsonb) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
DECLARE
    v_home  boolean;
    v_role  text;
    v_row   streaming_hosts%ROWTYPE;
    v_key   tv_cluster_secret%ROWTYPE;
BEGIN
    v_home := tv_host_guard(p_host);
    v_role := CASE WHEN v_home AND p_info->>'role' = 'home' THEN 'home' ELSE 'node' END;
    INSERT INTO streaming_hosts AS h
        (host_id, host, role, stream_base_url, encoder, max_active, active_channels,
         nvenc_free, vt_free, heartbeat_at, enabled, encoders, active, load, can_push,
         can_live, unfit, version)
    VALUES (p_host, COALESCE(NULLIF(p_info->>'host', ''), p_host), v_role,
            NULLIF(p_info->>'stream_base_url', ''), NULLIF(p_info->>'encoder', ''),
            COALESCE((p_info->>'capacity')::int, 0),
            COALESCE(jsonb_array_length(p_info->'active'), 0),
            COALESCE((p_info->>'nvenc_free')::int, -1), COALESCE((p_info->>'vt_free')::int, -1),
            now(), v_role = 'home',
            COALESCE(p_info->'encoders', '[]'::jsonb), COALESCE(p_info->'active', '[]'::jsonb),
            COALESCE(p_info->'load', '{}'::jsonb), COALESCE(p_info->'can_push', '{}'::jsonb),
            COALESCE((p_info->>'can_live')::boolean, false), NULLIF(p_info->>'unfit', ''),
            NULLIF(p_info->>'version', ''))
    ON CONFLICT (host_id) DO UPDATE SET
        host = EXCLUDED.host,
        role = EXCLUDED.role,
        stream_base_url = CASE WHEN h.stream_base_url_source = 'admin'
                               THEN h.stream_base_url ELSE EXCLUDED.stream_base_url END,
        encoder = EXCLUDED.encoder,
        max_active = EXCLUDED.max_active,
        active_channels = EXCLUDED.active_channels,
        nvenc_free = EXCLUDED.nvenc_free,
        vt_free = EXCLUDED.vt_free,
        heartbeat_at = now(),
        -- the home always streams
        enabled = CASE WHEN EXCLUDED.role = 'home' THEN true ELSE h.enabled END,
        encoders = EXCLUDED.encoders,
        active = EXCLUDED.active,
        load = EXCLUDED.load,
        can_push = EXCLUDED.can_push,
        can_live = EXCLUDED.can_live,
        unfit = EXCLUDED.unfit,
        version = EXCLUDED.version
    RETURNING * INTO v_row;
    SELECT * INTO v_key FROM tv_cluster_secret WHERE id = 1;
    RETURN jsonb_build_object(
        'enabled', v_row.enabled,
        'draining', v_row.draining,
        'capacity_override', v_row.capacity_override,
        'weight', v_row.weight,
        'stream_base_url', v_row.stream_base_url,
        'stream_key_b64', CASE WHEN v_key.stream_key IS NULL THEN NULL
                               ELSE encode(v_key.stream_key, 'base64') END,
        'previous_key_b64', CASE WHEN v_key.previous IS NULL THEN NULL
                                 ELSE encode(v_key.previous, 'base64') END,
        'rotated_at', v_key.rotated_at);
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A host confirms the station for this placement runs (state live). Only the host the
-- placement names, with the current generation; false when superseded.
CREATE OR REPLACE FUNCTION tv_placement_ack(p_channel uuid, p_generation bigint) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
DECLARE
    v_host text;
BEGIN
    SELECT host_id INTO v_host FROM channel_placements
     WHERE channel_id = p_channel AND generation = p_generation;
    IF v_host IS NULL THEN
        RETURN false;
    END IF;
    PERFORM tv_host_guard(v_host);
    UPDATE channel_placements SET state = 'live', acked_at = now()
     WHERE channel_id = p_channel AND generation = p_generation
       AND state IN ('placing', 'moving', 'live');
    RETURN FOUND;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A host reaped an idle station: the placement is released, only if it still holds that
-- generation (a newer placement is never touched).
CREATE OR REPLACE FUNCTION tv_placement_release(p_channel uuid, p_generation bigint) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
DECLARE
    v_host text;
BEGIN
    SELECT host_id INTO v_host FROM channel_placements
     WHERE channel_id = p_channel AND generation = p_generation;
    IF v_host IS NULL THEN
        RETURN false;
    END IF;
    PERFORM tv_host_guard(v_host);
    UPDATE channel_placements SET state = 'released', released_at = now()
     WHERE channel_id = p_channel AND generation = p_generation AND state <> 'released';
    IF FOUND THEN
        PERFORM pg_notify('tv_placement', p_channel::text || ':' || v_host || ':' || p_generation);
    END IF;
    RETURN FOUND;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE EXECUTE ON FUNCTION tv_host_guard(text), tv_host_beat(text, jsonb),
    tv_placement_ack(uuid, bigint), tv_placement_release(uuid, bigint) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- hosts and placements: read only (a host's row changes through tv_host_beat);
        -- the stream key and the generation sequence: nothing
        REVOKE ALL ON streaming_hosts, channel_placements, tv_cluster_secret FROM maktaba_worker;
        REVOKE ALL ON SEQUENCE tv_placement_generation_seq FROM maktaba_worker;
        GRANT SELECT ON streaming_hosts, channel_placements TO maktaba_worker;
        REVOKE EXECUTE ON FUNCTION tv_host_guard(text) FROM maktaba_worker;
        GRANT EXECUTE ON FUNCTION tv_host_beat(text, jsonb), tv_placement_ack(uuid, bigint),
            tv_placement_release(uuid, bigint) TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS tv_placement_release(uuid, bigint);
DROP FUNCTION IF EXISTS tv_placement_ack(uuid, bigint);
DROP FUNCTION IF EXISTS tv_host_beat(text, jsonb);
DROP FUNCTION IF EXISTS tv_host_guard(text);
DROP TABLE IF EXISTS tv_cluster_secret;
DROP TABLE IF EXISTS channel_placements;
DROP SEQUENCE IF EXISTS tv_placement_generation_seq;
ALTER TABLE streaming_hosts DROP CONSTRAINT IF EXISTS streaming_hosts_capacity_override_chk;
ALTER TABLE streaming_hosts DROP CONSTRAINT IF EXISTS streaming_hosts_url_source_chk;
ALTER TABLE streaming_hosts DROP COLUMN IF EXISTS version;
ALTER TABLE streaming_hosts DROP COLUMN IF EXISTS unfit;
ALTER TABLE streaming_hosts DROP COLUMN IF EXISTS can_live;
ALTER TABLE streaming_hosts DROP COLUMN IF EXISTS can_push;
ALTER TABLE streaming_hosts DROP COLUMN IF EXISTS load;
ALTER TABLE streaming_hosts DROP COLUMN IF EXISTS active;
ALTER TABLE streaming_hosts DROP COLUMN IF EXISTS weight;
ALTER TABLE streaming_hosts DROP COLUMN IF EXISTS capacity_override;
ALTER TABLE streaming_hosts DROP COLUMN IF EXISTS encoders;
ALTER TABLE streaming_hosts DROP COLUMN IF EXISTS stream_base_url_source;
ALTER TABLE streaming_hosts DROP COLUMN IF EXISTS draining;
ALTER TABLE streaming_hosts DROP COLUMN IF EXISTS enabled;
-- +goose StatementEnd
