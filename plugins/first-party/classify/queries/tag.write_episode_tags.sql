-- name: tag.write_episode_tags
-- mode: write
-- scope: subject $1
-- tables: episode_tags*
DELETE FROM episode_tags WHERE video_id = $1
