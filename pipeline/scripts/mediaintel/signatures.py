"""signatures: intro / end-song audio fingerprints and frame dHashes, only where they help.

Fingerprinting reads part of each video over the share, and a NAS slows down sharply
under many scattered reads at once. So this step does not fingerprint everything.
It runs after a first detection and covers two groups of videos:

- a few episodes of every season (``PER_SEASON``): what the season sounds and looks like;
- unplaced episode-length videos in a library that has seasons, whose length fits one
  of those seasons and whose air date is not years away from it. These are the only
  videos a fingerprint could still attach.

Everything else (shorts, lecture channels without seasons, videos already placed by
title and playlist) is skipped. At most ``workers`` reads run at once, across all
libraries. Each result is an ``index_jobs`` row (stage ``signature``), saved as soon as
it finishes.
"""

from __future__ import annotations

import json
import math
import sqlite3
import time
from concurrent.futures import ThreadPoolExecutor
from datetime import date

from maktaba_pipeline.audio.chromaprint import AUDIO_FP_VERSION
from maktaba_pipeline.thumbnail.dhash import FRAMES_VERSION

from . import jobs
from .scan_local import _signature

PER_SEASON = 6
SIG_VERSION = AUDIO_FP_VERSION * 100 + FRAMES_VERSION


def _spread(items: list[str], n: int) -> list[str]:
    if len(items) <= n:
        return list(items)
    return [items[int((i + 0.5) * len(items) / n)] for i in range(n)]


def targets(conn: sqlite3.Connection, folders: list[str] | None = None) -> list[tuple[str, float]]:
    """(path, duration) of the videos worth fingerprinting, not yet done."""
    seasons: dict[str, list[tuple[float | None, date | None, date | None]]] = {}
    picked: set[str] = set()
    for sid, meta_s in conn.execute("SELECT id, metadata FROM series"):
        m = json.loads(meta_s)
        folder = m.get("folder", "")
        if m.get("kind") == "clips" or (folders and not any(f in folder for f in folders)):
            continue
        first = date.fromisoformat(m["first_air"]) if m.get("first_air") else None
        last = date.fromisoformat(m["last_air"]) if m.get("last_air") else None
        seasons.setdefault(folder, []).append((m.get("median_duration_s"), first, last))
        eps = [p for (p,) in conn.execute(
            "SELECT path FROM series_episodes WHERE series_id=? ORDER BY episode", (sid,))]  # fmt: skip
        picked.update(_spread(eps, PER_SEASON))
    placed = {p for (p,) in conn.execute("SELECT path FROM series_episodes")}
    placed |= {p for (p,) in conn.execute("SELECT path FROM shorts")}
    for folder, ss in seasons.items():
        for path, dur, up in conn.execute(
            """SELECT f.path, f.duration_s, y.upload_date FROM files f
               LEFT JOIN yt_videos y ON y.youtube_id = f.youtube_id
               WHERE f.folder = ? AND f.duration_s >= 180""",
            (folder,),
        ):
            if path in placed:
                continue
            d = date.fromisoformat(up) if up else None
            for med, first, last in ss:
                if med and abs(math.log(dur / med)) > 0.7:
                    continue  # another length band: no season here it could join
                if d and first and last and not (first.replace(year=first.year - 1) <= d
                                                 and d.toordinal() <= last.toordinal() + 730):  # fmt: skip
                    continue
                picked.add(path)
                break
    out: list[tuple[str, float]] = []
    for path in sorted(picked):
        row = conn.execute(
            "SELECT duration_s, size, mtime FROM files WHERE path=?", (path,)
        ).fetchone()
        if row and row[0] and jobs.needed(conn, path, "signature", SIG_VERSION, row[1], row[2]):
            out.append((path, float(row[0])))
    return out


def run(conn: sqlite3.Connection, folders: list[str] | None = None, workers: int = 4) -> int:
    jobs.ensure(conn)
    todo = targets(conn, folders)
    print(f"fingerprinting {len(todo)} videos ({workers} reads at a time)", flush=True)
    t0, n = time.monotonic(), 0
    with ThreadPoolExecutor(max(1, workers)) as pool:
        for (path, _), (head, tail, frames) in zip(
            todo, pool.map(lambda x: _signature(*x), todo), strict=True
        ):
            conn.execute(
                """UPDATE files SET audio_head=?, audio_tail=?, audio_version=?,
                     frame_hashes=?, frames_version=? WHERE path=?""",
                (head, tail, AUDIO_FP_VERSION, frames, FRAMES_VERSION, path),
            )
            size, mtime = conn.execute(
                "SELECT size, mtime FROM files WHERE path=?", (path,)
            ).fetchone()
            jobs.record(conn, path, "signature", SIG_VERSION, size, mtime,
                        None if head or tail or frames else "no audio or frames decoded")  # fmt: skip
            conn.commit()
            n += 1
            if n % 25 == 0:
                rate = n / max(1e-6, time.monotonic() - t0) * 60
                print(f"  [{n}/{len(todo)}] {rate:.0f} per minute", flush=True)
    return n
