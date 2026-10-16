"""scan-visual: production style (talk / produced / cinematic / static) per season.

Samples ``per_season`` episodes of every detected season (spread over the
season's episode order), reads a few short windows of each over the share
(see :mod:`maktaba_pipeline.classify.production_style`), caches the
per-file metrics on (size, mtime) and writes one style row per season.

Re-running only samples new/changed files; the season style is recomputed
from the cache every time (cheap) so threshold changes apply without
re-reading video.
"""

from __future__ import annotations

import json
import os
import sqlite3
import subprocess
import tempfile
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from pathlib import Path

from maktaba_pipeline.classify.production_style import (
    STYLE_VERSION,
    StreamInfo,
    StyleMetrics,
    WindowSample,
    classify_style,
    compute_metrics,
    ffmpeg_sample_args,
    metrics_from_dict,
    parse_frame_stats,
    parse_stream_info,
    sample_windows,
    split_thumbs,
)

from . import jobs  # noqa: E402
from .bins import FFMPEG  # noqa: E402

SCHEMA = """
-- Per-file visual metrics (production_style.StyleMetrics as JSON), cached on size/mtime.
CREATE TABLE IF NOT EXISTS visual_metrics (
    path          TEXT PRIMARY KEY,
    probed_size   INTEGER,
    probed_mtime  REAL,
    metrics       TEXT NOT NULL,          -- JSON StyleMetrics
    version       INTEGER NOT NULL        -- STYLE_VERSION the metrics were computed with
);

-- Production style of one detected season (series row).
CREATE TABLE IF NOT EXISTS series_style (
    series_id   TEXT PRIMARY KEY,
    style       TEXT NOT NULL,            -- static | talk | produced | cinematic
    tier        INTEGER NOT NULL,         -- 0 | 1 | 2 | 3
    confidence  REAL NOT NULL,            -- 0..1
    reasons     TEXT NOT NULL,            -- JSON {"reasons": [...], "scores": {...}}
    sampled     INTEGER NOT NULL,         -- episodes the style was computed from
    version     INTEGER NOT NULL
);

-- Styles set by hand: a series (by library + series name) always gets this style.
CREATE TABLE IF NOT EXISTS style_overrides (
    library     TEXT NOT NULL,
    series      TEXT NOT NULL,
    style       TEXT NOT NULL,            -- static | talk | produced | cinematic
    PRIMARY KEY (library, series)
);
"""

TIERS = {"static": 0, "talk": 1, "produced": 2, "cinematic": 3}
#: a borderline season (low confidence, or "talk" with a dark/low-key look, as an
#: AI virtual set) is re-sampled with this many episodes
RESAMPLE_EPISODES = 8


#: reads at once over the share: above about 4 a busy NAS starts failing reads over SMB
NAS_READS = 2


def set_override(conn: sqlite3.Connection, library: str, series: str, style: str | None) -> None:
    """Pin (or with ``style=None`` clear) a series' style; applied on every run."""
    conn.executescript(SCHEMA)
    if style is None:
        conn.execute("DELETE FROM style_overrides WHERE library=? AND series=?", (library, series))
    else:
        if style not in TIERS:
            raise SystemExit(f"style must be one of {', '.join(TIERS)}")
        conn.execute(
            "INSERT OR REPLACE INTO style_overrides (library, series, style) VALUES (?,?,?)",
            (library, series, style),
        )
    conn.commit()


WINDOWS = 4
WINDOW_S = 30.0


@dataclass(frozen=True, slots=True)
class _Job:
    path: str
    duration_s: float | None


_NAS = threading.BoundedSemaphore(NAS_READS)
#: local scratch for copied sample windows (SSD): decoding happens here, not over SMB
LOCAL_TMP = Path(os.environ.get("MEDIAINTEL_TMP") or tempfile.gettempdir()) / "mediaintel-style"


def _copy_window(path: str, start: float, length: float, dest: Path) -> bool:
    """Copy one window's video stream to a local file, without decoding. This is the only
    part that reads the share, so it alone holds one of the ``NAS_READS`` slots."""
    with _NAS:
        try:
            r = subprocess.run(
                [FFMPEG, "-v", "error", "-nostdin", "-y", "-ss", f"{start:.2f}",
                 "-t", f"{length:.2f}", "-i", path, "-map", "0:v:0", "-c", "copy",
                 "-f", "matroska", str(dest)],
                capture_output=True, timeout=300, check=False,
            )  # fmt: skip
        except (subprocess.TimeoutExpired, OSError):
            return False
    return r.returncode == 0 and dest.exists() and dest.stat().st_size > 0


def sample_file(
    path: str,
    duration_s: float | None,
    *,
    windows: int = WINDOWS,
    window_s: float = WINDOW_S,
    hwaccel: str | None = None,
) -> StyleMetrics | None:
    """Sample the file's windows; None when nothing decoded.

    Two stages so the share and the CPU are each used well: each window is first
    copied (stream copy, no decoding) to local disk under the NAS read limit, then
    decoded and measured from there, many files at once. A window that cannot be
    copied is read over the share directly, still under the limit. ``hwaccel`` is
    optional: on these mostly low-bitrate files the CPU decodes faster than starting
    a GPU decoder per window (measured: CPU 2-17 s, d3d11va 9-80 s per file)."""
    samples: list[WindowSample] = []
    info: StreamInfo | None = None
    LOCAL_TMP.mkdir(parents=True, exist_ok=True)
    for start, length in sample_windows(duration_s, count=windows, length_s=window_s):
        fd, tmp_name = tempfile.mkstemp(suffix=".mkv", dir=LOCAL_TMP)
        os.close(fd)
        tmp = Path(tmp_name)
        try:
            if _copy_window(path, start, length, tmp):
                args = ffmpeg_sample_args(str(tmp), 0.0, length, binary=FFMPEG, hwaccel=hwaccel)
                remote = False
            else:
                args = ffmpeg_sample_args(path, start, length, binary=FFMPEG, hwaccel=hwaccel)
                remote = True
            try:
                if remote:
                    with _NAS:
                        out = subprocess.run(args, capture_output=True, timeout=300, check=False)
                else:
                    out = subprocess.run(args, capture_output=True, timeout=300, check=False)
            except (subprocess.TimeoutExpired, OSError):
                continue
        finally:
            tmp.unlink(missing_ok=True)
        log = out.stderr.decode("utf-8", errors="replace")
        if info is None or info.width is None:
            info = parse_stream_info(log)
        frames = parse_frame_stats(log)
        if frames:
            samples.append(WindowSample(length, frames, split_thumbs(out.stdout)))
    if not samples:
        return None
    return compute_metrics(samples, info)


def _spread(items: list[str], n: int) -> list[str]:
    """``n`` items from the centres of ``n`` equal slices of the list.

    Avoids the first and last episode (pilots, finales and compilations are the
    least typical): 30 episodes, n=3 → episodes 6, 16 and 26.
    """
    if len(items) <= n:
        return list(items)
    return [items[int((i + 0.5) * len(items) / n)] for i in range(max(1, n))]


def _cached(conn: sqlite3.Connection, path: str, size: int, mtime: float) -> StyleMetrics | None:
    row = conn.execute(
        "SELECT probed_size, probed_mtime, metrics, version FROM visual_metrics WHERE path=?",
        (path,),
    ).fetchone()
    if not row or row[0] != size or row[1] != mtime or row[3] != STYLE_VERSION:
        return None
    return metrics_from_dict(json.loads(row[2]))


def run(
    conn: sqlite3.Connection,
    only_folder: str | None = None,
    per_season: int = 3,
    workers: int = 4,
) -> dict[str, int]:
    conn.executescript(SCHEMA)
    jobs.ensure(conn)
    series = conn.execute(
        "SELECT id, library, name, year, metadata FROM series ORDER BY year, name"
    ).fetchall()
    overrides = {(lib, ser): st for lib, ser, st in conn.execute("SELECT * FROM style_overrides")}
    picked: dict[str, list[str]] = {}
    all_paths: dict[str, list[str]] = {}
    labels: dict[str, str] = {}
    pinned: dict[str, str] = {}
    for sid, library, name, year, meta_s in series:
        meta = json.loads(meta_s or "{}")
        # like the other subcommands: --folder matches folders whose path contains it
        if only_folder and os.path.normcase(only_folder) not in os.path.normcase(
            str(meta.get("folder", ""))
        ):
            continue
        paths = [
            p
            for (p,) in conn.execute(
                "SELECT path FROM series_episodes WHERE series_id=? "
                "ORDER BY season, episode IS NULL, episode, path",
                (sid,),
            )
        ]
        labels[sid] = f"{year or '?'} {name} S{meta.get('season', 1)}"
        if (library, name) in overrides:
            pinned[sid] = overrides[(library, name)]
            continue
        picked[sid] = _spread(paths, per_season)
        all_paths[sid] = paths

    have: dict[str, StyleMetrics] = {}
    stats = {"seasons": len(picked) + len(pinned), "cached": 0, "sampled": 0, "failed": 0}
    _sample(conn, picked, have, stats, workers)
    results = {
        sid: classify_style([have[p] for p in ps if p in have]) for sid, ps in picked.items()
    }
    # second look at borderline seasons with more episodes
    again = {
        sid: _spread(all_paths[sid], RESAMPLE_EPISODES)
        for sid, res in results.items()
        if len(all_paths[sid]) > len(picked[sid])
        and (res.confidence < 0.5 or (res.style == "talk" and res.scores.get("lowkey", 0) >= 0.35))
    }
    if again:
        print(f"re-sampling {len(again)} borderline seasons with {RESAMPLE_EPISODES} episodes")
        _sample(conn, again, have, stats, workers)
        picked.update(again)
        for sid, ps in again.items():
            results[sid] = classify_style([have[p] for p in ps if p in have])

    for sid, style in pinned.items():
        _store(conn, sid, style, TIERS[style], 1.0, {"reasons": ["set by user"], "scores": {}}, 0)
        print(f"{labels[sid]}: {style} (set by user)")
    for sid, res in results.items():
        n = sum(1 for p in picked[sid] if p in have)
        _store(conn, sid, res.style, res.tier, res.confidence,
               {"reasons": res.reasons, "scores": res.scores}, n)  # fmt: skip
        print(
            f"{labels[sid]}: {res.style} (tier {res.tier}, {res.confidence:.2f}) "
            f"[{n} ep] {res.scores} — {'; '.join(res.reasons)}"
        )
    conn.commit()
    return stats


def _store(
    conn: sqlite3.Connection, sid: str, style: str, tier: int, conf: float,
    reasons: dict[str, object], sampled: int,
) -> None:  # fmt: skip
    conn.execute(
        """INSERT INTO series_style (series_id, style, tier, confidence, reasons, sampled, version)
           VALUES (?,?,?,?,?,?,?)
           ON CONFLICT (series_id) DO UPDATE SET style=excluded.style, tier=excluded.tier,
             confidence=excluded.confidence, reasons=excluded.reasons,
             sampled=excluded.sampled, version=excluded.version""",
        (sid, style, tier, conf, json.dumps(reasons, ensure_ascii=False), sampled, STYLE_VERSION),
    )


def _sample(
    conn: sqlite3.Connection,
    picked: dict[str, list[str]],
    have: dict[str, StyleMetrics],
    stats: dict[str, int],
    workers: int,
) -> None:
    """Fill ``have`` with metrics for every picked file (cache first, then ffmpeg)."""
    todo: list[_Job] = []
    stamp: dict[str, tuple[int, float]] = {}
    for path in sorted({p for ps in picked.values() for p in ps} - set(have)):
        frow = conn.execute(
            "SELECT size, mtime, duration_s FROM files WHERE path=?", (path,)
        ).fetchone()
        if not frow:
            continue
        size, mtime, dur = int(frow[0]), float(frow[1]), frow[2]
        stamp[path] = (size, mtime)
        cached = _cached(conn, path, size, mtime)
        if cached is not None:
            have[path] = cached
            stats["cached"] += 1
        elif jobs.needed(conn, path, "visual_style", STYLE_VERSION, size, mtime):
            todo.append(_Job(path, dur))

    workers = max(1, min(workers, (os.cpu_count() or 4) - 2))  # decoding is local CPU work
    print(
        f"sampling {len(todo)} files ({NAS_READS} reads at a time, {workers} decoding)", flush=True
    )
    started = time.monotonic()
    with ThreadPoolExecutor(max(1, workers)) as pool:
        hw = os.environ.get("MEDIAINTEL_HWACCEL") or None  # optional; CPU is the default
        results = pool.map(lambda j: sample_file(j.path, j.duration_s, hwaccel=hw), todo)
        for n, (job, m) in enumerate(zip(todo, results, strict=True), 1):
            if n % 25 == 0 or n == len(todo):
                rate = n / max(1e-6, time.monotonic() - started) * 60
                print(f"  [{n}/{len(todo)}] {rate:.0f} per minute", flush=True)
            size, mtime = stamp[job.path]
            if m is None:
                stats["failed"] += 1
                print(f"  ! no frames: {job.path}", flush=True)
                jobs.record(conn, job.path, "visual_style", STYLE_VERSION, size, mtime, "no frames")
                conn.commit()
                continue
            jobs.record(conn, job.path, "visual_style", STYLE_VERSION, size, mtime)
            conn.execute(
                """INSERT INTO visual_metrics (path, probed_size, probed_mtime, metrics, version)
                   VALUES (?,?,?,?,?)
                   ON CONFLICT (path) DO UPDATE SET probed_size=excluded.probed_size,
                     probed_mtime=excluded.probed_mtime, metrics=excluded.metrics,
                     version=excluded.version""",
                (job.path, size, mtime, json.dumps(m.to_dict()), STYLE_VERSION),
            )
            conn.commit()
            have[job.path] = m
            stats["sampled"] += 1
    elapsed = time.monotonic() - started
    if stats["sampled"]:
        print(
            f"sampled {stats['sampled']} files in {elapsed:.0f}s "
            f"({elapsed / stats['sampled']:.1f}s per file wall, {workers} workers)"
        )
