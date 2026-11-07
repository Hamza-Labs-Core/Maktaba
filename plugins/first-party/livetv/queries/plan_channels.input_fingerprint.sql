-- name: plan_channels.input_fingerprint
-- mode: read
-- scope: catalog
-- tables: processing_jobs
SELECT j.stage AS stage, j.subject_id AS sid, j.output_hash AS h FROM processing_jobs j WHERE j.stage IN ('detect','tag','check_tags','check_clips','books','rate_style','topic_collections','promo_links','clip_groups') AND j.state = 'done' AND j.outcome IN ('changed','unchanged','skipped_unchanged','imported') AND j.id = (SELECT max(k.id) FROM processing_jobs k WHERE k.subject_kind = j.subject_kind  AND k.subject_id = j.subject_id AND k.stage = j.stage AND k.state = 'done'  AND k.outcome IN ('changed','unchanged','skipped_unchanged','imported')) ORDER BY j.stage, j.subject_id
