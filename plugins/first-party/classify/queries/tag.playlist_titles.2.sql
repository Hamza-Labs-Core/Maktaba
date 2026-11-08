-- name: tag.playlist_titles.2
-- mode: read
-- scope: catalog
-- tables: youtube_playlists
SELECT playlist_id, title FROM youtube_playlists WHERE playlist_id IN (SELECT value FROM json_each($1))
