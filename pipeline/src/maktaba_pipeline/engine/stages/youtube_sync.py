"""``youtube_sync``: list a library's YouTube channels and playlists (plan 06-11 section 3.2).

Subject: a library. Pool ``network_yt``. No input fingerprint: the listing is
external, so the stage always runs when queued (a scheduled refresh, a manual
run, or a trigger from ``scan``).

What it does:

1. **Discover** the library's channels. They come from the ``youtube_channels``
   table plus the *folder-level* yt-dlp sidecars under the library roots (a
   playlist-level ``.info.json`` next to the videos names the channel and,
   for a real playlist, its items). Per-video sidecars are the ``link``
   stage's business, not this one's.
2. **List** every channel that is not ``gone`` (a payload ``{"refresh": true}``
   retries the gone ones) through :mod:`maktaba_pipeline.integrations.youtube`
   and upsert ``youtube_channels``, ``youtube_videos``, ``youtube_playlists`` and
   ``youtube_playlist_items``.
3. The **output hash** covers the library's playlists, playlist items and videos
   (never timestamps), so an identical re-listing propagates nothing.
   ``changed_subjects`` names the library's videos whose YouTube row changed.

A bot check records ``status = 'bot_check'`` on the channel and fails the job
with the kind ``youtube_bot_check``, which parks it on ``dependency:youtube`` (Epic 40):
retrying at once only digs the hole deeper.

Epic 47 (story 47.8) adds the **check** half of "make it offline": before the listing
above, every *enabled* ``youtube_sources`` row (a channel or a playlist) is listed flat
(:func:`maktaba_pipeline.library_mgmt.youtube_sources.list_source`; a channel listed in
full once is read only up to the first id already known, ``--break-on-existing``), every
listed id the library lacks is marked ``wanted`` (``backfill`` when it is older than the
source), and ``youtube_fetch`` (new uploads, priority 120) or ``youtube_backfill`` (the
back catalogue, priority 300) is queued. A payload ``{"full_listing": true}`` reads every
source in full.
"""

from __future__ import annotations

import asyncio
import os
import pickle
from collections.abc import Callable, Iterable, Sequence
from dataclasses import dataclass, field
from datetime import date
from pathlib import Path
from typing import Any
from uuid import UUID

from ...discovery.ytdlp_sidecars import PlaylistInfo, VideoInfo, parse_info_json
from ...integrations.youtube import (
    BotCheck,
    ChannelListing,
    ChannelUnavailable,
    YouTubeFetcher,
    YtDlpFetcher,
    list_channel,
)
from ...integrations.youtube_errors import BOT_CHECK_KIND, RATE_LIMITED_KIND
from ...library_mgmt import tartube_import
from ...library_mgmt import youtube_sources as ys
from ...scanner.walker import DEFAULT_IGNORE_DIRNAMES, DEFAULT_VIDEO_EXTENSIONS
from ..context import StageContext, StageFailure
from ..graph import EPIC47_PRIORITIES, Request, StageResult, SubjectKind
from ..hashing import canonical_hash
from ..sql import is_pg, loads, now, to_ts, uid
from .registry import StageImpl

__all__ = [
    "IMPL",
    "FetcherFactory",
    "discover_folders",
    "new_fetcher",
    "run",
    "set_fetcher_factory",
]

FetcherFactory = Callable[[], YouTubeFetcher]

_fetcher_factory: FetcherFactory = YtDlpFetcher
_IN_CHUNK = 400


def new_fetcher() -> YouTubeFetcher:
    """A fetcher from the current factory (yt-dlp unless a test replaced it)."""
    return _fetcher_factory()


def set_fetcher_factory(factory: FetcherFactory | None) -> FetcherFactory:
    """Replace the fetcher factory (tests); None restores yt-dlp. Returns the previous one."""
    global _fetcher_factory
    previous = _fetcher_factory
    _fetcher_factory = factory or YtDlpFetcher
    return previous


# ------------------------------------------------------------------ folder discovery


@dataclass(slots=True)
class FolderDiscovery:
    """What the folder-level sidecars under the library roots say."""

    #: channel_id -> (url, title)
    channels: dict[str, tuple[str | None, str | None]] = field(default_factory=dict)
    #: real playlists found as sidecars
    playlists: list[PlaylistInfo] = field(default_factory=list)


def _is_format_piece(name: str) -> bool:
    # ``clip.f137.mp4``: a yt-dlp format piece, not a video of its own
    parts = name.rsplit(".", 2)
    return len(parts) == 3 and parts[1].startswith("f") and parts[1][1:].isdigit()


def discover_folders(roots: Iterable[str]) -> FolderDiscovery:
    """Port of the folder-level part of ``scan_local.scan_folder`` (blocking file I/O)."""
    out = FolderDiscovery()
    seen_playlists: set[str] = set()
    for root in roots:
        for dirpath, dirnames, filenames in os.walk(root):
            dirnames[:] = sorted(d for d in dirnames if d not in DEFAULT_IGNORE_DIRNAMES)
            names = sorted(filenames)
            stems = {
                n.rsplit(".", 1)[0]
                for n in names
                if os.path.splitext(n)[1].lower() in DEFAULT_VIDEO_EXTENSIONS
                and ".part" not in n
                and not _is_format_piece(n)
            }
            channel_id: str | None = None
            channel_url: str | None = None
            channel: str | None = None
            for n in names:
                if not n.endswith(".info.json") or n[: -len(".info.json")] in stems:
                    continue  # per-video sidecars belong to the ``link`` stage
                text = (Path(dirpath) / n).read_text(encoding="utf-8", errors="replace")
                info = parse_info_json(text)
                if isinstance(info, PlaylistInfo):
                    if info.is_real and info.playlist_id not in seen_playlists:
                        seen_playlists.add(info.playlist_id)
                        out.playlists.append(info)
                    channel_id = channel_id or info.channel_id
                    channel_url = channel_url or info.channel_url
                    channel = channel or info.channel
                elif isinstance(info, VideoInfo):
                    channel_id = channel_id or info.channel_id
                    channel = channel or info.channel
            if channel_id:
                url, title = out.channels.get(channel_id, (None, None))
                out.channels[channel_id] = (url or channel_url, title or channel)
    return out


# ------------------------------------------------------------------ database helpers


def _date(db: Any, d: date | None) -> date | str | None:
    if d is None or is_pg(db):
        return d
    return d.isoformat()


async def _roots(db: Any, library_id: UUID) -> list[str]:
    row = await db.fetchrow("SELECT roots FROM libraries WHERE id = $1", uid(library_id))
    if row is None:
        raise StageFailure(
            "library_missing", f"library {library_id} does not exist", retryable=False
        )
    roots = loads(row["roots"]) or []
    return [str(r) for r in roots]


_OWN_CHANNELS = "(SELECT channel_id FROM youtube_channels WHERE library_id = $1)"


@dataclass(slots=True)
class Snapshot:
    videos: dict[str, tuple[Any, ...]]
    playlists: dict[str, tuple[Any, ...]]
    items: dict[str, tuple[tuple[str, int | None], ...]]

    def digest(self) -> str:
        return canonical_hash(
            {
                "videos": sorted([k, *v] for k, v in self.videos.items()),
                "playlists": sorted([k, *v] for k, v in self.playlists.items()),
                "items": sorted([k, [list(i) for i in v]] for k, v in self.items.items()),
            }
        )


async def _snapshot(db: Any, library_id: UUID) -> Snapshot:
    lib = uid(library_id)
    vrows = await db.fetch(
        "SELECT youtube_id, channel_id, title, CAST(upload_date AS TEXT) AS d, date_quality,"
        " duration_s, tab FROM youtube_videos"
        f" WHERE channel_id IN {_OWN_CHANNELS} ORDER BY youtube_id",
        lib,
    )
    videos = {
        str(r["youtube_id"]): (
            r["channel_id"],
            r["title"],
            None if r["d"] is None else str(r["d"])[:10],
            r["date_quality"],
            None if r["duration_s"] is None else round(float(r["duration_s"]), 3),
            r["tab"],
        )
        for r in vrows
    }
    prows = await db.fetch(
        "SELECT playlist_id, channel_id, title, item_count, is_real FROM youtube_playlists"
        f" WHERE channel_id IN {_OWN_CHANNELS} ORDER BY playlist_id",
        lib,
    )
    playlists = {
        str(r["playlist_id"]): (
            r["channel_id"],
            r["title"],
            None if r["item_count"] is None else int(r["item_count"]),
            bool(r["is_real"]),
        )
        for r in prows
    }
    irows = await db.fetch(
        "SELECT i.playlist_id AS playlist_id, i.youtube_id AS youtube_id, i.position AS position"
        " FROM youtube_playlist_items i JOIN youtube_playlists p ON p.playlist_id = i.playlist_id"
        f" WHERE p.channel_id IN {_OWN_CHANNELS} ORDER BY i.playlist_id, i.youtube_id",
        lib,
    )
    items: dict[str, list[tuple[str, int | None]]] = {}
    for r in irows:
        items.setdefault(str(r["playlist_id"]), []).append(
            (str(r["youtube_id"]), None if r["position"] is None else int(r["position"]))
        )
    return Snapshot(videos, playlists, {k: tuple(v) for k, v in items.items()})


def changed_youtube_ids(before: Snapshot, after: Snapshot) -> set[str]:
    """YouTube ids whose own row changed, plus every member of a changed playlist."""
    changed = {k for k, v in after.videos.items() if before.videos.get(k) != v}
    for pid in set(before.playlists) | set(after.playlists):
        if before.playlists.get(pid) == after.playlists.get(pid) and before.items.get(
            pid
        ) == after.items.get(pid):
            continue
        for snap in (before, after):
            changed.update(y for y, _ in snap.items.get(pid, ()))
    return changed


async def _upsert_discovered_channel(
    db: Any, library_id: UUID, channel_id: str, url: str | None, title: str | None
) -> None:
    await db.execute(
        "INSERT INTO youtube_channels (channel_id, library_id, url, title, fetched_at)"
        " VALUES ($1,$2,$3,$4,$5) ON CONFLICT (channel_id) DO UPDATE SET"
        " library_id = COALESCE(youtube_channels.library_id, EXCLUDED.library_id),"
        " title = COALESCE(youtube_channels.title, EXCLUDED.title)",
        channel_id,
        uid(library_id),
        url or f"https://www.youtube.com/channel/{channel_id}",
        title,
        to_ts(db, now()),
    )


async def _upsert_sidecar_playlist(db: Any, info: PlaylistInfo) -> None:
    await db.execute(
        "INSERT INTO youtube_playlists (playlist_id, channel_id, title, item_count, is_real,"
        " source, fetched_at) VALUES ($1,$2,$3,$4,$5,'info_json',$6)"
        " ON CONFLICT (playlist_id) DO UPDATE SET title = EXCLUDED.title,"
        " item_count = COALESCE(EXCLUDED.item_count, youtube_playlists.item_count),"
        " channel_id = COALESCE(youtube_playlists.channel_id, EXCLUDED.channel_id)",
        info.playlist_id,
        info.channel_id,
        info.title or info.playlist_id,
        info.count,
        True,
        to_ts(db, now()),
    )
    for pos, vid in enumerate(info.entry_ids, 1):
        await db.execute(
            "INSERT INTO youtube_playlist_items (playlist_id, youtube_id, position)"
            " VALUES ($1,$2,$3) ON CONFLICT DO NOTHING",
            info.playlist_id,
            vid,
            pos,
        )


async def _set_channel_status(
    db: Any, channel_id: str, status: str, error: str | None, title: str | None = None
) -> None:
    await db.execute(
        "UPDATE youtube_channels SET status = $2, error = $3,"
        " title = COALESCE($4, title), fetched_at = $5 WHERE channel_id = $1",
        channel_id,
        status,
        None if error is None else error[:500],
        title,
        to_ts(db, now()),
    )


async def _store_listing(db: Any, listing: ChannelListing) -> None:
    ts = to_ts(db, now())
    cid = listing.channel_id
    async with db.transaction():
        # a video seen on a channel tab is authoritative; playlist entries only add the unseen
        tab_ids = {v.youtube_id for v in listing.videos}
        extra = {v.youtube_id: v for v in listing.playlist_videos if v.youtube_id not in tab_ids}
        for v in (*listing.videos, *extra.values()):
            quality = "approximate" if v.upload_date is not None else None
            await db.execute(
                "INSERT INTO youtube_videos (youtube_id, channel_id, title, upload_date,"
                " date_quality, duration_s, tab, source, fetched_at)"
                " VALUES ($1,$2,$3,$4,$5,$6,$7,'youtube',$8)"
                " ON CONFLICT (youtube_id) DO UPDATE SET"
                " duration_s = COALESCE(youtube_videos.duration_s, EXCLUDED.duration_s),"
                " channel_id = COALESCE(youtube_videos.channel_id, EXCLUDED.channel_id),"
                " tab = COALESCE(youtube_videos.tab, EXCLUDED.tab),"
                " upload_date = CASE WHEN youtube_videos.date_quality IS NULL"
                "   THEN EXCLUDED.upload_date ELSE youtube_videos.upload_date END,"
                " date_quality = CASE WHEN youtube_videos.date_quality IS NULL"
                "   THEN EXCLUDED.date_quality ELSE youtube_videos.date_quality END,"
                " title = CASE WHEN youtube_videos.source = 'info_json'"
                "   THEN youtube_videos.title ELSE EXCLUDED.title END",
                v.youtube_id,
                cid,
                v.title,
                _date(db, v.upload_date),
                quality,
                v.duration_s,
                v.tab,
                ts,
            )
        for p in listing.playlists:
            await db.execute(
                "INSERT INTO youtube_playlists (playlist_id, channel_id, title, item_count,"
                " is_real, source, fetched_at) VALUES ($1,$2,$3,$4,$5,'youtube',$6)"
                " ON CONFLICT (playlist_id) DO UPDATE SET title = EXCLUDED.title,"
                " item_count = EXCLUDED.item_count, source = 'youtube',"
                " channel_id = COALESCE(youtube_playlists.channel_id, EXCLUDED.channel_id)",
                p.playlist_id,
                cid,
                p.title,
                len(p.items),
                p.is_real,
                ts,
            )
            await db.execute(
                "DELETE FROM youtube_playlist_items WHERE playlist_id = $1", p.playlist_id
            )
            for pos, yid in enumerate(p.items, 1):
                await db.execute(
                    "INSERT INTO youtube_playlist_items (playlist_id, youtube_id, position)"
                    " VALUES ($1,$2,$3) ON CONFLICT DO NOTHING",
                    p.playlist_id,
                    yid,
                    pos,
                )
        await db.execute(
            "UPDATE youtube_channels SET status = 'ok', error = NULL,"
            " title = COALESCE($2, title), url = $3, fetched_at = $4 WHERE channel_id = $1",
            cid,
            listing.title,
            listing.url,
            ts,
        )


async def _linked_videos(db: Any, library_id: UUID, youtube_ids: Sequence[str]) -> set[UUID]:
    """Library videos linked (provider ``youtube``) to any of ``youtube_ids``."""
    found: set[UUID] = set()
    ids = sorted(youtube_ids)
    for i in range(0, len(ids), _IN_CHUNK):
        chunk = ids[i : i + _IN_CHUNK]
        marks = ",".join(f"${n + 2}" for n in range(len(chunk)))
        rows = await db.fetch(
            "SELECT m.video_id AS id FROM media_metadata_enrichment m"
            " JOIN videos v ON v.id = m.video_id"
            f" WHERE v.library_id = $1 AND v.deleted_at IS NULL AND m.provider = 'youtube'"
            f" AND m.external_id IN ({marks})",
            uid(library_id),
            *chunk,
        )
        found.update(UUID(str(r["id"])) for r in rows)
    return found


async def _unlinked_videos(db: Any, library_id: UUID) -> set[UUID]:
    rows = await db.fetch(
        "SELECT v.id AS id FROM videos v WHERE v.library_id = $1 AND v.deleted_at IS NULL"
        " AND NOT EXISTS (SELECT 1 FROM media_metadata_enrichment m"
        "  WHERE m.video_id = v.id AND m.provider = 'youtube')",
        uid(library_id),
    )
    return {UUID(str(r["id"])) for r in rows}


# ------------------------------------------------------------------ sources (Epic 47)


async def _import_tartube(
    db: Any, library_id: UUID, ctx: StageContext, spec: Any
) -> dict[str, Any]:
    """Seed the library from a Tartube database the owner named (payload ``{"tartube":
    {"path": ..., "database": ..., "channels": [...]}}``; a path on this machine)."""
    if not isinstance(spec, dict) or not isinstance(spec.get("path"), str):
        raise StageFailure("tartube_bad_payload", "tartube needs a path", retryable=False)
    path = ctx.local_path(str(spec["path"]))
    db_path = spec.get("database")
    database = ctx.local_path(db_path) if isinstance(db_path, str) and db_path else None
    channels = [str(c) for c in spec.get("channels") or [] if isinstance(c, str)]
    if not os.path.isabs(path) or not os.path.isfile(path):
        raise StageFailure(
            "tartube_missing", f"no Tartube export or database at {path}", retryable=False
        )
    try:
        ids, rep = await asyncio.to_thread(
            tartube_import.downloaded_ids,
            path,
            database=database,
            channels=channels,
            to_local=ctx.local_path,
        )
    except tartube_import.TartubeRefused as exc:
        raise StageFailure("tartube_refused", str(exc), retryable=False) from exc
    except (OSError, ValueError, EOFError, pickle.UnpicklingError) as exc:
        raise StageFailure(
            "tartube_unreadable", f"{path}: {type(exc).__name__}: {exc}", retryable=False
        ) from exc
    linked = await ys.linked_video_ids(db, library_id)
    seeded = await ys.seed_done(
        db, library_id, ids, reason="tartube", video_ids={i: linked[i] for i in ids if i in linked}
    )
    out = rep.as_json()
    out["seeded"] = seeded
    await ctx.log("info", f"Tartube: {rep.downloaded} downloaded, {seeded} new", out)
    return out


def _listing_archive(library_id: UUID) -> str:
    from ._youtube_drain import work_root

    folder = work_root() / str(library_id)
    folder.mkdir(parents=True, exist_ok=True)
    return str(folder / "listing-archive.txt")


async def _check_sources(
    db: Any, job: Any, ctx: StageContext, *, full: bool, refresh: bool
) -> dict[str, Any]:
    """List the enabled sources, mark what the library lacks ``wanted``, queue the
    downloads. Raises the parking failures (bot check, too many requests)."""
    library_id: UUID = job.subject.id
    await ys.ensure_channel_sources(db, library_id)
    sources = await ys.load_sources(db, library_id, enabled_only=True)
    report: dict[str, Any] = {"sources": len(sources)}
    tartube = (job.payload or {}).get("tartube")
    if sources or tartube:
        # story 47.12: what the library has is never downloaded again (before any listing)
        report["seeded_library"] = await ys.seed_from_library(db, library_id)
    if tartube:
        report["tartube"] = await _import_tartube(db, library_id, ctx, tartube)
    if not sources:
        return report
    todo = [s for s in sources if refresh or s.status != "gone"]
    known = await ys.known_ids(db, library_id)
    local = await ys.local_ids(db, library_id)
    archive = _listing_archive(library_id)
    totals = ys.MarkReport()
    counts = {"ok": 0, "gone": 0, "error": 0}
    for n, src in enumerate(todo, 1):
        await ctx.check_cancel()
        await ctx.progress("sources", n - 1, len(todo), src.url)
        first = src.last_checked_at is None
        # a playlist is in no particular order, and a channel never listed in full has
        # no head to stop at: both are read in full
        incremental = not (full or first or src.kind == "playlist")
        await asyncio.to_thread(ys.write_archive, archive, known)
        try:
            listing = await ys.list_source(src, archive=archive, break_on_existing=incremental)
        except ys.ListingFailed as exc:
            err = exc.error
            if err.action == "bot_check":
                await ys.set_source_status(db, src.id, "bot_check", err.message)
                raise StageFailure(
                    BOT_CHECK_KIND,
                    f"YouTube asked to sign in (listing {src.url}); waiting before the next"
                    " try, or give the source a signed-in browser's cookies",
                ) from exc
            if err.action == "rate_limited":
                await ys.set_source_status(db, src.id, "rate_limited", err.message)
                raise StageFailure(
                    RATE_LIMITED_KIND,
                    f"YouTube said too many requests (listing {src.url}); waiting at least an hour",
                ) from exc
            status = "gone" if err.action == "gone" else "error"
            await ys.set_source_status(db, src.id, status, err.message)
            counts[status] += 1
            continue
        rep = await ys.mark_listed(db, src, listing.entries, first_listing=first, local=local)
        for k, v in rep.as_json().items():
            setattr(totals, k, getattr(totals, k) + v)
        known.update(e.youtube_id for e in listing.entries)
        await ys.set_source_status(
            db, src.id, "ok", None, checked=True, folder=ys.default_folder(src, listing.title)
        )
        counts["ok"] += 1
    report.update(counts)
    report.update(totals.as_json())
    queued: list[str] = []
    for stage, backfill in (("youtube_fetch", False), ("youtube_backfill", True)):
        if await ys.wanted_count(db, library_id, backfill=backfill):
            queued.append(stage)
    report["queued"] = queued
    return report


# ------------------------------------------------------------------ the stage


async def run(db: Any, job: Any, ctx: StageContext) -> StageResult:
    library_id: UUID = job.subject.id
    payload: dict[str, Any] = job.payload or {}
    refresh = bool(payload.get("refresh"))
    lang = str(payload.get("lang") or os.environ.get("MAKTABA_YT_LANG") or "ar")

    sources = await _check_sources(
        db, job, ctx, full=bool(payload.get("full_listing")), refresh=refresh
    )

    before = await _snapshot(db, library_id)

    roots = [ctx.local_path(r) for r in await _roots(db, library_id)]
    found = await asyncio.to_thread(discover_folders, roots)
    async with db.transaction():
        for cid, (url, title) in sorted(found.channels.items()):
            await _upsert_discovered_channel(db, library_id, cid, url, title)
        for info in found.playlists:
            await _upsert_sidecar_playlist(db, info)

    channels = await db.fetch(
        "SELECT channel_id, url, status FROM youtube_channels WHERE library_id = $1"
        " ORDER BY channel_id",
        uid(library_id),
    )
    todo = [r for r in channels if refresh or r["status"] != "gone"]
    counts = {"ok": 0, "gone": 0, "error": 0, "skipped_gone": len(channels) - len(todo)}
    log: list[tuple[str, str, dict[str, Any]]] = []

    fetcher: YouTubeFetcher | None = None
    for n, r in enumerate(todo, 1):
        cid, url = str(r["channel_id"]), str(r["url"])
        await ctx.check_cancel()
        await ctx.progress("listing", n - 1, len(todo), cid)
        if fetcher is None:
            try:
                fetcher = _fetcher_factory()
            except ImportError as exc:
                raise StageFailure(
                    "yt_dlp_missing",
                    "yt-dlp is not installed (pip install 'maktaba-pipeline[mediaintel]')",
                    retryable=False,
                ) from exc
        try:
            listing = await asyncio.to_thread(list_channel, fetcher, cid, url, lang=lang)
        except BotCheck as exc:
            await _set_channel_status(db, cid, "bot_check", str(exc))
            raise StageFailure(
                "youtube_bot_check",
                f"Sign in to confirm you're not a bot (YouTube, listing {cid}); "
                "retry later or give yt-dlp a signed-in browser session",
                retryable=False,
            ) from exc
        except ChannelUnavailable as exc:
            await _set_channel_status(db, cid, exc.status, exc.message)
            counts[exc.status] += 1
            log.append(("warn", f"channel {cid}: {exc.status}", {"channel_id": cid}))
            continue
        await _store_listing(db, listing)
        counts["ok"] += 1

    after = await _snapshot(db, library_id)
    changed_ids = changed_youtube_ids(before, after)
    changed_videos: set[UUID] = set()
    if changed_ids:
        # a listing that changed may also let still-unlinked videos link now
        changed_videos = await _linked_videos(db, library_id, sorted(changed_ids))
        changed_videos |= await _unlinked_videos(db, library_id)
    subjects: tuple[tuple[SubjectKind, UUID], ...] = tuple(
        ("video", v) for v in sorted(changed_videos, key=str)
    )
    library_requests = tuple(
        Request(stage, "library", library_id, "youtube items wanted",
                priority=EPIC47_PRIORITIES[stage])
        for stage in sources.get("queued", ())
    )  # fmt: skip
    return StageResult(
        output_hash=after.digest(),
        changed_subjects=subjects,
        requests=library_requests,
        summary={
            "channels": len(channels),
            **counts,
            "videos": len(after.videos),
            "playlists": len(after.playlists),
            "changed_rows": len(changed_ids),
            "youtube_sources": sources,
        },
        log=tuple(log),
    )


IMPL = StageImpl(run=run, input_fingerprint=None, output_fingerprint=None)
