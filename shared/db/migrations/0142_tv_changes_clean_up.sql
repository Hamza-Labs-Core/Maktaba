-- +goose Up
-- +goose StatementBegin
--
-- Slot 0142 (Epic 33, story 33.12 AC3) - clean up the TV changes made before
-- updates added up in one change. Until now every channel plan that differed
-- from the open draft superseded it and opened a new one (a new numbered
-- change every hour), and a change that failed verification ended "failed"
-- for good. From now on an update joins the open change, and a failed
-- verification returns the change to draft. What the old rule left:
--
-- 1. superseded and failed changes that never aired (swapped_at IS NULL) are
--    deleted with their events (tv_change_events cascade; deleted here too for
--    a SQLite database without foreign keys on). A change that aired and was
--    then superseded by the next swap is kept: it is the record of what aired.
--    superseded_by pointing at a deleted change is cleared first
--    (tv_changes_superseded_by_fkey).
-- 2. a staged or ready change older than the newest open draft (a draft no one
--    approved) is cancelled, end_reason 'cleaned_up', with an event: an old plan
--    still waiting for approval, which the open draft has replaced.
--
-- Kept: the live change, the newest open draft, approved and swapping changes,
-- and anything that aired. Idempotent: a second run finds nothing to do.
-- Not reversible in place: restore the pg_dump of the tv_* tables taken before.
--
UPDATE tv_changes SET superseded_by = NULL
WHERE superseded_by IN (
    SELECT id FROM tv_changes WHERE state IN ('superseded', 'failed') AND swapped_at IS NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM tv_change_events
WHERE change_id IN (
    SELECT id FROM tv_changes WHERE state IN ('superseded', 'failed') AND swapped_at IS NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM tv_changes WHERE state IN ('superseded', 'failed') AND swapped_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO tv_change_events (change_id, at, actor_source, from_state, to_state, action, detail)
SELECT c.id, now(), 'system', c.state, 'cancelled', 'cancel',
       jsonb_build_object('why', 'an older change the open draft replaced (slot 0142)',
                          'end_reason', 'cleaned_up')
FROM tv_changes c
WHERE c.state IN ('staged', 'ready')
  AND c.number < (
      SELECT max(d.number) FROM tv_changes d
      WHERE d.state = 'draft'
        AND (jsonb_typeof(d.approved_by) <> 'array' OR jsonb_array_length(d.approved_by) = 0)
  );
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE tv_changes c
SET state = 'cancelled', end_reason = 'cleaned_up', ended_at = now(), updated_at = now()
WHERE c.state IN ('staged', 'ready')
  AND c.number < (
      SELECT max(d.number) FROM tv_changes d
      WHERE d.state = 'draft'
        AND (jsonb_typeof(d.approved_by) <> 'array' OR jsonb_array_length(d.approved_by) = 0)
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Nothing to undo in place: the deleted changes are gone (restore the backup).
SELECT 1;
-- +goose StatementEnd
