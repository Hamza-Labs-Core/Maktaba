-- name: topic_collections.stored_series
-- mode: read
-- scope: library $1
-- tables: series_seasons
SELECT ss.id AS id, ss.series_id AS series_id, ss.season AS season, ss.kind AS kind, ss.name AS name, ss.metadata AS metadata FROM series_seasons ss WHERE ss.library_id = $1 AND ss.retired_at IS NULL ORDER BY ss.series_id, ss.season
