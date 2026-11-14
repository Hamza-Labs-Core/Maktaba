-- +goose Up
-- Story 50.17: several api + worker nodes at once.
--
-- cloud_rate_buckets: the token buckets every node shares for the limits that guard against
-- brute force (sign-in, password reset, mail, claim redeem, the console's sign-in). One
-- UPSERT per request (cloud/internal/ratelimit/pg.go); the worker deletes the buckets idle
-- for a day. key is "<limit>:<subject>", e.g. "auth:ip:203.0.113.7".
CREATE TABLE IF NOT EXISTS cloud_rate_buckets (
    key        TEXT PRIMARY KEY,
    tokens     DOUBLE PRECISION NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS cloud_rate_buckets_updated_at ON cloud_rate_buckets (updated_at);

-- cloud_leaders: who holds each leader lock (cloud/internal/db/leader.go), renewed every 15 s
-- through the lock's own connection. A heartbeat over 60 s old lets another node end the
-- holder's backend (a hung process keeps its connection, and so its lock).
CREATE TABLE IF NOT EXISTS cloud_leaders (
    lock_id      BIGINT PRIMARY KEY,
    node         TEXT NOT NULL,
    backend_pid  INTEGER NOT NULL,
    since        TIMESTAMPTZ NOT NULL DEFAULT now(),
    heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS cloud_leaders;
DROP TABLE IF EXISTS cloud_rate_buckets;
