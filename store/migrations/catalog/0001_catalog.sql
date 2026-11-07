-- store-catalog (story 54.15 §3): developers, their keys and prefixes, plugins, versions,
-- submissions, revocations and the signed lists' versions.
CREATE TABLE IF NOT EXISTS developers (
  account           TEXT PRIMARY KEY,
  agreement_version INTEGER NOT NULL,
  seller_fee_paid   INTEGER NOT NULL DEFAULT 0,
  status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'deleted')),
  created           TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS developer_prefixes (
  prefix      TEXT PRIMARY KEY,                    -- 'dev.example' or 'tv.maktaba.community.<handle>'
  account     TEXT NOT NULL,
  kind        TEXT NOT NULL CHECK (kind IN ('dns', 'community')),
  token       TEXT NOT NULL DEFAULT '',            -- the TXT record's value
  verified_at TEXT,
  checked_at  TEXT,
  lapsed_at   TEXT                                 -- the TXT record was gone at a monthly check
);

CREATE TABLE IF NOT EXISTS developer_keys (
  id           TEXT PRIMARY KEY,                   -- the key id (54.14 §1)
  account      TEXT NOT NULL,
  public_key   TEXT NOT NULL,
  added        TEXT NOT NULL,
  revoked_at   TEXT,
  suspended_at TEXT                                -- an account suspended, deleted or its factors reset
);
CREATE INDEX IF NOT EXISTS developer_keys_account ON developer_keys (account);

CREATE TABLE IF NOT EXISTS plugins (
  id                 TEXT PRIMARY KEY,
  developer          TEXT NOT NULL,
  developer_name     TEXT NOT NULL DEFAULT '',
  names              TEXT NOT NULL DEFAULT '{}',   -- {"en","ar"}
  summaries          TEXT NOT NULL DEFAULT '{}',
  descriptions       TEXT NOT NULL DEFAULT '{}',
  category           TEXT NOT NULL DEFAULT '',
  tags               TEXT NOT NULL DEFAULT '[]',
  icon               TEXT NOT NULL DEFAULT '',
  machine_translated TEXT NOT NULL DEFAULT '[]',   -- languages marked machine translated (54.16 §4)
  status             TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'listed', 'delisted', 'removed')),
  created            TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS versions (
  plugin        TEXT NOT NULL REFERENCES plugins (id),
  version       TEXT NOT NULL,
  sha256        TEXT NOT NULL DEFAULT '',          -- the countersigned package on the CDN
  size          INTEGER NOT NULL DEFAULT 0,
  files_sha256  TEXT NOT NULL DEFAULT '',
  manifest      TEXT NOT NULL DEFAULT '{}',        -- the normalised manifest
  capabilities  TEXT NOT NULL DEFAULT '[]',
  maktaba       TEXT NOT NULL DEFAULT '',
  plugin_api    TEXT NOT NULL DEFAULT '',
  platforms     TEXT NOT NULL DEFAULT '[]',
  developer_key TEXT NOT NULL DEFAULT '',
  review_level  TEXT NOT NULL DEFAULT '' CHECK (review_level IN ('', 'automated', 'reviewed')),
  status        TEXT NOT NULL DEFAULT 'submitted' CHECK (status IN ('submitted', 'checking', 'in_review', 'approved', 'rejected', 'withdrawn')),
  notes_en      TEXT NOT NULL DEFAULT '',
  notes_ar      TEXT NOT NULL DEFAULT '',
  store_sig     TEXT NOT NULL DEFAULT '',
  store_key     TEXT NOT NULL DEFAULT '',          -- the store key that countersigned
  released      TEXT,
  PRIMARY KEY (plugin, version)
);

CREATE TABLE IF NOT EXISTS submissions (
  id            TEXT PRIMARY KEY,
  plugin        TEXT NOT NULL,
  version       TEXT NOT NULL,
  uploaded_by   TEXT NOT NULL,
  package_key   TEXT NOT NULL,                     -- in the private bucket
  source_key    TEXT NOT NULL,
  package_sha256 TEXT NOT NULL,
  package_size  INTEGER NOT NULL,
  source_size   INTEGER NOT NULL,
  status        TEXT NOT NULL DEFAULT 'uploading' CHECK (status IN ('uploading', 'checking', 'in_review', 'asked', 'approved', 'rejected', 'withdrawn')),
  report_key    TEXT NOT NULL DEFAULT '',
  flags         TEXT NOT NULL DEFAULT '[]',
  reviewer      TEXT NOT NULL DEFAULT '',
  decision      TEXT NOT NULL DEFAULT '',
  reasons       TEXT NOT NULL DEFAULT '[]',
  notes         TEXT NOT NULL DEFAULT '',
  created       TEXT NOT NULL,
  checked_at    TEXT,
  decided_at    TEXT
);
CREATE INDEX IF NOT EXISTS submissions_status ON submissions (status, created);

CREATE TABLE IF NOT EXISTS revocations (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  plugin_id        TEXT NOT NULL,
  versions         TEXT NOT NULL,                  -- a semver range
  developer_key_id TEXT NOT NULL DEFAULT '',
  action           TEXT NOT NULL CHECK (action IN ('disable', 'warn', 'delist')),
  reason_code      TEXT NOT NULL,
  issued           TEXT NOT NULL,
  issued_by        TEXT NOT NULL,
  lifted_at        TEXT
);

-- the signed lists' versions: catalog-<major>, revoked
CREATE TABLE IF NOT EXISTS lists (
  name    TEXT PRIMARY KEY,
  version INTEGER NOT NULL,
  sha256  TEXT NOT NULL,
  issued  TEXT NOT NULL
);

-- id squatting: words a reviewer flags in a plugin id (54.15 edge cases)
CREATE TABLE IF NOT EXISTS flagged_words (
  word     TEXT PRIMARY KEY,
  added_by TEXT NOT NULL,
  at       TEXT NOT NULL
);
