-- name: shows.plex_output
-- mode: read
-- scope: catalog
-- tables: processing_jobs
SELECT output_hash FROM processing_jobs WHERE stage = 'plex_collection' AND subject_kind = 'season' AND subject_id = $1 AND state = 'done' AND outcome IN ('changed','unchanged','skipped_unchanged') ORDER BY id DESC LIMIT 1
