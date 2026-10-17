"""Indexing jobs: one row per (file, stage), so every piece of per-file work is tracked.

Shaped like Maktaba's ``processing_jobs`` (slot 0002: ``stage``, ``state``,
``attempts``, ``max_attempts``, ``error``), with the cache's file path standing in
for ``video_id``. When Maktaba ingests these libraries the rows map 1:1. ``probe``
and ``thumbnail`` are existing stages; ``signature`` and ``visual_style`` would join
the ``processing_jobs.stage`` CHECK the way slot 0049 added ``chapter_infer``.

Stages:

- ``probe``: length/size/resolution (ffprobe, or taken from Plex)
- ``thumbnail``: thumbnail dHash
- ``signature``: audio intro/outro fingerprints and frame dHashes
- ``visual_style``: production-style sample

A job is ``done`` for one file size/mtime and stage version; a changed file or a
bumped version makes it pending again. A failure keeps its error and is retried
until ``max_attempts``, so a broken file never stalls every run.
"""

from __future__ import annotations

import sqlite3

SCHEMA = """
CREATE TABLE IF NOT EXISTS index_jobs (
    path          TEXT NOT NULL,              -- ≈ processing_jobs.video_id
    stage         TEXT NOT NULL,              -- probe | thumbnail | signature | visual_style
    version       INTEGER NOT NULL,           -- stage version the result was made with
    size          INTEGER,                    -- file size/mtime the job ran against
    mtime         REAL,
    state         TEXT NOT NULL CHECK (state IN ('done', 'failed')),
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 3,
    error         TEXT,
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (path, stage)
);
CREATE INDEX IF NOT EXISTS index_jobs_stage_idx ON index_jobs (stage, state);
"""

_ensured: set[int] = set()


def ensure(conn: sqlite3.Connection) -> None:
    """Create the table and adopt work done before jobs were tracked, so it is never
    repeated. Cheap after the first call per connection."""
    # an id() alone is not enough: Python reuses the id of a closed connection for a new
    # one (a fresh database in the next test, or after a reconnect), so check the table
    if (
        id(conn) in _ensured
        and conn.execute(
            "SELECT 1 FROM sqlite_master WHERE type='table' AND name='index_jobs'"
        ).fetchone()
    ):
        return
    conn.executescript(SCHEMA)
    _ensured.add(id(conn))
    if conn.execute("SELECT 1 FROM index_jobs LIMIT 1").fetchone():
        return
    conn.executescript(
        """
        INSERT OR IGNORE INTO index_jobs (path, stage, version, size, mtime, state, attempts)
          SELECT path, 'probe', 1, size, mtime, 'done', 1 FROM files
          WHERE width IS NOT NULL AND probed_size = size AND probed_mtime = mtime;
        INSERT OR IGNORE INTO index_jobs (path, stage, version, size, mtime, state, attempts)
          SELECT path, 'thumbnail', 1, size, mtime, 'done', 1 FROM files
          WHERE thumb_dhash IS NOT NULL;
        """
    )
    has_vm = conn.execute(
        "SELECT 1 FROM sqlite_master WHERE type='table' AND name='visual_metrics'"
    ).fetchone()
    if has_vm:
        conn.execute(
            """INSERT OR IGNORE INTO index_jobs (path, stage, version, size, mtime, state, attempts)
               SELECT path, 'visual_style', version, probed_size, probed_mtime, 'done', 1
               FROM visual_metrics"""
        )
    conn.commit()


def needed(
    conn: sqlite3.Connection, path: str, stage: str, version: int, size: int, mtime: float
) -> bool:
    """True when the job has not succeeded for this file state and version, and has not
    already used up its attempts for it."""
    row = conn.execute(
        "SELECT version, size, mtime, state, attempts, max_attempts FROM index_jobs "
        "WHERE path=? AND stage=?",
        (path, stage),
    ).fetchone()
    if row is None or row[0] != version or row[1] != size or row[2] != mtime:
        return True
    return bool(row[3] != "done" and row[4] < row[5])


def record(
    conn: sqlite3.Connection,
    path: str,
    stage: str,
    version: int,
    size: int | None,
    mtime: float | None,
    error: str | None = None,
) -> None:
    """Store the outcome (the caller commits). Attempts restart when the file changed."""
    conn.execute(
        """INSERT INTO index_jobs (path, stage, version, size, mtime, state, attempts, error)
           VALUES (?,?,?,?,?,?,1,?)
           ON CONFLICT (path, stage) DO UPDATE SET
             attempts = CASE WHEN index_jobs.size IS excluded.size
                              AND index_jobs.mtime IS excluded.mtime
                              AND index_jobs.version = excluded.version
                             THEN index_jobs.attempts + 1 ELSE 1 END,
             version=excluded.version, size=excluded.size, mtime=excluded.mtime,
             state=excluded.state, error=excluded.error,
             updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')""",
        (path, stage, version, size, mtime, "failed" if error else "done", error),
    )


def summary(conn: sqlite3.Connection, folder: str | None = None) -> None:
    """Per folder and stage: done / failed / gave up, against the files it applies to."""
    ensure(conn)
    rows = conn.execute(
        """SELECT f.folder, j.stage, SUM(j.state='done'), SUM(j.state='failed'),
                  SUM(j.state='failed' AND j.attempts >= j.max_attempts)
           FROM index_jobs j JOIN files f ON f.path=j.path
           WHERE (? IS NULL OR f.folder LIKE ?)
           GROUP BY f.folder, j.stage ORDER BY f.folder, j.stage""",
        (folder, f"%{folder}%"),
    ).fetchall()
    totals = {
        r[0]: (r[1], r[2] or 0)
        for r in conn.execute(
            """SELECT folder, COUNT(*), SUM(duration_s >= 180) FROM files
               WHERE (? IS NULL OR folder LIKE ?) GROUP BY folder""",
            (folder, f"%{folder}%"),
        )
    }
    print(f"{'folder':45} {'stage':12} {'done':>6} {'failed':>6} {'gave up':>7} {'of':>6}")
    for fold, stage, done, failed, gave_up in rows:
        n_all, n_long = totals.get(fold, (0, 0))
        of = n_long if stage in ("signature", "thumbnail") else n_all
        name = fold.replace("\\", "/").rsplit("/", 1)[-1][:45]
        print(f"{name:45} {stage:12} {done:6} {failed:6} {gave_up:7} {of:6}")
