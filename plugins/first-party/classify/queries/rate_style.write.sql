-- name: rate_style.write
-- mode: write
-- scope: catalog
-- tables: season_style*
INSERT INTO season_style (season_id, style, tier, confidence, reasons, sampled, style_version, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (season_id) DO UPDATE SET style = EXCLUDED.style, tier = EXCLUDED.tier, confidence = EXCLUDED.confidence, reasons = EXCLUDED.reasons, sampled = EXCLUDED.sampled, style_version = EXCLUDED.style_version, updated_at = EXCLUDED.updated_at
