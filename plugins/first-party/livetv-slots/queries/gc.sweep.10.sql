-- name: gc.sweep.10
-- mode: read
-- scope: catalog
-- tables: tv_changes, tv_release_channels
SELECT rc.channel_id AS cid FROM tv_changes c JOIN tv_release_channels rc ON rc.release_id = c.release_id WHERE c.state IN ('staged','verifying','ready','approved','swapping')
