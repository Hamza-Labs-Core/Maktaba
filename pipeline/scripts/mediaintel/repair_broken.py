"""``repair-broken``: download again the files ffprobe cannot read.

A file whose probe failed (an unfinished download: "moov atom not found") and that came
from YouTube is fetched again into its folder beside it, checked with ffprobe, and only
then put in its place; the broken file is kept as ``<name>.broken``. A video no longer on
YouTube, or a file without a YouTube id, is reported and left as it is.
"""

from __future__ import annotations

import os
import sqlite3
import subprocess
import sys
from pathlib import Path

from . import jobs
from .scan_local import _probe, probe_failure

#: the library's own format: at most 1080p, merged into the file's container
FORMAT = "bv*[height<=1080]+ba/b[height<=1080]/b"


def broken(conn: sqlite3.Connection) -> list[tuple[str, str | None]]:
    """(path, YouTube id) of every file whose probe failed."""
    return [
        (r[0], r[1])
        for r in conn.execute(
            """SELECT j.path, f.youtube_id FROM index_jobs j LEFT JOIN files f ON f.path = j.path
               WHERE j.stage = 'probe' AND j.state = 'failed' ORDER BY j.path"""
        )
    ]


def download_args(youtube_id: str, target: Path, cookies_from_browser: str | None) -> list[str]:
    """yt-dlp argv writing the video beside ``target`` as ``<stem>.repair.<ext>``."""
    ext = target.suffix.lstrip(".").lower() or "mp4"
    args = [
        sys.executable, "-m", "yt_dlp", "--no-playlist", "--no-progress", "-f", FORMAT,
        "--merge-output-format", ext, "-o", str(target.with_name(f"{target.stem}.repair.%(ext)s")),
    ]  # fmt: skip
    if cookies_from_browser:
        args += ["--cookies-from-browser", cookies_from_browser]
    return [*args, f"https://www.youtube.com/watch?v={youtube_id}"]


def repair_one(conn: sqlite3.Connection, path: str, youtube_id: str,
               cookies_from_browser: str | None = None) -> str:  # fmt: skip
    """Fetch, check and swap one file; returns what happened (one line)."""
    target = Path(path)
    out = subprocess.run(download_args(youtube_id, target, cookies_from_browser),
                         capture_output=True, timeout=3600, check=False)  # fmt: skip
    fresh = target.with_name(f"{target.stem}.repair{target.suffix}")
    if out.returncode != 0 or not fresh.is_file():
        err = out.stderr.decode("utf-8", "replace").strip().splitlines()
        why = next((ln for ln in reversed(err) if "ERROR" in ln), err[-1] if err else "")
        for leftover in target.parent.glob(f"{target.stem}.repair.*"):
            leftover.unlink(missing_ok=True)
        return f"not downloaded: {why[:160]}"
    dur, w, h = _probe(str(fresh))
    if dur is None:
        fresh.unlink(missing_ok=True)
        return "downloaded, but the new copy is broken too: left as it was"
    backup = target.with_name(target.name + ".broken")
    if target.exists():
        os.replace(target, backup)
    os.replace(fresh, target)
    st = target.stat()
    conn.execute(
        "UPDATE files SET size=?, mtime=?, duration_s=?, width=?, height=?, probed_size=?, "
        "probed_mtime=? WHERE path=?",
        (st.st_size, st.st_mtime, dur, w, h, st.st_size, st.st_mtime, path),
    )
    jobs.record(conn, path, "probe", 1, st.st_size, st.st_mtime, None)
    conn.commit()
    return f"repaired: {dur / 60:.1f} min, {w}x{h} (old file kept as {backup.name})"


def run(conn: sqlite3.Connection, cookies_from_browser: str | None = None) -> dict[str, int]:
    rows = broken(conn)
    counts = {"repaired": 0, "failed": 0, "no_id": 0}
    print(f"phase: {len(rows)} broken files", flush=True)
    for n, (path, yid) in enumerate(rows, 1):
        name = Path(path).name
        if not yid:
            counts["no_id"] += 1
            print(f"  [{n}/{len(rows)}] -  {name}: no YouTube id, cannot fetch it again",
                  flush=True)  # fmt: skip
            continue
        what = repair_one(conn, path, yid, cookies_from_browser)
        counts["repaired" if what.startswith("repaired") else "failed"] += 1
        if not what.startswith("repaired"):
            row = conn.execute("SELECT size, mtime FROM files WHERE path=?", (path,)).fetchone()
            jobs.record(conn, path, "probe", 1, row[0] if row else None, row[1] if row else None,
                        probe_failure("moov atom not found") + f"; {what}")  # fmt: skip
            conn.commit()
        print(f"  [{n}/{len(rows)}] {'✓' if what.startswith('repaired') else '!'}  {name}: {what}",
              flush=True)  # fmt: skip
    print(f"  ✓  {counts['repaired']} repaired, {counts['failed']} could not be, "
          f"{counts['no_id']} without a YouTube id")  # fmt: skip
    return counts
