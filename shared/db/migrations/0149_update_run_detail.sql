-- +goose Up
-- +goose StatementBegin
--
-- Slot 0149 (Epic 35 stories 35.4, 35.12, 35.13): what the home's update driver keeps
-- of a run beside its state. update_runs.install_kind is the home's install kind
-- (layout, runtime, appbundle: specs/epics/35-cluster-full-nodes-and-updates/
-- install-kinds.md) and detail what the run waits on and what it changed to put back
-- (the machines' drain before it, the gate it waits at, the home's last report);
-- update_targets gains the machine's name and install kind as the run found them, its
-- last report (detail) and when it last moved (updated_at), so a run's page reads whole
-- after the machine is gone.
--
-- Additive only; the update tables stay the home API's (nothing for maktaba_worker).
--
ALTER TABLE update_runs ADD COLUMN IF NOT EXISTS install_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE update_runs ADD COLUMN IF NOT EXISTS detail JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE update_targets ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '';
ALTER TABLE update_targets ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT '';
ALTER TABLE update_targets ADD COLUMN IF NOT EXISTS detail JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE update_targets ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE update_targets DROP COLUMN IF EXISTS updated_at;
ALTER TABLE update_targets DROP COLUMN IF EXISTS detail;
ALTER TABLE update_targets DROP COLUMN IF EXISTS kind;
ALTER TABLE update_targets DROP COLUMN IF EXISTS name;
ALTER TABLE update_runs DROP COLUMN IF EXISTS detail;
ALTER TABLE update_runs DROP COLUMN IF EXISTS install_kind;
-- +goose StatementEnd
