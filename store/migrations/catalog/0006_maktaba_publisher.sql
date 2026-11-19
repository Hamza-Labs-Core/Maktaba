-- store-catalog (story 54.45 §2): Maktaba's own publisher account. One prefix of kind
-- 'maktaba' ('tv.maktaba'), set by a store admin, covers every tv.maktaba.* id but the
-- community prefixes (tv.maktaba.community.*). SQLite changes a CHECK only by a new table:
-- the rows are copied as they are.
CREATE TABLE developer_prefixes_new (
  prefix      TEXT PRIMARY KEY,                    -- 'dev.example', 'tv.maktaba.community.<handle>' or 'tv.maktaba'
  account     TEXT NOT NULL,
  kind        TEXT NOT NULL CHECK (kind IN ('dns', 'community', 'maktaba')),
  token       TEXT NOT NULL DEFAULT '',            -- the TXT record's value
  verified_at TEXT,
  checked_at  TEXT,
  lapsed_at   TEXT                                 -- the TXT record was gone at a monthly check
);
INSERT INTO developer_prefixes_new (prefix, account, kind, token, verified_at, checked_at, lapsed_at)
  SELECT prefix, account, kind, token, verified_at, checked_at, lapsed_at FROM developer_prefixes;
DROP TABLE developer_prefixes;
ALTER TABLE developer_prefixes_new RENAME TO developer_prefixes;
