-- name: fp_gate.season
-- mode: read
-- scope: catalog
-- tables: series_seasons
SELECT metadata, retired_at, library_id FROM series_seasons WHERE id = $1
