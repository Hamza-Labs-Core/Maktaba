-- name: fp_gate.season
-- mode: read
-- scope: season $1
-- tables: series_seasons
SELECT metadata, retired_at, library_id FROM series_seasons WHERE id = $1
