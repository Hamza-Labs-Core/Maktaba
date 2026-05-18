-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0064 (gap-closure Wave 3 / Epic 12 Story 12.10) — extend the
-- slot-0040 `devices` table (owned by Story 7.22) with the two
-- additive columns Story 12.10 requires but 7.22 omitted:
--
--   os_version  — reported by the client at register time, surfaced in
--                 GET /api/devices for support/diagnostics.
--   categories  — the push-notification categories this device opted
--                 into (closed vocabulary: job, library, subscription,
--                 system). NULL/[] means "all" until the client sends
--                 an explicit set via POST /register or PATCH.
--
-- Additive ADD COLUMN only — no rename, no constraint change — so it
-- composes cleanly on top of 7.22 without re-owning the table (same
-- "downstream plans extend via separate slots" pattern as `chapters`
-- in the migration MANIFEST).
--
ALTER TABLE devices ADD COLUMN IF NOT EXISTS os_version TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE devices ADD COLUMN IF NOT EXISTS categories JSONB;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE devices DROP COLUMN IF EXISTS categories;
ALTER TABLE devices DROP COLUMN IF EXISTS os_version;
-- +goose StatementEnd
