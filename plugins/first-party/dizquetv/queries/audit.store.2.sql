-- name: audit.store.2
-- mode: write
-- scope: catalog
-- tables: channel_audit_channels*
INSERT INTO channel_audit_channels (audit_id, number, name, theme, hours, shares, filler_clips, filler_share, detail) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
