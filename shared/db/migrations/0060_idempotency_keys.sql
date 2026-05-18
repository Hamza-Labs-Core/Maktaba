-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0060 (gap-closure Wave 1 / HLB-315) — idempotency_keys table.
--
-- Durable backing for the API's Idempotency-Key replay store. The
-- in-memory MemoryStore lost every entry on restart and was not shared
-- across replicas, which defeats the point of an idempotency key (a
-- retried mutation could re-execute). This table makes the store
-- survive restarts and be replica-safe.
--
-- The replay identity is the (user_id, Idempotency-Key) pair: empty
-- user_id for unauthenticated routes (those callers all share one
-- bucket, which is the documented contract). It is a TWO-COLUMN
-- composite PRIMARY KEY rather than a single concatenated string —
-- Postgres TEXT/varchar rejects the 0x00 byte
-- (`invalid byte sequence for encoding "UTF8": 0x00`), so a NUL
-- separator (the obvious "userID\x00key" join) is unstorable. The
-- two-column key is also collision-free by construction (no separator
-- to smuggle), and a concurrent duplicate still races on
-- INSERT ... ON CONFLICT (user_id, idem_key) DO NOTHING: exactly one
-- writer wins, the rest are no-ops and replay the winner's row.
--
-- Indexes:
--   * the (user_id, idem_key) primary key serves the Lookup
--     point-read.
--   * idempotency_keys_reaper: created_at scan for the TTL sweep
--     (api/main.go runs the 24h-TTL reaper every 5 min).
--
CREATE TABLE IF NOT EXISTS idempotency_keys (
    user_id        TEXT         NOT NULL DEFAULT '',
    idem_key       TEXT         NOT NULL,
    request_hash   TEXT         NOT NULL,
    status         INTEGER      NOT NULL,
    body           BYTEA        NOT NULL,
    headers        JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, idem_key)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS idempotency_keys_reaper
    ON idempotency_keys (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idempotency_keys_reaper;
DROP TABLE IF EXISTS idempotency_keys;
-- +goose StatementEnd
