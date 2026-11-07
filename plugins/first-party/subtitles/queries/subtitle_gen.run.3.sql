-- name: subtitle_gen.run.3
-- mode: write
-- scope: subject $1
-- tables: subtitle_files*
INSERT INTO subtitle_files (video_id, transcript_id, language, format, source, path, byte_size, sha256, is_external, metadata) VALUES ($1, $2, $3, $4, 'generated', $5, $6, $7, true, $8)
