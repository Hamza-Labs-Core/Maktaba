-- store-catalog, ratings and reports (story 54.18 §3, §4). Additive.
CREATE TABLE IF NOT EXISTS ratings (
  id         TEXT PRIMARY KEY,
  plugin     TEXT NOT NULL,
  account    TEXT NOT NULL,
  author     TEXT NOT NULL DEFAULT '',          -- the display name shown
  stars      INTEGER NOT NULL CHECK (stars BETWEEN 1 AND 5),
  text       TEXT NOT NULL DEFAULT '',          -- plain text, at most 2,000 characters
  created    TEXT NOT NULL,
  updated    TEXT NOT NULL,
  reply      TEXT NOT NULL DEFAULT '',          -- the developer's one reply
  replied_at TEXT,
  reported   INTEGER NOT NULL DEFAULT 0,
  hidden_at  TEXT,
  UNIQUE (plugin, account)
);
CREATE INDEX IF NOT EXISTS ratings_plugin ON ratings (plugin);

CREATE TABLE IF NOT EXISTS reports (
  id         TEXT PRIMARY KEY,
  plugin     TEXT NOT NULL,
  version    TEXT NOT NULL DEFAULT '',
  reason     TEXT NOT NULL CHECK (reason IN ('malware', 'data_misuse', 'does_not_work', 'misleading', 'offensive', 'copyright', 'other')),
  details    TEXT NOT NULL DEFAULT '',
  account    TEXT NOT NULL DEFAULT '',          -- a verified store account, or
  home       TEXT NOT NULL DEFAULT '',          -- a linked home's signature
  urgent     INTEGER NOT NULL DEFAULT 0,        -- malware and data misuse: triaged within 24 hours
  status     TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
  outcome    TEXT NOT NULL DEFAULT '' CHECK (outcome IN ('', 'no_action', 'contact_developer', 'warn', 'delist', 'disable', 'suspend_developer')),
  created    TEXT NOT NULL,
  resolved_at TEXT,
  resolved_by TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS reports_open ON reports (status, urgent, created);
