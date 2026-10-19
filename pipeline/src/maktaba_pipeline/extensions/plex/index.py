"""The ``plex_index`` stage: save Plex's record of every item of a library.

One listing per Plex section that holds the library (``/library/sections/K/all``
carries every field later stages need), saved in ``plex_items`` (slot 0092).
Items Plex dropped are removed with their section's old rows. Every video the
listing reaches through the extension's ``path_map`` gets its ``plex_item`` link
(``external_links``) and, when the scan has not measured it, Plex's length.

The seasons whose members gained, changed or lost a Plex item are returned as
changed subjects, so the edges ``plex_index -> plex_collection`` and
``plex_index -> dizquetv_show`` re-run exactly those seasons.
"""

from __future__ import annotations

import asyncio
from collections.abc import Sequence
from typing import Any
from uuid import UUID

from ...engine.context import StageContext, StageFailure
from ...engine.graph import StageResult, SubjectKind
from ...engine.hashing import canonical_hash
from ...engine.paths import PathMap
from ...engine.sql import loads, uid
from ...integrations.plex import PlexClient, PlexError, PlexSection, TransportError
from ..links import Link, delete_link, get_links, put_link
from ..plex_items import replace_section
from .collection import ITEM_TARGET, PlexService, item_content_hash, path_map_of

__all__ = ["run_index", "sections_for"]

_CHUNK = 400


def _norm(path: str) -> str:
    return path.replace("\\", "/")


def sections_for(sections: Sequence[PlexSection], plex_paths: Sequence[str]) -> list[PlexSection]:
    """The sections whose locations hold any of these (Plex-side) paths. When none
    does (locations outside the path map, or not reported), every section: an
    over-wide listing costs time, a missed one would lose the library."""
    paths = [_norm(p).casefold() for p in plex_paths if p]
    hit = []
    for sec in sections:
        locs = [_norm(loc).rstrip("/").casefold() + "/" for loc in sec.locations]
        if any(
            p.startswith(loc) or loc.startswith(p.rstrip("/") + "/") for loc in locs for p in paths
        ):
            hit.append(sec)
    return hit or list(sections)


def _list(
    client: PlexClient, chosen: Sequence[PlexSection]
) -> tuple[str, list[list[dict[str, Any]]]]:
    machine = client.machine_identifier()
    return machine, [client.section_listing(sec) for sec in chosen]


def _wrap(exc: Exception) -> StageFailure:
    kind = "plex_unreachable" if isinstance(exc, TransportError) else "plex_error"
    return StageFailure(kind, f"plex: {exc}", retryable=True)


async def _library(db: Any, library_id: UUID) -> tuple[str, list[str]] | None:
    row = await db.fetchrow("SELECT name, roots FROM libraries WHERE id = $1", uid(library_id))
    if row is None:
        return None
    roots = row["roots"]
    if isinstance(roots, str):
        roots = loads(roots) or []
    return str(row["name"]), [str(r) for r in roots or []]


async def _videos(db: Any, library_id: UUID) -> list[tuple[str, str, Any]]:
    rows = await db.fetch(
        "SELECT id, path, duration_sec FROM videos WHERE library_id = $1 AND deleted_at IS NULL",
        uid(library_id),
    )
    return [(str(r["id"]), str(r["path"]), r["duration_sec"]) for r in rows]


async def _seasons_of(db: Any, video_ids: Sequence[str]) -> list[UUID]:
    out: set[UUID] = set()
    ids = list(dict.fromkeys(video_ids))
    for i in range(0, len(ids), _CHUNK):
        chunk = ids[i : i + _CHUNK]
        marks = ",".join(f"${n + 1}" for n in range(len(chunk)))
        rows = await db.fetch(
            "SELECT DISTINCT ss.id AS id FROM series_episodes e"
            " JOIN series_seasons ss ON ss.series_id = e.series_id"
            "  AND ss.season = COALESCE(e.season_override, e.season)"
            f" WHERE e.video_id IN ({marks})",  # noqa: S608
            *chunk,
        )
        out.update(UUID(str(r["id"])) for r in rows)
    return sorted(out)


async def run_index(db: Any, job: Any, ctx: StageContext, service: PlexService) -> StageResult:
    library_id: UUID = job.subject.id
    lib = await _library(db, library_id)
    if lib is None:
        return StageResult(output_hash=canonical_hash({"library": None}), summary={"gone": True})
    name, roots = lib
    settings = ctx.extension_settings("plex")
    pm: PathMap = path_map_of(settings)
    videos = await _videos(db, library_id)
    by_path: dict[str, str] = {}
    folded: dict[str, str] = {}
    for vid, path, _ in videos:
        p = _norm(pm.rewrite(path))
        by_path[p] = vid
        folded.setdefault(p.casefold(), vid)

    client = service.client(settings)
    await ctx.progress("list", 0, 1, "reading Plex libraries", steps=["list", "save"], force=True)
    try:
        sections = await asyncio.to_thread(client.sections)
        probe = [pm.rewrite(r) for r in roots] + [p.rsplit("/", 1)[0] for p in list(by_path)[:200]]
        chosen = sections_for(sections, probe)
        machine, listings = await asyncio.to_thread(_list, client, chosen)
    except (PlexError, TransportError) as exc:
        raise _wrap(exc) from exc
    service.invalidate(settings)  # the in-memory path index is older than this listing

    # video -> (rating key, section, file, length ms, title)
    found: dict[str, tuple[str, str, str, int | None, str]] = {}
    items_total = 0
    for n, (sec, items) in enumerate(zip(chosen, listings, strict=True), 1):
        library_of: dict[str, str | None] = {}
        video_of: dict[str, str | None] = {}
        for m in items:
            rk = str(m["ratingKey"])
            try:
                file = str(m["Media"][0]["Part"][0]["file"])
            except (KeyError, IndexError, TypeError):
                continue
            p = _norm(file)
            hit = by_path.get(p) or folded.get(p.casefold())
            if hit is None:
                continue
            library_of[rk] = str(library_id)
            video_of[rk] = hit
            ms = int(m["duration"]) if m.get("duration") else None
            found[hit] = (rk, sec.key, file, ms, str(m.get("title", "")))
        await replace_section(
            db,
            server=machine,
            section_key=sec.key,
            section_title=sec.title,
            items=items,
            library_of=library_of,
            video_of=video_of,
        )
        items_total += len(items)
        await ctx.progress("save", n, len(chosen), f"{sec.title}: {len(items):,} items")

    # plex_item links: new, changed (re-added in Plex) and gone
    current = await get_links(db, ITEM_TARGET, "video", [v for v, _, _ in videos])
    changed: list[str] = []
    for vid, (rk, section, file, _ms, title) in found.items():
        content = item_content_hash(section, file)
        old = current.get(vid)
        if old is None or old.external_id != rk or old.content_hash != content:
            await put_link(db, Link(ITEM_TARGET, "video", vid, rk, title, content, False))
            changed.append(vid)
    gone = [vid for vid in current if vid not in found]
    for vid in gone:
        await delete_link(db, ITEM_TARGET, "video", vid)
    # Plex already measured the files the scan has not
    lengths = 0
    for vid, _path, dur in videos:
        got = found.get(vid)
        length_ms = got[3] if got else None
        if dur is None and length_ms:
            await db.execute(
                "UPDATE videos SET duration_sec = $2 WHERE id = $1 AND duration_sec IS NULL",
                vid,
                length_ms / 1000,
            )
            lengths += 1

    seasons = await _seasons_of(db, changed + gone)
    subjects: tuple[tuple[SubjectKind, UUID], ...] = tuple(("season", s) for s in seasons)
    summary: dict[str, Any] = {
        "library": name,
        "sections": [s.title for s in chosen],
        "items": items_total,
        "videos": len(videos),
        "matched": len(found),
        "linked": len(changed),
        "unlinked": len(gone),
        "lengths": lengths,
        "seasons_changed": len(seasons),
    }
    output = canonical_hash(sorted((v, t[0], t[2]) for v, t in found.items()))
    return StageResult(
        output_hash=output,
        changed_subjects=subjects,
        summary=summary,
    )
