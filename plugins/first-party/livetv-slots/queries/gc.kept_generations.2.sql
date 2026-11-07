-- name: gc.kept_generations.2
-- mode: read
-- scope: catalog
-- tables: tv_changes, tv_releases
SELECT r.sched_gen AS g FROM tv_changes c JOIN tv_releases r ON r.id = c.release_id WHERE c.state IN ('staged','verifying','ready','approved','swapping','draft')
