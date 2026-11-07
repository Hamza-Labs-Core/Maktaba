-- name: link.write_link.3
-- mode: write
-- scope: subject $2
-- tables: media_metadata_enrichment*
INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped, confidence, is_accepted, fetched_at) VALUES ($1,$2,'youtube',$3,$4,$5,$6,$7) ON CONFLICT (video_id, provider, external_id) DO UPDATE SET mapped = EXCLUDED.mapped, confidence = EXCLUDED.confidence, fetched_at = EXCLUDED.fetched_at
