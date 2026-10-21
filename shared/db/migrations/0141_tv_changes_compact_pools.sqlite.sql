-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0141: keep, in each filler pool write a TV
-- change stores (diff -> pending -> pools -> write), only the items of its
-- add and retime lists (see the Postgres file for why).
--
UPDATE tv_changes
SET diff = json_set(diff, '$.pending.pools.write', json((
    SELECT json_group_array(json(json_set(w.value, '$.items', json(COALESCE((
        SELECT json_group_object(t.k, json_extract(w.value, '$.items."' || t.k || '"'))
        FROM (
            SELECT value AS k FROM json_each(w.value, '$.add')
            UNION
            SELECT value AS k FROM json_each(w.value, '$.retime')
        ) AS t
        WHERE json_type(w.value, '$.items."' || t.k || '"') IS NOT NULL
    ), '{}')))))
    FROM (SELECT value FROM json_each(tv_changes.diff, '$.pending.pools.write') ORDER BY key) AS w
)))
WHERE json_type(diff, '$.pending.pools.write') = 'array'
  AND json_array_length(diff, '$.pending.pools.write') > 0;
-- +goose StatementEnd

-- +goose StatementBegin
-- A change that ended without airing (failed, cancelled, or superseded before
-- it swapped) is never staged again: nothing reads its pool writes, they go (the daily TV clean-up
-- does the same from now on).
UPDATE tv_changes SET diff = json_remove(diff, '$.pending.pools')
WHERE state IN ('failed', 'cancelled', 'superseded')
  AND (state <> 'superseded' OR swapped_at IS NULL)
  AND json_type(diff, '$.pending.pools') IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
