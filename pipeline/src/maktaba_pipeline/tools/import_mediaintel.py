# ruff: noqa: E501  (long SQL strings read better unwrapped)
"""Import the mediaintel prototype's cache into Maktaba (plan 06-11 section 10.4).

    python -m maktaba_pipeline.tools.import_mediaintel --cache cache.db --library <uuid|name> \\
        [--library ...] [--path-map CACHE_PREFIX=MAKTABA_PREFIX] [--settings settings.json] \\
        [--thumbs thumbs/] [--database-url URL] [--apply]

A **dry run by default**: everything is written inside a transaction that is rolled back, and the
per-table row counts it would change are printed. ``--apply`` commits.

Precondition: Maktaba has scanned the libraries (``videos`` rows exist; cheap, the scanner hashes only
head and tail of each file). Cache files are matched to ``videos`` by normalised path (optionally
rewritten with ``--path-map``) plus equal size and an mtime within 2 s; unmatched files are reported.

What is copied: fingerprints / thumbnail hashes / style metrics (``video_signals``), YouTube rows,
the links (``media_metadata_enrichment``), series / seasons / episodes (through the same
``detect.apply_changes`` the live stage uses, so ids and membership hashes are identical), tags,
styles and user style overrides, and the Plex collections the tool created (``external_links``).
The user's rule changes (``settings.json``, beside the cache unless ``--settings`` says
otherwise) become the ``rules.<section>`` rows of ``app_settings``, each value checked with
:func:`maktaba_pipeline.rules.config` (unusable ones are reported, never imported; a value
already set in Maktaba is kept). The small thumbnails (``thumbs/``, named
``sha1(<thumb source>)[:16].jpg``, as a folder or the ``thumbs.tar`` it ships as) are copied
into Maktaba's thumbnail cache, so
``thumb_small`` never reads them from the NAS again (copied on ``--apply`` only).

For every ``(subject, stage)`` that was done in the cache one **synthetic ``processing_jobs`` row**
(``state='done'``, ``outcome='imported'``) is written with ``input_hash`` computed by the *same*
``input_fingerprint`` function the live stage uses and ``output_hash`` the hash of the imported
output. Every live job for those subjects therefore ends ``skipped_unchanged``: nothing is
recomputed and the NAS is not touched. Cache rows that failed become ``failed`` rows with a
normalised ``error_cause``. ``probe`` and the extension stages are not imported: Maktaba probes its
own files, and an extension reconciles with its server on first run (the link rows it needs are
imported). A final ``detect`` per library is queued with reason ``import``.
"""

from __future__ import annotations

import argparse
import asyncio
import contextlib
import hashlib
import json
import os
import posixpath
import shutil
import sqlite3
import sys
import tarfile
import unicodedata
import uuid
from collections import defaultdict
from collections.abc import Iterator
from dataclasses import dataclass, field
from datetime import UTC, date, datetime
from pathlib import Path
from typing import Any
from urllib.parse import quote
from uuid import UUID

from .. import rules
from ..audio.chromaprint import AUDIO_FP_VERSION
from ..classify.production_style import STYLE_VERSION, metrics_from_dict
from ..classify.series_detector import DETECTOR_VERSION
from ..engine.causes import classify
from ..engine.enqueue import enqueue_job
from ..engine.graph import SIG_VERSION, Subject
from ..engine.hashing import canonical_hash
from ..engine.ids import season_uuid, series_uuid
from ..engine.sql import dumps, is_pg, loads, now, parse_ts, to_date, to_ts, uid
from ..engine.stages import detect as detect_stage
from ..engine.stages import fingerprint as fingerprint_stage
from ..engine.stages import link as link_stage
from ..engine.stages import rate_style as rate_style_stage
from ..engine.stages import style_sample as style_sample_stage
from ..engine.stages import tag as tag_stage
from ..engine.stages import thumb_hash as thumb_stage
from ..engine.stages import thumb_small as thumb_small_stage
from ..engine.stages import youtube_sync as yt_stage
from ..engine.stages.content_hash import PENDING, is_pending
from ..engine.stages.thumb_small import THUMB_SMALL_VERSION, small_path, staged_path
from ..thumbnail.dhash import DHASH_VERSION, FRAMES_VERSION

__all__ = ["ImportReport", "main", "run_import"]

MTIME_SLOP_S = 2.0
LINK_CONFIDENCE = {
    "info_json": 1.0,
    "exact_title": 1.0,
    "folded_title": 0.9,
    "title_duration": 0.8,
    "fuzzy_title_duration": 0.6,
}
TABLES = (
    "video_signals", "youtube_channels", "youtube_videos", "youtube_playlists",
    "youtube_playlist_items", "media_metadata_enrichment", "series", "series_seasons",
    "series_episodes", "season_tags", "video_topic_tags", "season_style", "external_links",
    "processing_jobs", "app_settings", "libraries", "library_roots", "videos",
)  # fmt: skip


class _Rollback(Exception):
    """Raised to roll the dry-run transaction back."""


@dataclass
class ImportReport:
    applied: bool
    files: int = 0
    matched: int = 0
    unmatched: list[str] = field(default_factory=list)
    tables: dict[str, tuple[int, int]] = field(default_factory=dict)  # name -> (before, after)
    jobs_imported: int = 0
    jobs_failed: int = 0
    jobs_skipped_existing: int = 0
    detect_queued: int = 0
    #: section -> settings imported; and every setting skipped, with why
    settings_imported: dict[str, int] = field(default_factory=dict)
    settings_skipped: list[str] = field(default_factory=list)
    thumbs_copied: int = 0
    thumbs_present: int = 0
    thumbs_missing: int = 0
    libraries_created: int = 0
    videos_created: int = 0
    hash_jobs_queued: int = 0
    #: synthetic job rows not written yet, and the newest imported row per subject + stage
    _job_rows: list[tuple[Any, ...]] = field(default_factory=list, repr=False)
    _imported: dict[tuple[str, str, str], tuple[Any, Any]] | None = field(default=None, repr=False)

    def render(self) -> str:
        out = [
            f"{'APPLIED' if self.applied else 'DRY RUN (nothing written; pass --apply)'}",
            f"cache files: {self.files}, matched to videos: {self.matched}, "
            f"unmatched: {len(self.unmatched)}",
        ]
        for p in self.unmatched[:10]:
            out.append(f"  unmatched: {p}")
        out.append(f"{'table':28} {'before':>8} {'after':>8} {'delta':>8}")
        for name, (b, a) in self.tables.items():
            out.append(f"{name:28} {b:8d} {a:8d} {a - b:+8d}")
        out.append(
            f"synthetic jobs: {self.jobs_imported} done, {self.jobs_failed} failed, "
            f"{self.jobs_skipped_existing} already imported; detect queued for {self.detect_queued} libraries"
        )
        if self.libraries_created or self.videos_created:
            out.append(
                f"created from the cache: {self.libraries_created} libraries,"
                f" {self.videos_created} videos (file identity queued: {self.hash_jobs_queued}"
                " content_hash jobs)"
            )
        if self.settings_imported or self.settings_skipped:
            got = ", ".join(f"{k} {n}" for k, n in sorted(self.settings_imported.items()))
            out.append(f"rules imported: {got or 'none'}")
            out.extend(f"  rule skipped: {why}" for why in self.settings_skipped)
        if self.thumbs_copied or self.thumbs_present or self.thumbs_missing:
            verb = "copied" if self.applied else "to copy"
            out.append(
                f"thumbnails: {self.thumbs_copied} {verb}, {self.thumbs_present} already cached,"
                f" {self.thumbs_missing} not in thumbs/"
            )
        return "\n".join(out)


# --------------------------------------------------------------------------- cache access


def _open_cache(path: Path) -> sqlite3.Connection:
    # immutable: a copied snapshot of a WAL database has no -shm beside it, and a plain
    # read-only open would need to create one; the copy never changes during an import
    conn = sqlite3.connect(f"file:{quote(path.as_posix())}?immutable=1", uri=True)
    conn.row_factory = sqlite3.Row
    return conn


def _has(conn: sqlite3.Connection, table: str) -> bool:
    return (
        conn.execute("SELECT 1 FROM sqlite_master WHERE name = ?", (table,)).fetchone() is not None
    )


def _norm(p: str, maps: list[tuple[str, str]]) -> str:
    s = p.replace("\\", "/")
    for src, dst in maps:
        if s.lower().startswith(src.lower()):
            s = dst + s[len(src) :]
            break
    return _key(s)


def _key(path: str) -> str:
    """How two paths are compared: ``/`` separators, composed Unicode (the share may hold a
    name decomposed, and content_hash then stores that form), case folded."""
    return unicodedata.normalize("NFC", path.replace("\\", "/")).lower()


def parse_path_maps(values: list[str]) -> list[tuple[str, str]]:
    out = []
    for v in values:
        a, _, b = v.partition("=")
        if not a or not b:
            raise SystemExit(f"--path-map expects CACHE_PREFIX=MAKTABA_PREFIX, got {v!r}")
        out.append((a.replace("\\", "/"), b.replace("\\", "/")))
    return sorted(out, key=lambda x: -len(x[0]))


def _epoch(v: Any) -> float | None:
    dt = parse_ts(v)
    return None if dt is None else dt.timestamp()


async def _resolve_libraries(db: Any, names: list[str]) -> list[tuple[UUID, str]]:
    out = []
    for n in names:
        row = None
        with contextlib.suppress(ValueError):
            row = await db.fetchrow("SELECT id, name FROM libraries WHERE id = $1", str(UUID(n)))
        if row is None:
            row = await db.fetchrow("SELECT id, name FROM libraries WHERE name = $1", n)
        if row is None:
            raise SystemExit(f"library {n!r} not found")
        out.append((UUID(str(row["id"])), str(row["name"])))
    return out


# --------------------------------------------------------------------------- batching

_BATCH = 2000


async def _analyze(db: Any) -> None:
    """Statistics for the tables this transaction just filled. Without them Postgres plans
    for empty tables, and the per-library reads that follow (detect's writer, the
    youtube_sync snapshot) take tens of seconds each instead of milliseconds."""
    if is_pg(db):
        for t in TABLES:
            await db.execute(f"ANALYZE {t}")


async def _many(db: Any, sql: str, rows: list[tuple[Any, ...]]) -> None:
    """``rows`` through one statement, ``_BATCH`` at a time (a round trip per batch, not
    per row: the cache holds ~150k files and twice as many YouTube rows)."""
    for i in range(0, len(rows), _BATCH):
        await db.executemany(sql, rows[i : i + _BATCH])


# --------------------------------------------------------------------------- libraries and videos


def _map(p: str, maps: list[tuple[str, str]]) -> str:
    """``p`` as Maktaba stores it: the cache prefix rewritten, the case kept."""
    s = p.replace("\\", "/")
    for src, dst in maps:
        if s.lower().startswith(src.lower()):
            return dst + s[len(src) :]
    return s


def _library_of(f: Any) -> str:
    """The Maktaba library a cache file goes to: its Plex library, else its folder."""
    # sqlite3.Row: ``in`` looks at the values, the column names are ``keys()``
    plex = f["plex_library"] if "plex_library" in f.keys() else None  # noqa: SIM118
    return str(plex or posixpath.basename(str(f["folder"]).rstrip("/")) or f["folder"])


def stand_in_hash(library: str, path: str) -> str:
    """A unique, well-formed ``content_hash`` for a video whose file has not been read yet
    (``metadata.hash_pending``); ``content_hash`` replaces it with the real one."""
    return hashlib.sha256(f"pending|{library}|{path}".encode()).hexdigest()


async def _create_libraries(
    db: Any,
    cache: sqlite3.Connection,
    only: list[str],
    maps: list[tuple[str, str]],
    report: ImportReport,
) -> list[tuple[UUID, str]]:
    """The cache's libraries and videos as Maktaba rows (no read of the share). A library
    that exists by name is reused; a video that exists by path is left as it is."""
    from ..library_mgmt.roots import canonicalise

    groups: dict[str, list[Any]] = defaultdict(list)
    for f in cache.execute("SELECT * FROM files ORDER BY path"):
        groups[_library_of(f)].append(f)
    wanted = set(only)
    out: list[tuple[UUID, str]] = []
    for name in sorted(groups):
        if wanted and name not in wanted:
            continue
        files = groups[name]
        roots = sorted({_map(str(f["folder"]), maps).rstrip("/") for f in files})
        row = await db.fetchrow("SELECT id FROM libraries WHERE name = $1", name)
        if row is not None:
            lib = UUID(str(row["id"]))
        else:
            lib = uuid.uuid4()
            await db.execute(
                "INSERT INTO libraries (id, name, roots) VALUES ($1, $2, $3)",
                uid(lib), name, roots if is_pg(db) else dumps(roots),
            )  # fmt: skip
            for r in roots:
                await db.execute(
                    "INSERT INTO library_roots (library_id, path, path_canonical) VALUES ($1, $2, $3)",
                    uid(lib), r, canonicalise(r),
                )  # fmt: skip
            report.libraries_created += 1
        out.append((lib, name))
        have = {
            _key(str(r["path"]))
            for r in await db.fetch("SELECT path FROM videos WHERE library_id = $1", uid(lib))
        }
        rows: list[tuple[Any, ...]] = []
        for f in files:
            path = _map(str(f["path"]), maps)
            if _key(path) in have:
                continue
            have.add(_key(path))
            rows.append((
                uid(uuid.uuid4()), uid(lib), stand_in_hash(name, path), path, posixpath.basename(path),
                int(f["size"]), to_ts(db, datetime.fromtimestamp(float(f["mtime"]), UTC)),
                None if f["duration_s"] is None else float(f["duration_s"]),
                dumps({PENDING: True, "source": "mediaintel"}),
            ))  # fmt: skip
        await _many(
            db,
            "INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime,"
            " duration_sec, metadata) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)",
            rows,
        )
        report.videos_created += len(rows)
    if wanted - {n for _, n in out}:
        raise SystemExit(f"not in the cache: {', '.join(sorted(wanted - {n for _, n in out}))}")
    return out


async def _queue_content_hash(db: Any, libs: list[tuple[UUID, str]]) -> int:
    """One ``content_hash`` job per video still on a stand-in hash (one statement, not one
    enqueue per video: there are ~150k). A live job for a video is left as it is."""
    pending = (
        "v.metadata ->> 'hash_pending' = 'true'"
        if is_pg(db)
        else "json_extract(v.metadata, '$.hash_pending') = 1"
    )
    n = 0
    for lib, _ in libs:
        before = int(
            await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'content_hash'")
        )
        await db.execute(
            "INSERT INTO processing_jobs (subject_kind, subject_id, video_id, library_id, stage,"
            " state, priority, max_attempts, reasons)"
            " SELECT 'video', v.id, v.id, v.library_id, 'content_hash', 'pending', 100, 3,"
            " $2 FROM videos v"
            f" WHERE v.library_id = $1 AND v.deleted_at IS NULL AND {pending}"
            " AND NOT EXISTS (SELECT 1 FROM processing_jobs j WHERE j.subject_kind = 'video'"
            "  AND j.subject_id = v.id AND j.stage = 'content_hash'"
            "  AND j.state IN ('pending','claimed','running','resuming','paused'))",
            uid(lib), dumps([{"why": "import: file not read yet", "by": "system", "from_stage": "import"}]),
        )  # fmt: skip
        after = int(
            await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'content_hash'")
        )
        n += after - before
    return n


# --------------------------------------------------------------------------- the import


async def run_import(
    db: Any,
    cache: Path,
    libraries: list[str],
    *,
    path_maps: list[tuple[str, str]] | None = None,
    apply: bool = False,
    settings: Path | None = None,
    thumbs: Path | None = None,
    create: bool = False,
) -> ImportReport:
    """Run the import; with ``apply=False`` everything is rolled back after counting.

    ``settings`` / ``thumbs`` default to ``settings.json`` / ``thumbs/`` beside the cache,
    when they exist. ``create`` makes the libraries (one per Plex library of the cache,
    or only those named) and their videos from the cache itself: nothing is read from the
    share, and each new video's file identity is a queued ``content_hash`` job."""
    maps = path_maps or []
    conn = _open_cache(cache)
    try:
        report = ImportReport(applied=apply)
        before = {t: int(await db.fetchval(f"SELECT count(*) FROM {t}")) for t in TABLES}
        try:
            async with db.transaction():
                if create:
                    libs = await _create_libraries(db, conn, libraries, maps, report)
                    await _analyze(db)
                else:
                    libs = await _resolve_libraries(db, libraries)
                await _import(db, conn, libs, maps, report)
                if create:
                    report.hash_jobs_queued = await _queue_content_hash(db, libs)
                await _settings(db, _beside(cache, settings, "settings.json"), report)
                with ThumbSource.open(_thumbs_at(cache, thumbs)) as source:
                    await _thumbs(db, conn, libs, maps, source, report, apply)
                await _flush_jobs(db, report)
                report.tables = {
                    t: (before[t], int(await db.fetchval(f"SELECT count(*) FROM {t}")))
                    for t in TABLES
                }
                if not apply:
                    raise _Rollback
        except _Rollback:
            pass
        return report
    finally:
        conn.close()


async def _import(
    db: Any,
    cache: sqlite3.Connection,
    libs: list[tuple[UUID, str]],
    maps: list[tuple[str, str]],
    report: ImportReport,
) -> None:
    files = {r["path"]: r for r in cache.execute("SELECT * FROM files")}
    report.files = len(files)
    by_norm: dict[str, tuple[UUID, UUID, int, float | None]] = {}
    for lib_id, _ in libs:
        for r in await db.fetch(
            "SELECT id, path, size_bytes, mtime FROM videos WHERE library_id = $1 AND deleted_at IS NULL",
            uid(lib_id),
        ):
            by_norm[_key(str(r["path"]))] = (
                UUID(str(r["id"])), lib_id, int(r["size_bytes"]), _epoch(r["mtime"]),
            )  # fmt: skip
    # cache path -> (video id, library id)
    match: dict[str, tuple[UUID, UUID]] = {}
    for path, f in files.items():
        hit = by_norm.get(_norm(path, maps))
        if hit is None:
            report.unmatched.append(path)
            continue
        vid, lib, size, mtime = hit
        if int(f["size"]) != size or (
            mtime is not None and abs(float(f["mtime"]) - mtime) >= MTIME_SLOP_S
        ):
            report.unmatched.append(f"{path} (size or mtime differs)")
            continue
        match[path] = (vid, lib)
    report.matched = len(match)

    await _youtube(db, cache, files, match)
    await _signals(db, cache, files, match)
    await _links(db, files, match)
    await _analyze(db)  # detect's writer reads videos and episodes per library
    detect_hashes: dict[UUID, str] = {}
    seasons = await _series(db, cache, match, report, detect_hashes)
    await _tags_styles_plex(db, cache, match, seasons)
    await _analyze(db)
    await _jobs(db, cache, files, match, libs, seasons, report, detect_hashes)
    for lib_id, _ in libs:
        res = await enqueue_job(
            db,
            subject_kind="library",
            subject_id=lib_id,
            stage="detect",
            reason={"why": "import", "by": "system"},
        )
        report.detect_queued += 1 if res.job_id is not None else 0


# --------------------------------------------------------------------------- youtube


def _d(db: Any, s: str | None) -> Any:
    return to_date(db, date.fromisoformat(s[:10])) if s else None


async def _youtube(
    db: Any, cache: sqlite3.Connection, files: dict[str, Any], match: dict[str, Any]
) -> None:
    wanted_ids = {files[p]["youtube_id"] for p in match if files[p]["youtube_id"]}
    lib_of_folder: dict[str, UUID] = {}
    for p, (_, lib) in match.items():
        lib_of_folder.setdefault(files[p]["folder"], lib)
    if _has(cache, "folders"):
        for f in cache.execute("SELECT * FROM folders WHERE channel_id IS NOT NULL"):
            lib = lib_of_folder.get(f["folder"])
            if lib is None:
                continue
            await db.execute(
                "INSERT INTO youtube_channels (channel_id, library_id, url, title, status, fetched_at)"
                " VALUES ($1,$2,$3,$4,'ok',$5) ON CONFLICT (channel_id) DO NOTHING",
                f["channel_id"], uid(lib), f["channel_url"] or f"https://www.youtube.com/channel/{f['channel_id']}",
                f["channel"], to_ts(db, now()),
            )  # fmt: skip
    if not _has(cache, "yt_videos"):
        return
    channel_ids = (
        {r[0] for r in cache.execute("SELECT channel_id FROM folders WHERE channel_id IS NOT NULL")}
        if _has(cache, "folders")
        else set()
    )
    stamp = to_ts(db, now())
    await _many(
        db,
        "INSERT INTO youtube_videos (youtube_id, channel_id, title, upload_date, date_quality,"
        " duration_s, tab, description, source, fetched_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)"
        " ON CONFLICT (youtube_id) DO NOTHING",
        [
            (v["youtube_id"], v["channel_id"], v["title"], _d(db, v["upload_date"]), v["date_quality"],
             v["duration_s"], v["tab"], v["description"], v["source"], stamp)
            for v in cache.execute("SELECT * FROM yt_videos")
            if v["youtube_id"] in wanted_ids or v["channel_id"] in channel_ids
        ],
    )  # fmt: skip
    if _has(cache, "yt_playlists"):
        pls = {
            r["playlist_id"]
            for r in cache.execute("SELECT playlist_id, youtube_id FROM yt_playlist_items")
            if r["youtube_id"] in wanted_ids
        }
        await _many(
            db,
            "INSERT INTO youtube_playlists (playlist_id, channel_id, title, item_count, is_real,"
            " source, fetched_at) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (playlist_id) DO NOTHING",
            [
                (p["playlist_id"], p["channel_id"], p["title"], p["item_count"],
                 bool(p["is_real"]) if is_pg(db) else int(p["is_real"]), p["source"], stamp)
                for p in cache.execute("SELECT * FROM yt_playlists")
                if p["playlist_id"] in pls
            ],
        )  # fmt: skip
        await _many(
            db,
            "INSERT INTO youtube_playlist_items (playlist_id, youtube_id, position)"
            " VALUES ($1,$2,$3) ON CONFLICT (playlist_id, youtube_id) DO NOTHING",
            [
                (i["playlist_id"], i["youtube_id"], i["position"])
                for i in cache.execute("SELECT * FROM yt_playlist_items")
                if i["playlist_id"] in pls
            ],
        )


# --------------------------------------------------------------------------- signals + links


async def _signals(
    db: Any, cache: sqlite3.Connection, files: dict[str, Any], match: dict[str, Any]
) -> None:
    vm = {}
    if _has(cache, "visual_metrics"):
        vm = {r["path"]: r for r in cache.execute("SELECT * FROM visual_metrics")}
    # one upsert per set of columns (upsert_signals' statement), many videos per round trip
    by_cols: dict[tuple[str, ...], list[tuple[Any, ...]]] = defaultdict(list)
    stamp = to_ts(db, now())
    for path, (vid, _lib) in match.items():
        f = files[path]
        cols: dict[str, Any] = {}
        if f["thumb_dhash"]:
            cols.update(thumb_dhash=f["thumb_dhash"], thumb_version=DHASH_VERSION)
        if f["audio_head"] or f["audio_tail"] or f["frame_hashes"]:
            cols.update(
                audio_head=f["audio_head"], audio_tail=f["audio_tail"], frame_hashes=f["frame_hashes"] or "",
                fp_version=int(f["audio_version"] or AUDIO_FP_VERSION) * 100 + int(f["frames_version"] or FRAMES_VERSION),
                fp_partial=False if is_pg(db) else 0,
            )  # fmt: skip
        m = vm.get(path)
        if m is not None:
            cols.update(
                style_metrics=json.dumps(metrics_from_dict(json.loads(m["metrics"])).to_dict()),
                style_version=int(m["version"]),
            )
        if cols:
            names = tuple(cols)
            by_cols[names].append((uid(vid), *[cols[c] for c in names], stamp))
    for names, rows in by_cols.items():
        insert_cols = ", ".join(["video_id", *names, "updated_at"])
        params = ", ".join(f"${i + 1}" for i in range(len(names) + 2))
        updates = ", ".join(f"{c} = EXCLUDED.{c}" for c in [*names, "updated_at"])
        await _many(
            db,
            f"INSERT INTO video_signals ({insert_cols}) VALUES ({params})"
            f" ON CONFLICT (video_id) DO UPDATE SET {updates}",
            rows,
        )


async def _links(db: Any, files: dict[str, Any], match: dict[str, Any]) -> None:
    yt = {
        str(r["youtube_id"]): r
        for r in await db.fetch(
            "SELECT youtube_id, title, upload_date, date_quality FROM youtube_videos"
        )
    }
    user_edited = {
        str(r["video_id"])
        for r in await db.fetch(
            "SELECT DISTINCT video_id FROM media_field_provenance WHERE origin = 'user'"
        )
    }
    stamp = to_ts(db, now())
    rows: list[tuple[Any, ...]] = []
    for path, (vid, _lib) in match.items():
        f = files[path]
        if not f["youtube_id"]:
            continue
        method = f["link_method"] or "info_json"
        yv = yt.get(str(f["youtube_id"]))
        mapped = {
            "link_method": method,
            "title": None if yv is None else yv["title"],
            "upload_date": None
            if yv is None or yv["upload_date"] is None
            else str(yv["upload_date"]),
            "date_quality": None if yv is None else yv["date_quality"],
        }
        rows.append((
            str(uuid.uuid4()), uid(vid), f["youtube_id"], dumps(mapped), LINK_CONFIDENCE.get(method, 0.6),
            str(vid) not in user_edited, stamp,
        ))  # fmt: skip
    await _many(
        db,
        "INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped, confidence,"
        " is_accepted, fetched_at) VALUES ($1,$2,'youtube',$3,$4,$5,$6,$7)"
        " ON CONFLICT (video_id, provider, external_id) DO NOTHING",
        rows,
    )


# --------------------------------------------------------------------------- series


async def _series(
    db: Any,
    cache: sqlite3.Connection,
    match: dict[str, tuple[UUID, UUID]],
    report: ImportReport,
    detect_hashes: dict[UUID, str],
) -> dict[str, tuple[UUID, UUID]]:
    """Series / seasons / episodes through the live ``detect`` writer. Returns cache series id ->
    (season id, library id)."""
    if not _has(cache, "series"):
        return {}
    eps: dict[str, list[Any]] = defaultdict(list)
    overrides: dict[str, list[Any]] = defaultdict(list)
    for e in cache.execute("SELECT * FROM series_episodes ORDER BY series_id, episode, path"):
        eps[e["series_id"]].append(e)
    per_lib: dict[UUID, list[detect_stage.PlannedSeason]] = defaultdict(list)
    out: dict[str, tuple[UUID, UUID]] = {}
    # one cache show = its seasons under one id_key (mediaintel gives two shows of one name,
    # split by length, distinct id_keys); its id key comes from the live detector's rule, so
    # twins never share a season id
    shows: dict[tuple[UUID, str, str], list[tuple[Any, dict[str, Any], list[Any]]]] = defaultdict(
        list
    )
    for s in cache.execute("SELECT * FROM series ORDER BY id"):
        meta = json.loads(s["metadata"] or "{}")
        rows = [e for e in eps.get(s["id"], []) if e["path"] in match]
        if not rows:
            continue
        lib = match[rows[0]["path"]][1]
        show = str(meta.get("id_key") or meta.get("series_key") or s["name"])
        shows[(lib, show, str(meta.get("kind") or "episodic"))].append((s, meta, rows))
    used: dict[UUID, set[UUID]] = defaultdict(set)
    keys: dict[str, str] = {}
    for (lib, _show, kind), members in sorted(
        shows.items(), key=lambda kv: (str(kv[0][0]), kv[0][1])
    ):
        _s0, meta0, _r0 = members[0]
        key = detect_stage.distinct_series_key(
            lib,
            str(meta0.get("series_key") or members[0][0]["name"]),
            kind,
            [int(m.get("season") or 1) for _, m, _ in members],
            str(meta0.get("folder") or ""),
            min(str(match[e["path"]][0]) for _, _, rows in members for e in rows),
            used[lib],
        )
        for s, meta, _rows in members:
            keys[s["id"]] = key
            used[lib].add(season_uuid(lib, key, kind, int(meta.get("season") or 1)))
    for (lib, _show, _kind), members in shows.items():
        for s, meta, rows in members:
            key, kind, number = (
                keys[s["id"]],
                str(meta.get("kind") or "episodic"),
                int(meta.get("season") or 1),
            )
            ids = tuple(match[e["path"]][0] for e in rows)
            nums = tuple(int(e["episode"] or i) for i, e in enumerate(rows, 1))
            extras = tuple(match[p][0] for p in meta.get("extras") or [] if p in match)
            sid = season_uuid(lib, key, kind, number)
            per_lib[lib].append(
            detect_stage.PlannedSeason(
                id=sid, series_id=series_uuid(lib, key, kind), series_key=key, series_name=str(s["name"]),
                kind=kind, number=number, episodes=ids, numbers=nums, extras=extras,
                attached=tuple(match[p][0] for p in meta.get("attached") or [] if p in match),
                duplicates=tuple(match[p][0] for p in meta.get("duplicates") or [] if p in match),
                playlists=tuple(meta.get("playlists") or []),
                first_air=date.fromisoformat(meta["first_air"]) if meta.get("first_air") else None,
                last_air=date.fromisoformat(meta["last_air"]) if meta.get("last_air") else None,
                median_duration_s=meta.get("median_duration_s"),
                membership_hash=detect_stage._membership_hash(
                    str(s["name"]), kind, number, ids, nums, extras, meta.get("season_name")
                ),
                season_name=meta.get("season_name"), season_year=meta.get("season_year"),
                brand=bool(meta.get("brand")),
            )
        )  # fmt: skip
            out[s["id"]] = (sid, lib)
            overrides[s["id"]] = [e for e in rows if e["is_user_override"]]
    for lib, seasons in per_lib.items():
        plan = detect_stage.LibraryPlan(tuple(seasons), (), ())
        current = await detect_stage.load_current(db, lib)
        await detect_stage.apply_changes(db, lib, plan, current)
        detect_hashes[lib] = detect_stage.output_hash(seasons)  # the live stage's own hash
    for rows in overrides.values():
        for e in rows:
            await db.execute(
                "UPDATE series_episodes SET season_override = season, episode_override = episode WHERE video_id = $1",
                uid(match[e["path"]][0]),
            )
    return out


# --------------------------------------------------------------------------- tags, styles, plex


async def _tags_styles_plex(
    db: Any, cache: sqlite3.Connection, match: dict[str, Any], seasons: dict[str, tuple[UUID, UUID]]
) -> None:
    stamp = to_ts(db, now())
    if _has(cache, "series_tags"):
        await _many(
            db,
            "INSERT INTO season_tags (season_id, tag, score, evidence) VALUES ($1,$2,$3,$4)"
            " ON CONFLICT (season_id, tag) DO NOTHING",
            [
                (uid(seasons[t["series_id"]][0]), t["tag"], t["score"], dumps({"why": t["evidence"] or ""}))
                for t in cache.execute("SELECT * FROM series_tags")
                if t["series_id"] in seasons
            ],
        )  # fmt: skip
    if _has(cache, "video_tags"):
        await _many(
            db,
            "INSERT INTO video_topic_tags (video_id, tag, score) VALUES ($1,$2,$3)"
            " ON CONFLICT (video_id, tag) DO NOTHING",
            [
                (uid(match[t["path"]][0]), t["tag"], t["score"])
                for t in cache.execute("SELECT * FROM video_tags")
                if t["path"] in match
            ],
        )
    ov = {}
    if _has(cache, "style_overrides"):
        ov = {
            (r["library"], r["series"]): r["style"]
            for r in cache.execute("SELECT * FROM style_overrides")
        }
    names = {
        s["id"]: (s["library"], s["name"])
        for s in cache.execute("SELECT id, library, name FROM series")
    }
    if _has(cache, "series_style"):
        for st in cache.execute("SELECT * FROM series_style"):
            if st["series_id"] not in seasons:
                continue
            override = ov.get(names.get(st["series_id"], ("", "")))
            await db.execute(
                "INSERT INTO season_style (season_id, style, tier, confidence, reasons, sampled, style_version,"
                " override_style, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)"
                " ON CONFLICT (season_id) DO NOTHING",
                uid(seasons[st["series_id"]][0]), st["style"], st["tier"], st["confidence"], st["reasons"],
                st["sampled"], st["version"], override, stamp,
            )  # fmt: skip
    if _has(cache, "plex_collections"):
        for c in cache.execute("SELECT * FROM plex_collections"):
            if c["series_id"] not in seasons:
                continue
            await db.execute(
                "INSERT INTO external_links (target, subject_kind, subject_id, external_id, title, content_hash,"
                " created_by_tool, updated_at) VALUES ('plex_collection','season',$1,$2,$3,'imported',$4,$5)"
                " ON CONFLICT (target, subject_kind, subject_id) DO NOTHING",
                str(seasons[c["series_id"]][0]), c["rating_key"], c["title"], True if is_pg(db) else 1, stamp,
            )  # fmt: skip


# --------------------------------------------------------------------------- synthetic jobs

_STAGE_OF = {"thumbnail": "thumb_hash", "signature": "fingerprint", "visual_style": "style_sample"}
_FP_VERSION_DEFAULT = SIG_VERSION


async def _write_job(
    db: Any,
    report: ImportReport,
    *,
    kind: str,
    subject_id: UUID,
    library_id: UUID | None,
    stage: str,
    version: int,
    input_hash: str | None,
    output_hash: str | None,
    error: str | None = None,
) -> None:
    """Buffer one synthetic ``processing_jobs`` row (written by :func:`_flush_jobs`); one the
    database already holds as imported with the same input and version is skipped."""
    if report._imported is None:
        report._imported = {
            (str(r["subject_kind"]), str(r["subject_id"]), str(r["stage"])): (
                r["input_hash"], r["stage_version"],
            )
            for r in await db.fetch(
                "SELECT subject_kind, subject_id, stage, input_hash, stage_version FROM processing_jobs"
                " WHERE state = 'done' AND outcome = 'imported' ORDER BY id"
            )
        }  # fmt: skip
    key = (kind, str(subject_id), stage)
    if not error and report._imported.get(key) == (input_hash, version):
        report.jobs_skipped_existing += 1
        return
    t = to_ts(db, now())
    failed = error is not None
    report._job_rows.append((
        kind, uid(subject_id), uid(subject_id) if kind == "video" else None, uid(library_id),
        uid(subject_id) if kind == "season" else None, stage, "failed" if failed else "done", version,
        input_hash, output_hash, None if failed else "imported", t,
        None if not failed else json.dumps({"kind": "import", "message": error, "retryable": True}),
        None if not failed else classify("import", error or ""),
    ))  # fmt: skip
    if not failed:
        report._imported[key] = (input_hash, version)
    if failed:
        report.jobs_failed += 1
    else:
        report.jobs_imported += 1


async def _flush_jobs(db: Any, report: ImportReport) -> None:
    """Write the buffered synthetic rows. Called before any stage fingerprint that reads
    earlier jobs (a video's link reads its library's youtube_sync, detect reads every
    video stage, the season stages read detect), so the hashes match the live stages'."""
    rows, report._job_rows = report._job_rows, []
    await _many(
        db,
        "INSERT INTO processing_jobs (subject_kind, subject_id, video_id, library_id, season_id, stage, state,"
        " attempts, max_attempts, stage_version, input_hash, output_hash, outcome, propagated_at, started_at,"
        " finished_at, error, error_cause) VALUES ($1,$2,$3,$4,$5,$6,$7,1,3,$8,$9,$10,$11,$12,$12,$12,$13,$14)",
        rows,
    )  # fmt: skip


async def _jobs(
    db: Any,
    cache: sqlite3.Connection,
    files: dict[str, Any],
    match: dict[str, tuple[UUID, UUID]],
    libs: list[tuple[UUID, str]],
    seasons: dict[str, tuple[UUID, UUID]],
    report: ImportReport,
    detect_hashes: dict[UUID, str],
) -> None:
    done: dict[tuple[str, str], Any] = {}
    if _has(cache, "index_jobs"):
        for j in cache.execute("SELECT * FROM index_jobs"):
            if j["path"] in match and j["stage"] in _STAGE_OF:
                done[(j["path"], _STAGE_OF[j["stage"]])] = j
    # 1. per-video stages, in dependency order
    for stage in ("thumb_hash", "fingerprint", "style_sample"):
        for (path, st), j in sorted(done.items()):
            if st != stage:
                continue
            vid, lib = match[path]
            if j["state"] != "done":
                await _write_job(db, report, kind="video", subject_id=vid, library_id=lib, stage=stage,
                                 version=int(j["version"]), input_hash=None, output_hash=None, error=j["error"] or "failed in cache")  # fmt: skip
                continue
            sig = await db.fetchrow(
                "SELECT thumb_dhash, audio_head, audio_tail, frame_hashes, style_metrics FROM video_signals WHERE video_id = $1",
                uid(vid),
            )
            subject = Subject("video", vid)
            oh: str | None
            if stage == "thumb_hash":
                ih = await thumb_stage.input_fingerprint(db, subject)
                oh = canonical_hash({"dhash": None if sig is None else sig["thumb_dhash"]})
                ver = DHASH_VERSION
            elif stage == "fingerprint":
                ih = await fingerprint_stage.input_fingerprint(db, subject)
                oh = canonical_hash(
                    {
                        "head": sig["audio_head"],
                        "tail": sig["audio_tail"],
                        "frames": sig["frame_hashes"],
                    }
                )
                ver = SIG_VERSION if int(j["version"]) == SIG_VERSION else int(j["version"])
            else:
                ih = await style_sample_stage.input_fingerprint(db, subject)
                oh = (
                    canonical_hash(loads(sig["style_metrics"]))
                    if sig and sig["style_metrics"]
                    else None
                )
                ver = int(j["version"]) if j["version"] else STYLE_VERSION
            await _write_job(db, report, kind="video", subject_id=vid, library_id=lib, stage=stage, version=ver, input_hash=ih, output_hash=oh)  # fmt: skip
        await _flush_jobs(db, report)  # the next stage's fingerprint may read this one
    # 2. youtube_sync + link per linked video
    for lib_id, _ in libs:
        chans = await db.fetchval(
            "SELECT count(*) FROM youtube_channels WHERE library_id = $1", uid(lib_id)
        )
        if chans:
            snap = await yt_stage._snapshot(db, lib_id)
            await _write_job(db, report, kind="library", subject_id=lib_id, library_id=lib_id, stage="youtube_sync",
                             version=1, input_hash=None, output_hash=snap.digest())  # fmt: skip
    await _flush_jobs(db, report)  # link's fingerprint reads its library's youtube_sync
    for path, (vid, lib) in sorted(match.items()):
        f = files[path]
        if not f["youtube_id"]:
            continue
        method = f["link_method"] or "info_json"
        ih = await link_stage.input_fingerprint(db, Subject("video", vid))
        await _write_job(db, report, kind="video", subject_id=vid, library_id=lib, stage="link", version=1,
                         input_hash=ih, output_hash=canonical_hash({"youtube_id": f["youtube_id"], "method": method}))  # fmt: skip
    await _flush_jobs(db, report)  # detect's fingerprint reads every video stage
    # 3. detect (library), then the season stages that depend on it
    for lib_id, _ in libs:
        rows = await db.fetch(
            "SELECT id, membership_hash FROM series_seasons WHERE library_id = $1 AND retired_at IS NULL",
            uid(lib_id),
        )
        if rows and lib_id in detect_hashes:
            ih = await detect_stage.input_fingerprint(db, Subject("library", lib_id))
            await _write_job(db, report, kind="library", subject_id=lib_id, library_id=lib_id, stage="detect",
                             version=DETECTOR_VERSION, input_hash=ih, output_hash=detect_hashes[lib_id])  # fmt: skip
    await _flush_jobs(db, report)
    for sid, lib in seasons.values():
        tags = await db.fetch("SELECT tag, score FROM season_tags WHERE season_id = $1", uid(sid))
        if tags:
            ih = await tag_stage.input_fingerprint(db, Subject("season", sid))
            oh = canonical_hash(sorted([str(t["tag"]), round(float(t["score"]), 3)] for t in tags))
            await _write_job(db, report, kind="season", subject_id=sid, library_id=lib, stage="tag", version=1, input_hash=ih, output_hash=oh)  # fmt: skip
        st = await db.fetchrow(
            "SELECT style, tier, style_version FROM season_style WHERE season_id = $1", uid(sid)
        )
        if st is not None and st["style"]:
            ih = await rate_style_stage.input_fingerprint(db, Subject("season", sid))
            await _write_job(db, report, kind="season", subject_id=sid, library_id=lib, stage="rate_style",
                             version=int(st["style_version"] or STYLE_VERSION), input_hash=ih,
                             output_hash=canonical_hash({"style": st["style"], "tier": int(st["tier"])}))  # fmt: skip
    await _flush_jobs(db, report)


# --------------------------------------------------------------------------- rules and thumbnails


def _beside(cache: Path, given: Path | None, name: str) -> Path | None:
    if given is not None:
        return given
    p = cache.parent / name
    return p if p.exists() else None


async def _settings(db: Any, path: Path | None, report: ImportReport) -> None:
    """``settings.json`` ({section: {setting: value}}, only what differs from the defaults)
    into ``app_settings`` ``rules.<section>``. Each value is checked as the stages read it;
    a value Maktaba already has is kept (the user may have changed it there since)."""
    if path is None:
        return
    raw = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(raw, dict):
        report.settings_skipped.append(f"{path.name}: not a JSON object")
        return
    for section, changes in sorted(raw.items()):
        if section not in rules.SECTIONS:
            report.settings_skipped.append(f"{section}: not a rules section")
            continue
        if not isinstance(changes, dict) or not changes:
            continue
        _, bad = rules.config(section, changes)
        report.settings_skipped.extend(f"{section}.{k}: {why}" for k, why in sorted(bad.items()))
        _, have, _ = await rules.read(db, section)
        for k in sorted(set(have) & set(changes)):
            if have[k] != changes[k]:
                report.settings_skipped.append(f"{section}.{k}: already set in Maktaba; kept")
        add = {k: v for k, v in changes.items() if k not in bad and k not in have}
        if not add:
            continue
        await db.execute(
            "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)"
            " ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at",
            f"rules.{section}",
            dumps({**have, **add}),
            to_ts(db, now()),
        )
        report.settings_imported[section] = len(add)


async def _thumbs(
    db: Any,
    cache: sqlite3.Connection,
    libs: list[tuple[UUID, str]],
    maps: list[tuple[str, str]],
    source: ThumbSource | None,
    report: ImportReport,
    apply: bool,
) -> None:
    """mediaintel's small copies into Maktaba's thumbnail cache, each recorded as a done
    ``thumb_small`` job. A copy is named after its source (the sidecar image, else the
    video) as the cache stores it; files are written on ``--apply`` only."""
    if source is None:
        return
    videos: dict[str, tuple[UUID, UUID, str | None]] = {}
    for lib_id, _ in libs:
        for r in await db.fetch(
            "SELECT id, path, content_hash, metadata FROM videos WHERE library_id = $1"
            " AND deleted_at IS NULL",
            uid(lib_id),
        ):
            key = _key(str(r["path"]))
            # a stand-in hash names no cache folder: the copy waits under the video's id
            ch = (
                None
                if is_pending(loads(r["metadata"])) or r["content_hash"] is None
                else str(r["content_hash"])
            )
            videos[key] = (UUID(str(r["id"])), lib_id, ch)
    for f in cache.execute("SELECT path, thumb_path FROM files ORDER BY path"):
        hit = videos.get(_norm(f["path"], maps))
        if hit is None:
            continue
        vid, lib, content_hash = hit
        src = next(
            (n for n in (thumbs_name(x) for x in (f["thumb_path"], f["path"]) if x) if n in source),
            None,
        )
        if src is None:
            report.thumbs_missing += 1
            continue
        # not hashed yet: staged by the video's id; thumb_small moves it in after content_hash
        dst = small_path(content_hash) if content_hash else staged_path(vid)
        if dst.is_file() and dst.stat().st_size > 0:
            report.thumbs_present += 1
        else:
            report.thumbs_copied += 1
            if apply:
                dst.parent.mkdir(parents=True, exist_ok=True)
                tmp = dst.with_name(dst.stem + ".part.jpg")
                source.copy(src, tmp)
                tmp.replace(dst)
        if content_hash is None:
            continue  # thumb_small is not done until the real hash names its folder
        size = dst.stat().st_size if dst.is_file() else source.size(src)
        ih = await thumb_small_stage.input_fingerprint(db, Subject("video", vid))
        await _write_job(db, report, kind="video", subject_id=vid, library_id=lib, stage="thumb_small",
                         version=THUMB_SMALL_VERSION, input_hash=ih,
                         output_hash=canonical_hash({"small": dst.name, "size": size}))  # fmt: skip


def _thumbs_at(cache: Path, given: Path | None) -> Path | None:
    """``--thumbs``, else ``thumbs/`` beside the cache, else ``thumbs.tar`` beside it."""
    if given is not None:
        return given
    return next(
        (p for p in (cache.parent / "thumbs", cache.parent / "thumbs.tar") if p.exists()), None
    )


class ThumbSource:
    """mediaintel's thumbnail copies by file name: a ``thumbs/`` folder, or the
    ``thumbs.tar`` it ships as (81k tiny files copy slowly over SMB; the archive is read
    in place, nothing is unpacked besides the copies the import writes)."""

    def __init__(self, folder: Path | None = None, archive: tarfile.TarFile | None = None) -> None:
        self._folder = folder
        self._tar = archive
        self._members: dict[str, tarfile.TarInfo] = {}
        if archive is not None:
            for m in archive.getmembers():
                if m.isfile():
                    self._members[posixpath.basename(m.name)] = m

    @classmethod
    @contextlib.contextmanager
    def open(cls, path: Path | None) -> Iterator[ThumbSource | None]:
        if path is None or not path.exists():
            yield None
        elif path.is_dir():
            yield cls(folder=path)
        else:
            with tarfile.open(path) as tf:
                yield cls(archive=tf)

    def __contains__(self, name: object) -> bool:
        if not isinstance(name, str):
            return False
        if self._folder is not None:
            return (self._folder / name).is_file()
        return name in self._members

    def size(self, name: str) -> int:
        if self._folder is not None:
            return (self._folder / name).stat().st_size
        return int(self._members[name].size)

    def copy(self, name: str, dst: Path) -> None:
        if self._folder is not None:
            shutil.copyfile(self._folder / name, dst)
            return
        assert self._tar is not None
        src = self._tar.extractfile(self._members[name])
        if src is None:
            raise FileNotFoundError(name)
        with src, dst.open("wb") as out:
            shutil.copyfileobj(src, out)


def thumbs_name(source: str) -> str:
    """mediaintel's file name for the small copy of ``source`` (as the cache stores it)."""
    return hashlib.sha1(source.encode()).hexdigest()[:16] + ".jpg"  # noqa: S324 - a file name


# --------------------------------------------------------------------------- cli


def _parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="import_mediaintel", description=__doc__.split("\n\n")[0] if __doc__ else ""
    )
    p.add_argument(
        "--cache", required=True, type=Path, help="mediaintel cache database (opened read-only)"
    )
    p.add_argument(
        "--library",
        action="append",
        default=[],
        help="Maktaba library id or name (repeatable); with --create, a Plex library name of"
        " the cache (default: all of them)",
    )
    p.add_argument(
        "--path-map", action="append", default=[], help="CACHE_PREFIX=MAKTABA_PREFIX (repeatable)"
    )
    p.add_argument(
        "--settings", type=Path, help="mediaintel's settings.json (default: beside the cache)"
    )
    p.add_argument(
        "--thumbs",
        type=Path,
        help="mediaintel's thumbs/ folder or thumbs.tar (default: either, beside the cache)",
    )
    p.add_argument("--database-url", default=os.environ.get("MAKTABA_DATABASE_URL", ""))
    p.add_argument(
        "--create",
        action="store_true",
        help="make the libraries and videos from the cache (no scan, no read of the share);"
        " each video's file identity is queued as a content_hash job",
    )
    p.add_argument("--apply", action="store_true", help="commit (default: dry run, rolled back)")
    return p


async def _amain(args: argparse.Namespace) -> int:
    from ..runtime import Database

    if not args.database_url:
        sys.stderr.write("set --database-url or MAKTABA_DATABASE_URL\n")
        return 2
    db = await Database.connect(args.database_url)
    try:
        report = await run_import(
            db, args.cache, args.library, path_maps=parse_path_maps(args.path_map),
            apply=args.apply, settings=args.settings, thumbs=args.thumbs, create=args.create,
        )  # fmt: skip
    finally:
        await db.close()
    sys.stdout.write(report.render() + "\n")
    return 0


def main(argv: list[str] | None = None) -> int:
    return asyncio.run(_amain(_parser().parse_args(argv)))


if __name__ == "__main__":
    raise SystemExit(main())
