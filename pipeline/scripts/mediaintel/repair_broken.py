"""``repair-broken``: download again the files ffprobe cannot read.

A file whose probe failed is read again first: a passing failure (a busy share, a
timeout) is no reason to replace it. A file that really is broken (an unfinished
download: "moov atom not found") and came from YouTube is fetched again beside it, put in
its container, checked with ffprobe, and only then put in its place; the broken file is
kept as ``<name>.broken``. A video no longer on YouTube, or a file without a YouTube id,
is reported and left as it is.
"""

from __future__ import annotations

import shutil
import sqlite3
import tempfile
from pathlib import Path

from maktaba_pipeline.library_mgmt import repair as shared
from maktaba_pipeline.library_mgmt.repair import FORMAT, FORMAT_MP4, PARTIAL

from . import jobs
from .bins import FFMPEG, FFPROBE

__all__ = ["FORMAT", "FORMAT_MP4", "broken", "run"]
_PARTIAL = PARTIAL


def broken(conn: sqlite3.Connection) -> list[tuple[str, str | None]]:
    """(path, YouTube id) of every file whose probe failed."""
    return [
        (r[0], r[1])
        for r in conn.execute(
            """SELECT j.path, f.youtube_id FROM index_jobs j LEFT JOIN files f ON f.path = j.path
               WHERE j.stage = 'probe' AND j.state = 'failed' ORDER BY j.path"""
        )
    ]


def check(path: str) -> tuple[float | None, int | None, int | None, str]:
    """(duration, width, height, why it failed) of a file read now."""
    p = shared.check(path, ffprobe=FFPROBE)
    return p.duration, p.width, p.height, p.why


def download_args(youtube_id: str, target: Path, cookies_from_browser: str | None,
                  work: Path) -> list[str]:  # fmt: skip
    return shared.download_args(youtube_id, target, cookies_from_browser, work)


_made = shared.made
_backup_name = shared.backup_name


def repair_one(conn: sqlite3.Connection, path: str, youtube_id: str,
               cookies_from_browser: str | None = None) -> str:  # fmt: skip
    """Fetch, check and swap one file; returns what happened (one line)."""
    work = Path(tempfile.mkdtemp(prefix="maktaba-repair-"))
    try:
        got = shared.fetch_and_swap(path, youtube_id, work, cookies_from_browser=cookies_from_browser,
                                    ffmpeg=FFMPEG, ffprobe=FFPROBE)  # fmt: skip
    finally:
        shutil.rmtree(work, ignore_errors=True)
    if got.ok and got.probed is not None:
        conn.execute(
            "UPDATE files SET size=?, mtime=?, duration_s=?, width=?, height=?, probed_size=?, "
            "probed_mtime=? WHERE path=?",
            (got.size, got.mtime, got.probed.duration, got.probed.width, got.probed.height,
             got.size, got.mtime, path),
        )  # fmt: skip
        jobs.record(conn, path, "probe", 1, got.size, got.mtime, None)
        conn.commit()
    return got.message


def _record(conn: sqlite3.Connection, path: str, error: str | None, dur: float | None = None,
            w: int | None = None, h: int | None = None) -> None:  # fmt: skip
    row = conn.execute("SELECT size, mtime FROM files WHERE path=?", (path,)).fetchone()
    size, mtime = (row[0], row[1]) if row else (None, None)
    if dur is not None:
        conn.execute(
            "UPDATE files SET duration_s=?, width=?, height=?, probed_size=?, probed_mtime=? "
            "WHERE path=?",
            (dur, w, h, size, mtime, path),
        )
    jobs.record(conn, path, "probe", 1, size, mtime, error)
    conn.commit()


def run(conn: sqlite3.Connection, cookies_from_browser: str | None = None) -> dict[str, int]:
    rows = broken(conn)
    counts = {"repaired": 0, "readable": 0, "failed": 0, "no_id": 0}
    print(f"phase: {len(rows)} files whose probe failed", flush=True)
    for n, (path, yid) in enumerate(rows, 1):
        name = Path(path).name
        at = f"  [{n}/{len(rows)}]"
        dur, w, h, why = check(path)
        if dur is not None:  # a passing failure: the file is fine
            counts["readable"] += 1
            _record(conn, path, None, dur, w, h)
            print(f"{at} ✓  {name}: reads fine now, kept", flush=True)
            continue
        if not why.startswith("broken file"):  # cannot tell now: never replace on a guess
            counts["failed"] += 1
            print(f"{at} !  {name}: {why}; left for a later run", flush=True)
            continue
        if not yid:
            counts["no_id"] += 1
            _record(conn, path, f"{why}; no YouTube id, cannot fetch it again")
            print(f"{at} -  {name}: {why}; no YouTube id, cannot fetch it again", flush=True)
            continue
        what = repair_one(conn, path, yid, cookies_from_browser)
        if what.startswith("repaired"):
            counts["repaired"] += 1
        else:
            counts["failed"] += 1
            _record(conn, path, f"{why}; {what}")
        print(f"{at} {'✓' if what.startswith('repaired') else '!'}  {name}: {what}", flush=True)
    print(f"  ✓  {counts['repaired']} repaired, {counts['readable']} read fine after all, "
          f"{counts['failed']} could not be, {counts['no_id']} without a YouTube id")  # fmt: skip
    return counts
