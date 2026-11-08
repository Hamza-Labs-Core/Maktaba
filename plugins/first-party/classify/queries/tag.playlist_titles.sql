-- name: tag.playlist_titles
-- mode: read
-- scope: youtube_playlist $1
-- tables: youtube_playlists
SELECT playlist_id, title FROM youtube_playlists WHERE playlist_id = ANY($1::text[])
