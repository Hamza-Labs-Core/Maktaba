-- name: generation.open_generations
-- mode: read
-- scope: catalog
-- tables: tv_changes, tv_releases
SELECT r.id AS id, r.sched_gen AS g, c.diff AS diff FROM tv_changes c JOIN tv_releases r ON r.id = c.release_id WHERE c.state IN ('staged','verifying','ready','approved','swapping')
