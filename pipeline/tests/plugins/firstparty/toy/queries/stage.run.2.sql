-- name: stage.run.2
-- mode: write
-- scope: library $1
-- tables: fp_toy_out*
DELETE FROM fp_toy_out WHERE library_id = $1
