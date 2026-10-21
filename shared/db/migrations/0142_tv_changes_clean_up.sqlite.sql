-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0142: delete the superseded and failed TV
-- changes that never aired (with their events), and cancel the staged or
-- ready changes older than the newest open draft (see the Postgres file).
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
SELECT c.id, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), 'system', c.state, 'cancelled', 'cancel',
       json_object('why', 'an older change the open draft replaced (slot 0142)',
                   'end_reason', 'cleaned_up')
FROM tv_changes c
WHERE c.state IN ('staged', 'ready')
  AND c.number < (
      SELECT max(d.number) FROM tv_changes d
      WHERE d.state = 'draft'
        AND (json_valid(d.approved_by) = 0 OR json_type(d.approved_by) <> 'array'
             OR json_array_length(d.approved_by) = 0)
  );
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE tv_changes
SET state = 'cancelled', end_reason = 'cleaned_up',
    ended_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE state IN ('staged', 'ready')
  AND number < (
      SELECT max(d.number) FROM tv_changes d
      WHERE d.state = 'draft'
        AND (json_valid(d.approved_by) = 0 OR json_type(d.approved_by) <> 'array'
             OR json_array_length(d.approved_by) = 0)
  );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
