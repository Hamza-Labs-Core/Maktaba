-- name: tag.write_episode_tags.2
-- mode: write
-- scope: subject $1
-- tables: episode_tags*
INSERT INTO episode_tags (video_id, tag, score) VALUES ($1,$2,$3)
