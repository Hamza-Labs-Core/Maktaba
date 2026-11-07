-- name: links.put_link
-- mode: write
-- scope: catalog
-- tables: external_links*
INSERT INTO external_links (target, subject_kind, subject_id, external_id, title, content_hash, created_by_tool, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (target, subject_kind, subject_id) DO UPDATE SET external_id = EXCLUDED.external_id, title = EXCLUDED.title, content_hash = EXCLUDED.content_hash, created_by_tool = EXCLUDED.created_by_tool, updated_at = EXCLUDED.updated_at
