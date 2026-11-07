-- store-catalog, report logs (#758): a report from a home may carry the plugin's own recent
-- log lines, scrubbed and capped at the home, kept in the private bucket under report-logs/
-- (deleted after 90 days by its lifecycle rule) and read by the store's reviewers. Additive.
ALTER TABLE reports ADD COLUMN logs_key TEXT NOT NULL DEFAULT '';
