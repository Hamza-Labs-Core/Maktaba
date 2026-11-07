-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0198 (plugins: the lineage a write leaves on its
-- subject). Plugins run on a Postgres home only (slot 0194); this keeps the schema in step.
--
CREATE TABLE IF NOT EXISTS plugin_write_lineage (
    subject_kind TEXT NOT NULL,
    subject_id   TEXT NOT NULL,
    plugin_id    TEXT NOT NULL,
    stage        TEXT NOT NULL,
    lineage      TEXT NOT NULL,
    written_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (subject_kind, subject_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plugin_write_lineage;
-- +goose StatementEnd
