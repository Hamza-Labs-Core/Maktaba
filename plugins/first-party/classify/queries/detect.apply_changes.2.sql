-- name: detect.apply_changes.2
-- mode: write
-- scope: season $1, series $2, library $3
-- tables: series_seasons*
INSERT INTO series_seasons (id, series_id, library_id, season, kind, name, first_air, last_air, median_duration_s, episode_count, membership_hash, detector_version, metadata, retired_at, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,NULL,$14,$14) ON CONFLICT (id) DO UPDATE SET series_id = EXCLUDED.series_id, kind = EXCLUDED.kind, season = EXCLUDED.season, name = EXCLUDED.name, first_air = EXCLUDED.first_air, last_air = EXCLUDED.last_air, median_duration_s = EXCLUDED.median_duration_s, episode_count = EXCLUDED.episode_count, membership_hash = EXCLUDED.membership_hash, detector_version = EXCLUDED.detector_version, metadata = EXCLUDED.metadata, retired_at = NULL, updated_at = EXCLUDED.updated_at
