-- store-catalog, flows (story 54.16 C12, README §4.12, story 54.39): a flow is checked at
-- verification and approved or refused as a whole. Additive.

-- One upload of several packages: the members that change in a flow, submitted together.
-- C12 runs once each has passed its own checks (C1 to C11).
CREATE TABLE IF NOT EXISTS flow_submissions (
  id           TEXT PRIMARY KEY,
  flow_id      TEXT NOT NULL,
  flow_version TEXT NOT NULL,
  uploaded_by  TEXT NOT NULL,
  members      TEXT NOT NULL,              -- JSON: the plugin ids uploaded in it
  status       TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'rejected', 'in_review', 'approved')),
  report_key   TEXT NOT NULL DEFAULT '',   -- C12's report: findings in English and Arabic, the graph
  created      TEXT NOT NULL,
  decided_at   TEXT
);

-- Each submission's flow: its group (none for a plugin alone, a flow of one), the sha256 of
-- the flow.toml it carries (byte-identical in every member), and where C12 is for it.
ALTER TABLE submissions ADD COLUMN flow_submission TEXT;
ALTER TABLE submissions ADD COLUMN flow_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE submissions ADD COLUMN flow_state TEXT NOT NULL DEFAULT '';  -- '' | 'waiting' (its checks passed, the others' not yet) | 'checked'

-- The approved flows: what a later version is compared with (a new member, event or
-- subscription goes to a person, 54.16 §2) and what the listing draws.
CREATE TABLE IF NOT EXISTS flows (
  id           TEXT NOT NULL,
  version      TEXT NOT NULL,
  members      TEXT NOT NULL,               -- JSON: [plugin id]
  events       TEXT NOT NULL,               -- JSON: [plugin:<id>/<event>]
  subscriptions TEXT NOT NULL,              -- JSON: ["<from> -> <to>"]
  graph        TEXT NOT NULL,               -- the checker's normalised graph (graphJson)
  flow_sha256  TEXT NOT NULL,
  approved_at  TEXT NOT NULL,
  PRIMARY KEY (id, version)
);

-- The members' latest flow, for the listing page.
CREATE TABLE IF NOT EXISTS flow_members (
  plugin   TEXT NOT NULL,
  flow_id  TEXT NOT NULL,
  version  TEXT NOT NULL,
  PRIMARY KEY (plugin, flow_id)
);
