-- name: store.save_candidate.4
-- mode: write
-- scope: library $1
-- tables: videos*
INSERT INTO videos
       (library_id, content_hash, path, filename, size_bytes, mtime,
        last_seen_at, state, id)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'discovered', $8)
ON CONFLICT (content_hash) DO UPDATE SET
    path         = excluded.path,
    filename     = excluded.filename,
    size_bytes   = excluded.size_bytes,
    mtime        = excluded.mtime,
    last_seen_at = excluded.last_seen_at,
    updated_at   = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
 WHERE videos.library_id = excluded.library_id
RETURNING id
