-- +goose Up
-- +goose StatementBegin
--
-- Slot 0225 - parental controls start off in a home that has set nothing up (Epic 57,
-- story 57.12).
--
-- Slot 0221 seeded app_settings `parental.enabled` as true. A home that updates has
-- users and devices and no ratings: were a device's age signal applied at once, a
-- child's iPad on an adult's account would show nothing (unrated content hides), with
-- no admin having asked for it. So the switch goes off wherever nothing is set up: no
-- restricted account and no library default tier. While it is off the API records a
-- device's signal and applies none (api/internal/parental/enable.go); it turns on by
-- itself with the first restricted account or the first library default tier, and an
-- admin can turn it on. A home that already has either keeps it on.
--
-- Data only: no table, no column, no index. An older release never reads the key.
--
UPDATE app_settings
   SET value = 'false'::jsonb, updated_at = now()
 WHERE key = 'parental.enabled'
   AND NOT EXISTS (SELECT 1 FROM parental_policies)
   AND NOT EXISTS (SELECT 1 FROM libraries WHERE age_tier IS NOT NULL);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- back to slot 0221's seed; the dismissal of the notice is this slot's release's key
UPDATE app_settings SET value = 'true'::jsonb, updated_at = now() WHERE key = 'parental.enabled';
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM app_settings WHERE key = 'parental.notice_dismissed_at';
-- +goose StatementEnd
