-- store-installs (story 54.15 §3, §5, §10): homes linked to a store account and what they
-- report having installed. A home's store id is the store's own, unrelated to any cloud id.
CREATE TABLE IF NOT EXISTS homes (
  id             TEXT PRIMARY KEY,
  account        TEXT NOT NULL,
  public_key     TEXT NOT NULL,                    -- the home store key (Ed25519, base64)
  name           TEXT NOT NULL DEFAULT '',
  server_version TEXT NOT NULL DEFAULT '',
  platform       TEXT NOT NULL DEFAULT '',
  linked_at      TEXT NOT NULL,
  unlinked_at    TEXT
);
CREATE INDEX IF NOT EXISTS homes_account ON homes (account);
CREATE INDEX IF NOT EXISTS homes_key ON homes (public_key);

CREATE TABLE IF NOT EXISTS installs (
  home       TEXT NOT NULL,
  plugin     TEXT NOT NULL,
  version    TEXT NOT NULL,
  first_seen TEXT NOT NULL,
  last_seen  TEXT NOT NULL,
  PRIMARY KEY (home, plugin)
);
CREATE INDEX IF NOT EXISTS installs_plugin ON installs (plugin);

CREATE TABLE IF NOT EXISTS install_counts (
  plugin TEXT NOT NULL,
  month  TEXT NOT NULL,                            -- YYYY-MM
  homes  INTEGER NOT NULL,
  PRIMARY KEY (plugin, month)
);
