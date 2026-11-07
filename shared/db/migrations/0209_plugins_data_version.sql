-- +goose Up
-- +goose StatementBegin
--
-- Slot 0209 - plugins: the version a plugin's stored data is at (Epic 54, story 54.07 §5
-- step 4).
--
--   plugin_installs.data_version  the data_version the plugin's data is at, when it is
--                                 behind the installed manifest's: an update whose
--                                 data_version grew leaves it at the data's version, and
--                                 the plugin's stages wait until its migrate job brings
--                                 the data up and sets it back to NULL. NULL: the data is
--                                 at the manifest's data_version (every install before
--                                 this slot, and every fresh install).
--
-- Written by the home only: the API at update and rollback, the home's pipeline when a
-- migrate job ends (plugin_tables_guard() keeps joined machines from writing it).
-- Additive: an older release never reads it. No CHECK: a constraint added is not additive
-- (the updater then puts the dump back on a rollback instead of keeping this schema); the
-- API and the pipeline only ever write NULL or a manifest's data_version (1 or more).
--
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS data_version integer;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE plugin_installs DROP COLUMN IF EXISTS data_version;
-- +goose StatementEnd
