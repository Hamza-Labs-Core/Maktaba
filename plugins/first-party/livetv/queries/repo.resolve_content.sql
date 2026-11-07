-- name: repo.resolve_content
-- mode: read
-- scope: library $1
-- tables: videos
SELECT id, title, duration_sec
                FROM videos
                WHERE library_id = $1 AND duration_sec IS NOT NULL AND duration_sec > 0
                  AND deleted_at IS NULL
                ORDER BY title ASC
