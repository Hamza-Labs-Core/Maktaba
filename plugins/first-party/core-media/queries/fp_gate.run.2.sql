-- name: fp_gate.run.2
-- mode: write
-- scope: catalog
-- tables: series_seasons*
UPDATE series_seasons SET metadata = $2, updated_at = $3 WHERE id = $1
