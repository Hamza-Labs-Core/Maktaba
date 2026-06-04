-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0069. `app_settings.value` is TEXT in
-- the SQLite schema (slot 0042 sibling), so the JSON scalar is stored
-- as the literal string 'false'.
--
INSERT OR IGNORE INTO app_settings (key, value)
    VALUES ('auth.open_registration', 'false');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM app_settings WHERE key = 'auth.open_registration';
-- +goose StatementEnd
