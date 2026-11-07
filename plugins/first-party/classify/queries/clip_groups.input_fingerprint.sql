-- name: clip_groups.input_fingerprint
-- mode: read
-- scope: library $1
-- tables: series_seasons
SELECT id, membership_hash FROM series_seasons WHERE library_id = $1 AND retired_at IS NULL ORDER BY id
