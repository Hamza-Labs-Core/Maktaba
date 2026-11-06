-- +goose Up
-- +goose StatementBegin
--
-- Slot 0185 - a restart for components whose loops run in another process (#636).
--
-- POST /api/components/{id}/restart restarts the supervisor's processes and the API's own
-- loops directly. A loop that runs in streaming or the pipeline belongs to that process's
-- runner, which the API cannot reach: the API asks it here instead.
--
--   component_restarts  one row per machine, component and host (api, streaming, pipeline,
--                       supervisor): seq moves on each request. A runner of that host on
--                       that machine reads the rows on every change of the desired state
--                       and restarts its loops of a component whose seq moved since it
--                       last read (the first read is only remembered: a process that
--                       starts restarts nothing).
--
-- Every write moves component_meta.revision and sends NOTIFY components_changed (the same
-- trigger function as the desired rows), so a runner hears it at once. maktaba_worker may
-- read it (a joined machine's runners) and not write it.
--
-- Additive: one table, two triggers.
--
CREATE TABLE IF NOT EXISTS component_restarts (
    machine      text        NOT NULL,
    id           text        NOT NULL,
    host         text        NOT NULL CHECK (host IN ('api', 'streaming', 'pipeline', 'supervisor')),
    seq          bigint      NOT NULL DEFAULT 1,
    requested_at timestamptz NOT NULL DEFAULT now(),
    requested_by text        NOT NULL DEFAULT '',
    PRIMARY KEY (machine, id, host)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER component_restarts_changed_trg
    AFTER INSERT OR UPDATE ON component_restarts
    FOR EACH ROW EXECUTE FUNCTION components_changed();
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        REVOKE ALL ON component_restarts FROM maktaba_worker;
        GRANT SELECT ON component_restarts TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- dev only (README rule 6)
DROP TRIGGER IF EXISTS component_restarts_changed_trg ON component_restarts;
DROP TABLE IF EXISTS component_restarts;
-- +goose StatementEnd
