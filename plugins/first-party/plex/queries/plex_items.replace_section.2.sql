-- name: plex_items.replace_section.2
-- mode: write
-- scope: subject $6
-- tables: plex_items*
INSERT INTO plex_items (server, rating_key, section_key, section_title, library_id, video_id, file, duration_ms, plex_updated_at, detail, seen_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
