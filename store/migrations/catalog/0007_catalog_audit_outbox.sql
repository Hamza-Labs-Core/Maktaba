-- store-catalog: durable outbox for cross-database admin audit rows whose catalog mutation
-- must always produce a recoverable audit record in store-accounts.
CREATE TABLE IF NOT EXISTS catalog_audit_outbox (
  id        TEXT PRIMARY KEY,
  actor     TEXT NOT NULL,
  action    TEXT NOT NULL,
  target    TEXT NOT NULL DEFAULT '',
  at        TEXT NOT NULL,
  ip_prefix TEXT NOT NULL DEFAULT '',
  result    TEXT NOT NULL DEFAULT 'ok'
);
