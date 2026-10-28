-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0169 (Epic 33: tv_changes.origin, auto | manual: only
-- a manual change waits for an admin's approval). Same backfill as the Postgres file.
--
ALTER TABLE tv_changes ADD COLUMN IF NOT EXISTS origin TEXT NOT NULL DEFAULT 'manual'
    CHECK (origin IN ('auto', 'manual'));
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE tv_changes SET origin = 'auto'
WHERE (json_extract(source, '$.kind') IN ('write_channels', 'schedule', 'auto_update')
       OR (json_extract(source, '$.kind') IN ('rollback', 'restage') AND created_by IS NULL))
  AND NOT EXISTS (
      SELECT 1 FROM tv_change_events e
      WHERE e.change_id = tv_changes.id AND e.actor_user_id IS NOT NULL
        AND e.action NOT IN ('approve', 'approve_ahead', 'promote', 'schedule', 'cancel', 'verify', 'stage')
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tv_changes DROP COLUMN origin;
-- +goose StatementEnd
