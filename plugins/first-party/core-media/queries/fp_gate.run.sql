-- name: fp_gate.run
-- mode: read
-- scope: season $1
-- tables: series_seasons
SELECT metadata FROM series_seasons WHERE id = $1
