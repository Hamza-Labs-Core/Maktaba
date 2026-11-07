-- name: rules.read
-- mode: read
-- scope: settings $1
-- tables: app_settings
SELECT value FROM app_settings WHERE key = $1
