-- name: livetv.device
-- mode: read
-- scope: catalog
-- tables: hdhr_device
SELECT device_id, friendly_name, enabled FROM hdhr_device WHERE id = 1
