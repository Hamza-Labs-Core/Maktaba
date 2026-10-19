-- +goose Up
-- +goose StatementBegin
--
-- Slot 0118 (Epic 27, channel builder) - channels.edited_at: when a person last changed the
-- channel's definition through the API (name, number, category, mode, rules, filler, enabled).
-- The lineup plan writer (pipeline write_channels) decides "user-edited" on its own, from the
-- content hash in external_links; this column is what the channel list shows ("edited by
-- you" next to "from plan") without the API having to compute that hash. "Back to plan"
-- (POST /api/channels/{id}/follow-plan) clears it. Art uploads never set it: they are
-- overrides, not edits (slot 0115).
--
ALTER TABLE channels ADD COLUMN IF NOT EXISTS edited_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
-- a plan channel changed after the plan writer last wrote it (its row is newer than its
-- link) has been edited by hand
UPDATE channels c SET edited_at = c.updated_at
FROM external_links el
WHERE el.target = 'maktaba_channel' AND el.subject_kind = 'catalog'
  AND el.external_id = c.id::text
  AND c.updated_at > el.updated_at + interval '2 seconds'
  AND c.edited_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE channels DROP COLUMN IF EXISTS edited_at;
-- +goose StatementEnd
