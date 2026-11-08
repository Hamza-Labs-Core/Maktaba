-- name: youtube_derive.apply
-- mode: write
-- scope: library $1
-- tables: youtube_channel_libraries*
DELETE FROM youtube_channel_libraries WHERE library_id = $1
