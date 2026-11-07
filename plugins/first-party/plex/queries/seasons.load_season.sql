-- name: seasons.load_season
-- mode: read
-- scope: catalog
-- tables: libraries, series, series_seasons
SELECT ss.id AS id, ss.season AS season, ss.kind AS kind, ss.membership_hash AS membership_hash, ss.retired_at AS retired_at, ss.metadata AS metadata, COALESCE(s.name_override, s.name) AS series, COALESCE(l.name, '') AS library, (SELECT count(*) FROM series_seasons x WHERE x.series_id = ss.series_id   AND x.retired_at IS NULL) AS n FROM series_seasons ss JOIN series s ON s.id = ss.series_id LEFT JOIN libraries l ON l.id = ss.library_id WHERE ss.id = $1
