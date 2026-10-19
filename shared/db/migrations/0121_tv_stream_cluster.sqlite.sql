-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0121 (Epic 32, 32.1/32.2). A SQLite home is a single
-- machine (no nodes, Epic 31): no roles, no functions; the streaming service writes its
-- row directly. JSONB becomes TEXT, BOOLEAN INTEGER, BYTEA BLOB, TIMESTAMPTZ TEXT.
--
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS enabled INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS draining INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS stream_base_url_source TEXT NOT NULL DEFAULT 'reported' CHECK (stream_base_url_source IN ('reported','admin'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS encoders TEXT NOT NULL DEFAULT '[]';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS capacity_override INTEGER;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS weight INTEGER NOT NULL DEFAULT 1;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS active TEXT NOT NULL DEFAULT '[]';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS load TEXT NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS can_push TEXT NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS can_live INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS unfit TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts ADD COLUMN IF NOT EXISTS version TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE streaming_hosts SET enabled = (role = 'home') WHERE enabled = 0;

CREATE TABLE IF NOT EXISTS channel_placements (
    channel_id   TEXT    PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    host_id      TEXT    NOT NULL REFERENCES streaming_hosts(host_id) ON DELETE CASCADE,
    generation   INTEGER NOT NULL,
    state        TEXT    NOT NULL CHECK (state IN ('placing', 'live', 'moving', 'released')),
    reason       TEXT    NOT NULL,
    placed_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    acked_at     TEXT,
    released_at  TEXT
);
CREATE INDEX IF NOT EXISTS channel_placements_host_idx
    ON channel_placements (host_id) WHERE state <> 'released';

-- the generation sequence of Postgres: one row holding the last value
CREATE TABLE IF NOT EXISTS tv_placement_generation_seq (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    last  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS tv_cluster_secret (
    id          INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    stream_key  BLOB    NOT NULL,
    previous    BLOB,
    rotated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tv_cluster_secret;
DROP TABLE IF EXISTS tv_placement_generation_seq;
DROP TABLE IF EXISTS channel_placements;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts DROP COLUMN version;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts DROP COLUMN unfit;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts DROP COLUMN can_live;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts DROP COLUMN can_push;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts DROP COLUMN load;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts DROP COLUMN active;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts DROP COLUMN weight;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts DROP COLUMN capacity_override;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts DROP COLUMN encoders;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts DROP COLUMN stream_base_url_source;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts DROP COLUMN draining;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_hosts DROP COLUMN enabled;
-- +goose StatementEnd
