-- +goose Up
-- +goose StatementBegin
--
-- Slot 0069 (web-pages-batch2 / Register) — seed the `open_registration`
-- runtime setting.
--
-- The Register flow (POST /api/auth/register) is gated by this flag:
-- self-service sign-up is allowed only when `auth.open_registration` is
-- true OR the users table is empty (first-user bootstrap). We model it
-- as a row in the slot-0042 `app_settings` KV store (JSONB scalar)
-- rather than a bespoke column so it inherits the existing
-- merge/redaction/NOTIFY machinery and is admin-PATCHable at runtime.
--
-- Default is `false` (closed) — the safe posture for an exposed server.
-- ON CONFLICT keeps a re-run idempotent and never clobbers an operator's
-- chosen value.
--
INSERT INTO app_settings (key, value)
    VALUES ('auth.open_registration', 'false'::jsonb)
ON CONFLICT (key) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM app_settings WHERE key = 'auth.open_registration';
-- +goose StatementEnd
