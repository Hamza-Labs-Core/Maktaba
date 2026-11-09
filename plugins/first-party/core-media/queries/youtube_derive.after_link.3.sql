-- name: youtube_derive.after_link.3
-- mode: read
-- scope: library $1
-- tables: youtube_source_decisions
SELECT seen FROM youtube_source_decisions WHERE library_id = $1
