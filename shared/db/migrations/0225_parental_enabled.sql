-- +goose Up
-- +goose StatementBegin
--
-- Slot 0225 - parental controls start off in a home that has set nothing up (Epic 57,
-- story 57.12).
--
-- A home that updates has users and devices and no ratings: were a device's age signal
-- applied at once, a child's iPad on an adult's account would show nothing (unrated
-- content hides), with no admin having asked for it. So the switch, app_settings
-- `parental.enabled`, is seeded off wherever nothing is set up: no restricted account
-- and no library default tier. While it is off the API records a device's signal and
-- applies none (api/internal/parental/enable.go); it turns on by itself with the first
-- restricted account or the first library default tier, and an admin can turn it on. A
-- home that already has either starts with it on.
--
-- The key is added here and nowhere before (slot 0221 does not seed it), and a value
-- already there is left alone: this slot overwrites nothing, so a rollback of the update
-- undoes it with the Down script and puts no dump back
-- (cmd/maktaba-server/internal/updater/schemadown.go).
--
-- Data only: no table, no column, no index. An older release never reads the key.
--
INSERT INTO app_settings (key, value)
SELECT 'parental.enabled',
       to_jsonb(EXISTS (SELECT 1 FROM parental_policies)
                OR EXISTS (SELECT 1 FROM libraries WHERE age_tier IS NOT NULL))
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- the switch and the dismissal of the notice are this slot's release's keys
DELETE FROM app_settings WHERE key IN ('parental.enabled', 'parental.notice_dismissed_at');
-- +goose StatementEnd
