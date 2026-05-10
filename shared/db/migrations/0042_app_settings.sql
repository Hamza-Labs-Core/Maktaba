-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0042 (Epic 7 / Story 7.15) — DB-backed runtime config.
--
-- One row per key. ``value`` is JSONB so a key can hold a scalar,
-- object, or array without a schema change. The trigger fires
-- ``NOTIFY settings_changed, '<key>'`` so each API replica reloads
-- without polling.
--
CREATE TABLE IF NOT EXISTS app_settings (
    key        TEXT         PRIMARY KEY,
    value      JSONB        NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_by UUID         REFERENCES users(id) ON DELETE SET NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app_settings_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('settings_changed', NEW.key);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER app_settings_notify_trg
    AFTER INSERT OR UPDATE ON app_settings
    FOR EACH ROW EXECUTE FUNCTION app_settings_notify();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS app_settings_notify_trg ON app_settings;
DROP FUNCTION IF EXISTS app_settings_notify();
DROP TABLE IF EXISTS app_settings;
-- +goose StatementEnd
