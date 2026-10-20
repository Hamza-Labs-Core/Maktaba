-- +goose Up
-- +goose StatementBegin
--
-- Slot 0141 (Epic 33, story 33.12) - compact the filler pool writes stored in
-- TV changes. A channel plan's pending edits (tv_changes.diff -> pending ->
-- pools -> write) held the whole of every pool it touched: hundreds of pools
-- of thousands of clips, about 50 MB per plan, one plan an hour. The swap and
-- the staged generation only need the added and retimed clips' lengths (the
-- rest is the live pool, filler_items), which is what the pipeline stores
-- from now on. This keeps, in each pool write, the items of its add and
-- retime lists and drops the others. Every change keeps working (a re-stage
-- of a rolled-back change included): the pools it describes are the same.
--
-- Not reversible: the dropped lengths are the live pool's own. Restore the
-- pre-migration database backup to get the old rows back. The table's disk
-- space comes back with a VACUUM FULL tv_changes after it (not run here: it
-- cannot run inside a migration's transaction).
--
UPDATE tv_changes c
SET diff = jsonb_set(c.diff, '{pending,pools,write}', (
    SELECT COALESCE(jsonb_agg(
        x.w || jsonb_build_object('items', COALESCE((
            SELECT jsonb_object_agg(t.k, x.w -> 'items' -> t.k)
            FROM (
                SELECT jsonb_array_elements_text(
                    CASE WHEN jsonb_typeof(x.w -> 'add') = 'array' THEN x.w -> 'add' ELSE '[]'::jsonb END)
                UNION
                SELECT jsonb_array_elements_text(
                    CASE WHEN jsonb_typeof(x.w -> 'retime') = 'array' THEN x.w -> 'retime' ELSE '[]'::jsonb END)
            ) AS t(k)
            WHERE x.w -> 'items' ? t.k
        ), '{}'::jsonb))
        ORDER BY x.ord), '[]'::jsonb)
    FROM jsonb_array_elements(c.diff -> 'pending' -> 'pools' -> 'write') WITH ORDINALITY AS x(w, ord)
))
WHERE jsonb_typeof(c.diff -> 'pending' -> 'pools' -> 'write') = 'array'
  AND jsonb_array_length(c.diff -> 'pending' -> 'pools' -> 'write') > 0;
-- +goose StatementEnd

-- +goose StatementBegin
-- A change that ended without airing (failed, cancelled, superseded) is never
-- staged again: nothing reads its pool writes, they go (the daily TV clean-up
-- does the same from now on).
UPDATE tv_changes SET diff = diff #- '{pending,pools}'
WHERE state IN ('failed', 'cancelled', 'superseded') AND diff -> 'pending' ? 'pools';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Nothing to undo in place: the dropped lengths are the live pools' own (see
-- the Up's note; restore the backup for the old rows).
SELECT 1;
-- +goose StatementEnd
