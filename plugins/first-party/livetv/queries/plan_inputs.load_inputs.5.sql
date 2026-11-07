-- name: plan_inputs.load_inputs.5
-- mode: read
-- scope: catalog
-- tables: libraries, series, series_seasons
SELECT ss.id AS id, ss.series_id AS series_id, ss.name AS name, ss.kind AS kind, ss.season AS season, ss.first_air AS first_air, ss.median_duration_s AS median, ss.metadata AS metadata, s.metadata AS series_meta, l.name AS library FROM series_seasons ss JOIN series s ON s.id = ss.series_id LEFT JOIN libraries l ON l.id = ss.library_id WHERE ss.retired_at IS NULL ORDER BY ss.id
