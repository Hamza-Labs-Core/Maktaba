-- +goose Up
-- +goose StatementBegin
--
-- Slot 0137 (Epic 33, story 33.13 AC1) - what the TV slots' front door reports for the
-- bell: a failover of the live slot to the hot standby, and a staging slot that keeps
-- crashing. The front door (streaming/internal/frontdoor) writes a row, the API's TV
-- change controller reads them in id order (notification_cursors 'tv_slot_events') and
-- turns each into a notice through the notification hub, as it does the change events.
--
-- kind: failover (slot: the lost live slot, detail.to: the standby now live, detail.why)
-- | crash_loop (slot: the staging slot, detail.restarts within detail.window_sec).
--
CREATE TABLE IF NOT EXISTS tv_slot_events (
    id      BIGSERIAL   PRIMARY KEY,
    host_id TEXT        NOT NULL,
    kind    TEXT        NOT NULL CHECK (kind IN ('failover', 'crash_loop')),
    slot    TEXT        NOT NULL DEFAULT '',
    detail  JSONB       NOT NULL DEFAULT '{}'::jsonb,
    at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS tv_slot_events_at_idx ON tv_slot_events (at);
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- the home's front door writes them and its API reads them; a node only reads
        REVOKE ALL ON tv_slot_events FROM maktaba_worker;
        GRANT SELECT ON tv_slot_events TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tv_slot_events;
-- +goose StatementEnd
