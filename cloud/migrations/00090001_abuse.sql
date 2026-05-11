-- +goose Up
-- Abuse signal store and blocklist used by the rate limiter and the
-- relay tunnel gate.
CREATE TABLE IF NOT EXISTS abuse_signals (
    id          BIGSERIAL   PRIMARY KEY,
    subject     TEXT        NOT NULL,
    subject_kind TEXT       NOT NULL CHECK (subject_kind IN ('user','server','ip')),
    kind        TEXT        NOT NULL,
    severity    INTEGER     NOT NULL DEFAULT 1,
    detail      JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_abuse_signals_subject ON abuse_signals (subject_kind, subject, created_at DESC);

CREATE TABLE IF NOT EXISTS blocklist (
    subject     TEXT        NOT NULL,
    subject_kind TEXT       NOT NULL CHECK (subject_kind IN ('user','server','ip')),
    reason      TEXT,
    blocked_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ,
    PRIMARY KEY (subject_kind, subject)
);

-- +goose Down
DROP TABLE IF EXISTS blocklist;
DROP TABLE IF EXISTS abuse_signals;
