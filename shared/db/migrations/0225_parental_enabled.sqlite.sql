-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0225 (Epic 57, story 57.12): parental controls start
-- off where nothing is set up (no restricted account, no library default tier). See
-- the Postgres file for why. app_settings.value is JSON text here.
--
UPDATE app_settings
   SET value = 'false', updated_at = datetime('now')
 WHERE key = 'parental.enabled'
   AND NOT EXISTS (SELECT 1 FROM parental_policies)
   AND NOT EXISTS (SELECT 1 FROM libraries WHERE age_tier IS NOT NULL);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE app_settings SET value = 'true', updated_at = datetime('now') WHERE key = 'parental.enabled';
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM app_settings WHERE key = 'parental.notice_dismissed_at';
-- +goose StatementEnd
