-- name: youtube_sources.ensure_channel_sources
-- mode: read
-- scope: library $1
-- tables: youtube_channels, youtube_sources
SELECT c.channel_id AS cid, c.url AS url, c.title AS title FROM youtube_channels c WHERE c.library_id = $1 AND NOT EXISTS (SELECT 1 FROM youtube_sources s  WHERE s.kind = 'channel' AND s.external_id = c.channel_id)
