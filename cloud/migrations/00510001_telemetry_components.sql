-- +goose Up
-- Epic 51 story 51.14: a home's daily usage report may carry its bundle (full, core,
-- worker or custom) and the catalog ids of the components it has enabled. Both are
-- empty for a report from an older home. No counts, no machines, no names.
ALTER TABLE telemetry_server_reports ADD COLUMN IF NOT EXISTS bundle TEXT NOT NULL DEFAULT '';
ALTER TABLE telemetry_server_reports ADD COLUMN IF NOT EXISTS components TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE telemetry_server_reports DROP COLUMN IF EXISTS components;
ALTER TABLE telemetry_server_reports DROP COLUMN IF EXISTS bundle;
