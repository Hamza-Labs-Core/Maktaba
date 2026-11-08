-- name: tag.channel_names
-- mode: read
-- scope: library $1
-- tables: youtube_channels
SELECT title FROM youtube_channels WHERE library_id = $1 AND title IS NOT NULL ORDER BY channel_id
