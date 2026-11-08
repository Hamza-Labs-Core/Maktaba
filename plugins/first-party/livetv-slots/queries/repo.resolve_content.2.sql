-- name: repo.resolve_content.2
-- mode: read
-- scope: catalog
-- tables: videos
SELECT id, title, duration_sec
                FROM videos
                WHERE duration_sec IS NOT NULL AND duration_sec > 0 AND deleted_at IS NULL
                ORDER BY title ASC
