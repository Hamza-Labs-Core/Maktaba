-- name: extract.commit_extract
-- mode: write
-- scope: audio_cache $1, subject $2, audio_track $3
-- tables: audio_cache*
INSERT INTO audio_cache (content_hash, video_id, audio_track_id, path, bytes)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (content_hash) DO UPDATE SET
    video_id       = EXCLUDED.video_id,
    audio_track_id = EXCLUDED.audio_track_id,
    path           = EXCLUDED.path,
    bytes          = EXCLUDED.bytes
