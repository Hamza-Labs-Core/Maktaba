"""signatures: intro / end-song audio fingerprints and frame dHashes, only where they help.

Fingerprinting reads part of each video over the share. Every video means seven opens
(two audio windows, five frames), and a busy NAS can take 10 s or more per open, so
this step reads as little as it can, in two passes:

1. **Season samples.** Only seasons that have unplaced videos which could still join
   them: same length band (``LENGTH_BAND``) and an air date within
   ``DATE_SLACK_DAYS`` of the season. ``PER_SEASON`` episodes of each are fingerprinted.
2. **Candidates.** Only for seasons whose samples share a signature (the same intro or
   end song, or the same frames). A season of talks without a fixed intro has nothing
   a fingerprint could match, so its candidates are skipped.

Everything else (shorts, seasons with nothing left to place, channels without seasons)
is never read. At most ``workers`` reads run at once. Each result is an ``index_jobs``
row (stage ``signature``), saved as soon as it finishes, so a stopped run resumes.
"""

from __future__ import annotations

import json
import math
import sqlite3
import time
from collections import defaultdict
from concurrent.futures import ThreadPoolExecutor
from datetime import date

from maktaba_pipeline.audio.chromaprint import AUDIO_FP_VERSION, MATCH, decode, similarity
from maktaba_pipeline.thumbnail.dhash import FRAMES_VERSION, hamming

from . import jobs
from .scan_local import _signature

PER_SEASON = 4
LENGTH_BAND = 0.35  # |log(duration / season median)|: about ±40 %
DATE_SLACK_DAYS = 180
SIG_VERSION = AUDIO_FP_VERSION * 100 + FRAMES_VERSION


def _spread(items: list[str], n: int) -> list[str]:
    if len(items) <= n:
        return list(items)
    return [items[int((i + 0.5) * len(items) / n)] for i in range(n)]


def plan(
    conn: sqlite3.Connection, folders: list[str] | None = None
) -> tuple[dict[str, list[str]], dict[str, list[str]]]:
    """(season id → sample paths, season id → candidate paths), for seasons with candidates."""
    seasons: dict[str, list[tuple[str, float | None, int, int]]] = defaultdict(list)
    for sid, meta_s in conn.execute("SELECT id, metadata FROM series"):
        m = json.loads(meta_s)
        folder = m.get("folder", "")
        if m.get("kind") == "clips" or (folders and not any(f in folder for f in folders)):
            continue
        if not (m.get("first_air") and m.get("last_air")):
            continue
        first = date.fromisoformat(m["first_air"]).toordinal()
        last = date.fromisoformat(m["last_air"]).toordinal()
        seasons[folder].append((sid, m.get("median_duration_s"), first, last))
    placed = {p for (p,) in conn.execute("SELECT path FROM series_episodes")}
    placed |= {p for (p,) in conn.execute("SELECT path FROM shorts")}
    cands: dict[str, list[str]] = defaultdict(list)
    for folder, ss in seasons.items():
        for path, dur, up in conn.execute(
            """SELECT f.path, f.duration_s, y.upload_date FROM files f
               JOIN yt_videos y ON y.youtube_id = f.youtube_id
               WHERE f.folder = ? AND f.duration_s >= 180 AND y.upload_date IS NOT NULL""",
            (folder,),
        ):
            if path in placed:
                continue
            d = date.fromisoformat(up).toordinal()
            for sid, med, first, last in ss:
                if med and abs(math.log(dur / med)) > LENGTH_BAND:
                    continue
                if first - DATE_SLACK_DAYS <= d <= last + DATE_SLACK_DAYS:
                    cands[sid].append(path)  # a video may fit several seasons
    samples: dict[str, list[str]] = {}
    for sid in cands:
        eps = [p for (p,) in conn.execute(
            "SELECT path FROM series_episodes WHERE series_id=? ORDER BY episode", (sid,))]  # fmt: skip
        samples[sid] = _spread(eps, PER_SEASON)
    return samples, dict(cands)


def _hashes(s: str | None) -> list[int]:
    return [int(h, 16) for h in (s or "").split(",") if h]


def _same(a: tuple, b: tuple) -> bool:
    """Do two fingerprinted videos share an intro, end song or frames?"""
    ah, at, bh, bt = decode(a[0]), decode(a[1]), decode(b[0]), decode(b[1])
    for x, y in ((ah, bh), (at, bt), (ah, bt), (at, bh)):
        if x and y and similarity(x, y, step=4) >= MATCH:
            return True
    af, bf = _hashes(a[2]), _hashes(b[2])
    return bool(af and bf) and sum(1 for f in af if min(hamming(f, g) for g in bf) <= 6) >= 2


def has_signature(conn: sqlite3.Connection, paths: list[str]) -> bool:
    """True when at least half the pairs of these fingerprinted episodes match."""
    rows = []
    for p in paths:
        r = conn.execute(
            "SELECT audio_head, audio_tail, frame_hashes FROM files WHERE path=?", (p,)
        ).fetchone()
        if r and (r[0] or r[1] or r[2]):
            rows.append(tuple(r))
    pairs = [(i, j) for i in range(len(rows)) for j in range(i + 1, len(rows))]
    return bool(pairs) and 2 * sum(_same(rows[i], rows[j]) for i, j in pairs) >= len(pairs)


def _todo(conn: sqlite3.Connection, paths: set[str]) -> list[tuple[str, float]]:
    out = []
    for path in sorted(paths):
        row = conn.execute(
            "SELECT duration_s, size, mtime FROM files WHERE path=?", (path,)
        ).fetchone()
        if row and row[0] and jobs.needed(conn, path, "signature", SIG_VERSION, row[1], row[2]):
            out.append((path, float(row[0])))
    return out


def _fingerprint(
    conn: sqlite3.Connection, todo: list[tuple[str, float]], workers: int, label: str
) -> None:
    print(f"{label}: fingerprinting {len(todo)} videos ({workers} reads at a time)", flush=True)
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
            if n % 25 == 0 or n == len(todo):
                rate = n / max(1e-6, time.monotonic() - t0) * 60
                print(f"  [{n}/{len(todo)}] {rate:.0f} per minute", flush=True)


def run(conn: sqlite3.Connection, folders: list[str] | None = None, workers: int = 4) -> int:
    """Fingerprint season samples, then the candidates of seasons that have a signature.
    Returns how many candidates carry a fingerprint (0: detecting again is pointless)."""
    jobs.ensure(conn)
    samples, cands = plan(conn, folders)
    print(f"{len(cands)} seasons have unplaced videos that could join them "
          f"({len({p for ps in cands.values() for p in ps})} videos)", flush=True)  # fmt: skip
    sample_paths = {p for ps in samples.values() for p in ps}
    _fingerprint(conn, _todo(conn, sample_paths), workers, "season samples")
    keep = [sid for sid, ps in samples.items() if has_signature(conn, ps)]
    print(f"{len(keep)} of {len(samples)} seasons share an intro, end song or frames", flush=True)
    wanted = {p for sid in keep for p in cands[sid]}
    _fingerprint(conn, _todo(conn, wanted), workers, "candidates")
    return sum(
        1
        for p in wanted
        if conn.execute(
            "SELECT 1 FROM files WHERE path=? AND (audio_head IS NOT NULL OR audio_tail IS NOT NULL"
            " OR frame_hashes != '')",
            (p,),
        ).fetchone()
    )
