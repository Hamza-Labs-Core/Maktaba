-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS media_info (
    video_id      TEXT PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    container     TEXT,
    video_codec   TEXT,
    width         INTEGER,
    height        INTEGER,
    fps           REAL,
    bitrate_kbps  INTEGER,
    has_subtitles INTEGER NOT NULL DEFAULT 0,
    raw_ffprobe   TEXT    NOT NULL DEFAULT '{}',
    probed_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS audio_tracks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id     TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    track_index  INTEGER NOT NULL,
    codec        TEXT,
    channels     INTEGER,
    sample_rate  INTEGER,
    language     TEXT    NOT NULL DEFAULT 'und',
    title        TEXT,
    is_default   INTEGER NOT NULL DEFAULT 0,
    disposition  TEXT    NOT NULL DEFAULT '{}',
    detected_language            TEXT,
    detected_language_confidence REAL CHECK (detected_language_confidence IS NULL
                                              OR (detected_language_confidence >= 0
                                                  AND detected_language_confidence <= 1)),
    UNIQUE (video_id, track_index)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS audio_tracks_video_idx ON audio_tracks (video_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audio_tracks;
DROP TABLE IF EXISTS media_info;
-- +goose StatementEnd
