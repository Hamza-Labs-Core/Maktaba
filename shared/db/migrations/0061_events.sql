-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0061 (gap-closure Wave 2 / Epic 19 Story 19.2, HLB-353) —
-- cross-replica WebSocket event bus: durable `events` table + the
-- LISTEN/NOTIFY fan-out trigger.
--
-- Why this exists
-- ----------------
-- The API WS Hub was in-memory single-process: an event published on
-- replica A never reached a WS client whose socket lives on replica B,
-- and a client that reconnected lost every event it missed. This slot
-- adds the durable surface the cross-replica bus needs:
--
--   * `events` — append-only log. `id BIGSERIAL` is the monotonic
--     cursor a reconnecting client replays from (`last_event_id`
--     handshake). `channel` is the WS fan-out key (`jobs`,
--     `library:<id>`, `playback:<video_id>` — the exact strings
--     ws.go already routes on). `payload` is the full event body;
--     the table, not the NOTIFY, is the source of truth so the
--     payload is never truncated.
--
--   * `events_notify()` + `events_notify_trg` — AFTER INSERT trigger
--     that fires `pg_notify('ws.events', '{"id":..,"channel":..}')`.
--     The NOTIFY carries ONLY the id + channel (tens of bytes), well
--     under Postgres' 8 KiB NOTIFY payload bound (Story 19.2 AC3) —
--     every replica's LISTEN loop reads the full row from `events` by
--     id, so a large payload can never overflow NOTIFY. This mirrors
--     the established slot-0002 `jobs.new` / slot-0005 `videos.new`
--     trigger pattern (DB-layer fan-out so application code cannot
--     forget to publish), differing only in that the durable replay
--     log is the table itself rather than a side effect.
--
-- Indexes
--   * events_channel_id  — (channel, id) serves the replay scan
--     "every event on this channel with id > :last_event_id".
--   * events_created_at  — created_at scan for the 7-day pruner
--     (api runs the prune sweep periodically; see eventbus.Pruner).
--
-- The indexes are built CONCURRENTLY (no ACCESS EXCLUSIVE lock on a
-- table that the live fan-out path writes on every event); that
-- requires this migration to run outside a transaction, which is the
-- reason for the no-transaction annotation on line 1 — same shape as
-- slot 0060's reaper index.
--
-- Idempotency: CREATE TABLE IF NOT EXISTS / CREATE INDEX CONCURRENTLY
-- IF NOT EXISTS / CREATE OR REPLACE FUNCTION / DROP TRIGGER IF EXISTS +
-- CREATE OR REPLACE TRIGGER keep re-application a no-op. This file
-- uses goose's StatementBegin / StatementEnd markers around each DDL
-- statement.
--
CREATE TABLE IF NOT EXISTS events (
    id          BIGSERIAL    PRIMARY KEY,
    channel     TEXT         NOT NULL,
    type        TEXT         NOT NULL,
    payload     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS events_channel_id ON events (channel, id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS events_created_at ON events (created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION events_notify() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    -- Bounded NOTIFY: id + channel only. The full payload is read
    -- back from the row by every replica's LISTEN loop, so this
    -- frame is always well under the 8 KiB NOTIFY limit regardless
    -- of payload size (Story 19.2 AC3).
    PERFORM pg_notify(
        'ws.events',
        json_build_object(
            'id',      NEW.id,
            'channel', NEW.channel
        )::text
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS events_notify_trg ON events;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER events_notify_trg
    AFTER INSERT ON events
    FOR EACH ROW
    EXECUTE FUNCTION events_notify();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS events_notify_trg ON events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS events_notify();
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS events_created_at;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS events_channel_id;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS events;
-- +goose StatementEnd
