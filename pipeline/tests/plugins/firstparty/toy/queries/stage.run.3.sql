-- name: stage.run.3
-- mode: write
-- scope: subject $2
-- tables: fp_toy_out*
INSERT INTO fp_toy_out (library_id, video_id, title, n) VALUES ($1, $2, $3, $4)
