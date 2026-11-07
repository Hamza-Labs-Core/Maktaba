-- store-catalog, review (story 54.16): what the checks and the review queue keep per
-- submission. Additive.
ALTER TABLE submissions ADD COLUMN first_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE submissions ADD COLUMN route TEXT NOT NULL DEFAULT '';          -- 'auto' | 'human' | 'blocked'
ALTER TABLE submissions ADD COLUMN route_reasons TEXT NOT NULL DEFAULT '[]';
ALTER TABLE submissions ADD COLUMN runner_state TEXT NOT NULL DEFAULT '';   -- '' | 'dispatched' | 'reported' | 'skipped'
ALTER TABLE submissions ADD COLUMN question TEXT NOT NULL DEFAULT '';
ALTER TABLE submissions ADD COLUMN answer TEXT NOT NULL DEFAULT '';
ALTER TABLE submissions ADD COLUMN asked_at TEXT;
ALTER TABLE submissions ADD COLUMN paused_ms INTEGER NOT NULL DEFAULT 0;   -- time spent waiting for the developer
ALTER TABLE submissions ADD COLUMN reproducible TEXT NOT NULL DEFAULT '';  -- 'yes' | 'no' | '' (54.16 C10's badge)
ALTER TABLE submissions ADD COLUMN backlog_mailed_at TEXT;
