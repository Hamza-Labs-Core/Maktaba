-- name: audit.store.3
-- mode: write
-- scope: catalog
-- tables: channel_audits*
DELETE FROM channel_audits WHERE id NOT IN (SELECT id FROM channel_audits ORDER BY id DESC LIMIT $1)
