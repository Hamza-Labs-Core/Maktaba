-- +goose Up
-- +goose StatementBegin
--
-- Slot 0169 (Epic 33, the owner's rule of 2026-10-28: "only manual needs approval") -
-- who made a TV change.
--
--   tv_changes.origin  'auto' | 'manual' (NOT NULL, default 'manual').
--     auto    the system made it: the pipeline's channel plan (write_channels) and
--             schedule releases, the automatic updater's binary change (auto_update),
--             a rollback no person asked for. It goes on the air by its kind's rule
--             (immediate, or quiet_window), with no approval and no channel limit.
--     manual  a person made it: the channel editor, a lineup or filler edit, a TV
--             settings save, an admin's "new change", a rollback an admin pressed. It
--             always waits for an admin's approval (require_second_admin still applies).
--   A change a person's edit joined is manual from then on (an automatic plan joining
--   a manual change leaves it manual). The default is the safe one: a writer that
--   does not say (an older pipeline) makes a change that waits for an approval.
--
-- Backfill: a change of an automatic producer (source.kind write_channels, schedule,
-- auto_update, or a rollback or restage with no created_by) that no person's edit joined
-- (no event by a person other than an approval, a stage, a check or a cancel) is auto,
-- everything else keeps the default, manual.
--
-- Additive: a column with a default, then one UPDATE of the (small) tv_changes table.
--
ALTER TABLE tv_changes ADD COLUMN IF NOT EXISTS origin text NOT NULL DEFAULT 'manual';
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'tv_changes_origin_check') THEN
        ALTER TABLE tv_changes ADD CONSTRAINT tv_changes_origin_check CHECK (origin IN ('auto', 'manual'));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE tv_changes c SET origin = 'auto'
WHERE (c.source ->> 'kind' IN ('write_channels', 'schedule', 'auto_update')
       OR (c.source ->> 'kind' IN ('rollback', 'restage') AND c.created_by IS NULL))
  AND NOT EXISTS (
      SELECT 1 FROM tv_change_events e
      WHERE e.change_id = c.id AND e.actor_user_id IS NOT NULL
        AND e.action NOT IN ('approve', 'approve_ahead', 'promote', 'schedule', 'cancel', 'verify', 'stage')
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tv_changes DROP CONSTRAINT IF EXISTS tv_changes_origin_check;
ALTER TABLE tv_changes DROP COLUMN IF EXISTS origin;
-- +goose StatementEnd
