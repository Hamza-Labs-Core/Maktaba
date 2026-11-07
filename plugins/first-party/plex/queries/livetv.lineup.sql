-- name: livetv.lineup
-- mode: read
-- scope: catalog
-- tables: channels
SELECT number FROM channels WHERE enabled = true ORDER BY sort_order ASC, number ASC
