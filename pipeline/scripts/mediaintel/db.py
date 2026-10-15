"""SQLite cache shared by the mediaintel scanners.

One file holds everything gathered about a library so each step (scan
local files, scan Plex, scan YouTube, link, detect, export) can be re-run
on its own and only does new work. Table shapes follow Maktaba's
migrations where they overlap (``series`` / ``series_episodes`` ≈ slot
0075; YouTube rows ≈ the ``youtube`` enrichment provider of slot 0077).
"""

from __future__ import annotations

import sqlite3
from pathlib import Path

SCHEMA = """
PRAGMA journal_mode = WAL;

-- A video file on disk (one row per path).
CREATE TABLE IF NOT EXISTS files (
    path            TEXT PRIMARY KEY,
    folder          TEXT NOT NULL,
    stem            TEXT NOT NULL,
    ext             TEXT NOT NULL,
    size            INTEGER NOT NULL,
    mtime           REAL NOT NULL,
    duration_s      REAL,
    width           INTEGER,
    height          INTEGER,
    thumb_path      TEXT,
    thumb_dhash     TEXT,              -- 16 hex chars (64-bit dHash)
    youtube_id      TEXT,              -- set by scan-local (info.json) or link
    link_method     TEXT,              -- info_json | exact_title | folded_title | title_duration
    plex_rating_key TEXT,
    plex_section    TEXT,
    plex_library    TEXT,
    plex_title      TEXT,
    probed_size     INTEGER,           -- size/mtime the probe ran against (cache key)
    probed_mtime    REAL
);
CREATE INDEX IF NOT EXISTS files_folder_idx ON files (folder);
CREATE INDEX IF NOT EXISTS files_youtube_idx ON files (youtube_id);

-- Per-folder facts (channel the folder was downloaded from, archive order).
CREATE TABLE IF NOT EXISTS folders (
    folder      TEXT PRIMARY KEY,
    channel_id  TEXT,
    channel_url TEXT,
    channel     TEXT,
    archive_ids TEXT                    -- JSON list, ytdl-archive.txt order
);

-- YouTube video metadata, from a local .info.json or from YouTube itself.
CREATE TABLE IF NOT EXISTS yt_videos (
    youtube_id    TEXT PRIMARY KEY,
    title         TEXT NOT NULL,
    upload_date   TEXT,                 -- ISO date
    date_quality  TEXT,                 -- exact | approximate
    duration_s    REAL,
    channel_id    TEXT,
    channel       TEXT,
    description   TEXT,
    tab           TEXT,                 -- videos | shorts | streams (channel tab it came from)
    source        TEXT NOT NULL,        -- info_json | youtube
    fetched_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS yt_videos_channel_idx ON yt_videos (channel_id);

CREATE TABLE IF NOT EXISTS yt_playlists (
    playlist_id TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    channel_id  TEXT,
    channel     TEXT,
    item_count  INTEGER,
    is_real     INTEGER NOT NULL,
    source      TEXT NOT NULL,
    fetched_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS yt_playlist_items (
    playlist_id TEXT NOT NULL,
    youtube_id  TEXT NOT NULL,
    position    INTEGER,
    PRIMARY KEY (playlist_id, youtube_id)
);
CREATE INDEX IF NOT EXISTS yt_playlist_items_video_idx ON yt_playlist_items (youtube_id);

CREATE TABLE IF NOT EXISTS yt_channels (
    channel_id   TEXT PRIMARY KEY,
    url          TEXT NOT NULL,
    title        TEXT,
    status       TEXT NOT NULL,         -- ok | gone | error
    error        TEXT,
    fetched_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Detected series (≈ Maktaba slot 0075). One row per (series, season).
CREATE TABLE IF NOT EXISTS series (
    id          TEXT PRIMARY KEY,
    library     TEXT NOT NULL,
    name        TEXT NOT NULL,
    year        INTEGER,
    metadata    TEXT NOT NULL DEFAULT '{}',
    detector_version INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS series_episodes (
    series_id  TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    path       TEXT NOT NULL REFERENCES files(path) ON DELETE CASCADE,
    season     INTEGER,
    episode    INTEGER,
    is_user_override INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (series_id, path)
);
CREATE UNIQUE INDEX IF NOT EXISTS series_episodes_path_idx ON series_episodes (path);

-- Videos the detector treats as shorts (never episodes), per detect run.
CREATE TABLE IF NOT EXISTS shorts (
    path   TEXT PRIMARY KEY REFERENCES files(path) ON DELETE CASCADE,
    folder TEXT NOT NULL
);
"""


# columns added after a cache file was first created: (table, column, type)
MIGRATIONS = [
    ("files", "width", "INTEGER"),
    ("files", "height", "INTEGER"),
    # folder-level resume marker: folder mtime at the last complete scan, and whether
    # that scan probed (1) or only listed (0)
    ("folders", "done_mtime", "REAL"),
    ("folders", "done_probe", "INTEGER"),
    # "same show" evidence for episode-length videos: chromaprint of the opening and
    # closing 90 s (base64), and dHashes of frames at fixed points (hex, comma-separated)
    ("files", "audio_head", "TEXT"),
    ("files", "audio_tail", "TEXT"),
    ("files", "audio_version", "INTEGER"),
    ("files", "frame_hashes", "TEXT"),
    ("files", "frames_version", "INTEGER"),
]


def connect(path: str | Path) -> sqlite3.Connection:
    # several tools may run at once (a long YouTube fetch + detect/report): wait for locks
    conn = sqlite3.connect(str(path), timeout=180)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA foreign_keys = ON")
    conn.executescript(SCHEMA)
    for table, col, typ in MIGRATIONS:
        cols = {r[1] for r in conn.execute(f"PRAGMA table_info({table})")}
        if col not in cols:
            conn.execute(f"ALTER TABLE {table} ADD COLUMN {col} {typ}")
    return conn
