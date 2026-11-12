-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0225 (Epic 57, story 57.12): parental controls start
-- off where nothing is set up (no restricted account, no library default tier). See
-- the Postgres file for why. app_settings.value is JSON text here.
--
INSERT OR IGNORE INTO app_settings (key, value)
SELECT 'parental.enabled',
       CASE WHEN EXISTS (SELECT 1 FROM parental_policies)
                 OR EXISTS (SELECT 1 FROM libraries WHERE age_tier IS NOT NULL)
            THEN 'true' ELSE 'false' END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM app_settings WHERE key IN ('parental.enabled', 'parental.notice_dismissed_at');
-- +goose StatementEnd
