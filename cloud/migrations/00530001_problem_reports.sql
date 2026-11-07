-- +goose Up
-- Epic 53 story 53.16: the problem reports a home's people chose to send to Maktaba, after
-- the home's export scrub (no title, library path, name, email or address). A report names
-- its home (the server and the account that linked it, so Maktaba can answer) and never
-- its sender. Kept 90 days (the api role's sweep), or until the home asks for its reports
-- to be deleted. The files live here, not in an object store: Epic 50.09's buckets hold the
-- database's backups only, and a report is at most 8 MB.
CREATE TABLE IF NOT EXISTS problem_reports (
    id              BIGSERIAL   PRIMARY KEY,
    server_id       UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    -- the report's number on its home: a reply goes back to it
    home_report_id  INTEGER     NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    happened_at     TIMESTAMPTZ,
    app             TEXT        NOT NULL DEFAULT '',
    version         TEXT        NOT NULL DEFAULT '',
    platform        TEXT        NOT NULL DEFAULT '',
    category        TEXT        NOT NULL DEFAULT '',
    status          TEXT        NOT NULL DEFAULT 'new'
                    CHECK (status IN ('new', 'triaged', 'fixed', 'closed')),
    fixed_in        TEXT        NOT NULL DEFAULT '',
    issue_url       TEXT        NOT NULL DEFAULT '',
    reply           TEXT        NOT NULL DEFAULT '',
    replied_at      TIMESTAMPTZ,
    -- the error groups (53.10 fingerprints) its lines name, for the Errors view (53.17)
    fingerprints    TEXT[]      NOT NULL DEFAULT '{}',
    body            JSONB       NOT NULL,
    screenshot      BYTEA,
    UNIQUE (server_id, home_report_id)
);
CREATE INDEX IF NOT EXISTS idx_problem_reports_received ON problem_reports (received_at DESC);
CREATE INDEX IF NOT EXISTS idx_problem_reports_server_replied ON problem_reports (server_id, replied_at);

-- +goose Down
DROP TABLE IF EXISTS problem_reports;
