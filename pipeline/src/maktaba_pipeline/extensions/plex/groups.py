"""The ``plex_groups`` stage: a library's topic and clip collections in Plex.

Ported from mediaintel's ``export_plex`` (topics, clip groups, label, retire pass):

* a topic collection ("ملف") is ``<library> - ملف - <name>``; a clip group is
  ``<library> - مقاطع - <name>`` (filler) or ``<library> - منفردات - <name>``
  (stand-alone videos), the title ``clip_groups`` stored;
* the **same safety rules** as season collections (:mod:`.collection`): only a
  collection this tool created is changed or deleted, a foreign one with the same title
  is a reported conflict, the old collection is deleted before its replacement is
  created, and every write is read back;
* every collection created carries the ``maktaba-auto`` label;
* **retire pass:** a topic or group collection this tool made whose topic / group no
  longer exists is deleted once the library's current collections hold at least 80 %
  of its items (a group renamed or split); otherwise it is kept and reported.

Being in a topic or clip collection is never a placement: the season collections do not
read these tables. Links: ``external_links`` targets ``plex_topic`` (subject kind
``topic``) and ``plex_clip_group`` (``clip_group``); ``content_hash`` is
``<library id>:<collection hash>`` so the retire pass finds a library's own.
"""

from __future__ import annotations

import asyncio
from collections import Counter
from typing import Any
from uuid import UUID

from ...engine.changes import apply_or_review
from ...engine.context import StageContext, StageFailure
from ...engine.graph import StageResult, Subject
from ...engine.hashing import canonical_hash
from ...engine.sql import uid
from ...integrations.plex import PlexClient, PlexError, TransportError
from ..links import Link, delete_link, get_link, put_link
from ..seasons import Member
from .collection import (
    LABEL,
    Desired,
    PlexRemote,
    PlexService,
    _read_remote,
    _wrap,
    apply_collection,
    apply_collection_report,
    collection_hash,
    compute_changes,
    resolve_members,
    section_of,
)

__all__ = ["CLIP_TARGET", "RETIRE_SHARE", "TOPIC_TARGET", "fingerprint", "planned", "run_groups"]

TOPIC_TARGET = "plex_topic"
CLIP_TARGET = "plex_clip_group"
RETIRE_SHARE = 0.8


async def _members(db: Any, table: str, key: str, owner: str) -> list[Member]:
    rows = await db.fetch(
        f"SELECT v.id AS id, v.path AS path, COALESCE(v.title, v.filename) AS title"  # noqa: S608
        f" FROM {table} t JOIN videos v ON v.id = t.video_id"
        f" WHERE t.{key} = $1 AND v.deleted_at IS NULL ORDER BY t.position",
        owner,
    )
    return [Member(UUID(str(r["id"])), str(r["path"]), str(r["title"])) for r in rows]


async def planned(db: Any, library_id: UUID) -> list[tuple[str, str, str, list[Member]]]:
    """``("target|kind|id", title, subject id, members)`` of the library's topics, then
    its clip groups."""
    lib = await db.fetchrow("SELECT name FROM libraries WHERE id = $1", uid(library_id))
    name = "" if lib is None else str(lib["name"])
    out: list[tuple[str, str, str, list[Member]]] = []
    for r in await db.fetch(
        "SELECT id, name FROM topic_collections WHERE library_id = $1 ORDER BY name, id",
        uid(library_id),
    ):
        tid = str(r["id"])
        out.append(
            (
                f"{TOPIC_TARGET}|topic|{tid}",
                f"{name} - ملف - {r['name']}",
                tid,
                await _members(db, "topic_items", "topic_id", tid),
            )
        )
    for r in await db.fetch(
        "SELECT id, title FROM clip_groups WHERE library_id = $1 ORDER BY kind, title, id",
        uid(library_id),
    ):
        gid = str(r["id"])
        out.append(
            (
                f"{CLIP_TARGET}|clip_group|{gid}",
                str(r["title"]),
                gid,
                await _members(db, "clip_group_items", "group_id", gid),
            )
        )
    return out


async def _our_links(db: Any, library_id: UUID) -> list[Link]:
    rows = await db.fetch(
        "SELECT target, subject_kind, subject_id, external_id, title, content_hash,"
        " created_by_tool FROM external_links WHERE target IN ($1, $2)"
        " AND content_hash LIKE $3",
        TOPIC_TARGET,
        CLIP_TARGET,
        f"{library_id}:%",
    )
    return [
        Link(
            str(r["target"]),
            str(r["subject_kind"]),
            str(r["subject_id"]),
            str(r["external_id"]),
            str(r["title"]),
            str(r["content_hash"]),
            bool(r["created_by_tool"]),
        )
        for r in rows
    ]


async def _season_keys(db: Any, library_id: UUID) -> set[str]:
    """Rating keys of the library's season collections' members (for the retire share)."""
    rows = await db.fetch(
        "SELECT l.external_id AS rk FROM external_links l"
        " JOIN series_episodes e ON CAST(e.video_id AS TEXT) = l.subject_id"
        " JOIN series s ON s.id = e.series_id"
        " WHERE l.target = 'plex_item' AND l.subject_kind = 'video' AND s.library_id = $1",
        uid(library_id),
    )
    return {str(r["rk"]) for r in rows}


async def fingerprint(db: Any, subject: Subject, write_mode: str) -> str | None:
    """Database only: every topic / group (id, title, members) plus the write mode."""
    rows = await planned(db, subject.id)
    return canonical_hash(
        {
            "objects": [[k, t, [str(m.video_id) for m in ms]] for k, t, _, ms in rows],
            "write_mode": write_mode,
        }
    )


async def _write_one(
    db: Any,
    ctx: StageContext,
    client: PlexClient,
    library_id: UUID,
    target: str,
    kind: str,
    sid: str,
    desired: Desired,
    refused: dict[str, str] | None = None,
) -> str:
    """Create, update or leave one collection; returns the status. Items Plex refuses
    stay out, are logged and added to ``refused``; they never fail the stage."""
    link = await get_link(db, target, kind, sid)
    remote: PlexRemote = await asyncio.to_thread(
        _read_remote, client, desired.section, link, desired.title
    )
    changes = compute_changes(desired, remote)
    if changes.meta.get("action") == "conflict":
        await ctx.log(
            "warn",
            f"a Plex collection titled {desired.title!r} exists that this tool did not create;"
            " left untouched",
            {"title": desired.title},
        )
        return "conflict"
    content = f"{library_id}:{collection_hash(desired)}"

    async def apply(_cs: Any) -> None:
        if remote.ours is not None:
            await delete_link(db, target, kind, sid)
        new, said = await asyncio.to_thread(apply_collection_report, client, desired, remote)
        if new is None:
            return
        await put_link(db, Link(target, kind, sid, new, desired.title, content, True))
        for k, why in said.items():
            await ctx.log("warn", f"{desired.title}: Plex refused item {k}: {why}", {"item": k})
        if refused is not None:
            refused.update(said)
        placed = tuple(k for k in desired.keys if k not in said)
        got = await asyncio.to_thread(client.collection_children, new)
        if tuple(got) != placed:
            raise StageFailure(
                "readback_mismatch",
                f"{desired.title}: Plex holds {len(got)} items (expected {len(placed)})",
            )

    status = await apply_or_review(ctx, changes, apply)
    # unchanged, but written before collections were labelled: label it once
    if (
        status == "noop"
        and link is not None
        and remote.ours is not None
        and link.content_hash != content
    ):
        await asyncio.to_thread(client.add_label, desired.section, remote.ours.rating_key, LABEL)
        await put_link(
            db, Link(target, kind, sid, remote.ours.rating_key, desired.title, content, True)
        )
    return status


async def run_groups(db: Any, job: Any, ctx: StageContext, service: PlexService) -> StageResult:
    library_id: UUID = job.subject.id
    settings = ctx.extension_settings("plex")
    objects = await planned(db, library_id)
    client = service.client(settings)
    counts: Counter[str] = Counter()
    covered: set[str] = set()
    live: set[tuple[str, str]] = set()
    written: list[list[Any]] = []
    try:
        for n, (key, title, sid, members) in enumerate(objects, 1):
            target, kind, _ = key.split("|", 2)
            resolved = await resolve_members(db, service, settings, members)
            keyed = [resolved[m.video_id] for m in members if m.video_id in resolved]
            if not keyed:
                live.add((target, sid))
                counts["waiting_plex"] += 1
                continue
            # a library can be two Plex sections of one name: each section's members are a
            # collection there (the biggest keeps the object's id, the others take
            # "<id>@<section>"), never dropped
            sections = Counter(section_of(lk) for lk in keyed)
            for i, (section, _n) in enumerate(
                sorted(sections.items(), key=lambda kv: (-kv[1], kv[0]))
            ):
                sid_s = sid if i == 0 else f"{sid}@{section}"
                live.add((target, sid_s))
                keys = tuple(
                    dict.fromkeys(lk.external_id for lk in keyed if section_of(lk) == section)
                )
                desired = Desired(title, keys, section)
                refused: dict[str, str] = {}
                try:
                    status = await _write_one(
                        db, ctx, client, library_id, target, kind, sid_s, desired, refused
                    )
                except PlexError as exc:  # this collection only; the others go on
                    if exc.status != 400:
                        raise
                    await ctx.log("warn", f"{title}: not written, Plex refused it: {exc}")
                    counts["refused"] += 1
                    continue
                counts[status] += 1
                if refused:
                    counts["items_refused"] += len(refused)
                # only what Plex holds covers anything: an older collection is never
                # retired for an item that did not make it into the new one
                kept_keys = [k for k in keys if k not in refused]
                covered.update(kept_keys)
                written.append([key if i == 0 else f"{key}@{section}", title, kept_keys])
            await ctx.progress("write", n, len(objects), title)
        retired, kept = await _retire(db, ctx, client, library_id, live, covered)
    except (PlexError, TransportError) as exc:
        raise _wrap(exc) from exc
    return StageResult(
        output_hash=canonical_hash(written),
        summary={
            "objects": len(objects),
            "topics": sum(1 for o in objects if o[0].startswith(TOPIC_TARGET)),
            "groups": sum(1 for o in objects if o[0].startswith(CLIP_TARGET)),
            "status": dict(counts),
            "retired": retired,
            "kept_old": kept,
        },
    )


async def _retire(
    db: Any,
    ctx: StageContext,
    client: PlexClient,
    library_id: UUID,
    live: set[tuple[str, str]],
    covered: set[str],
) -> tuple[list[str], list[str]]:
    """Delete our collections whose topic / group is gone, when the library's current
    collections hold (nearly) all their items; keep and report the others."""
    stale = [
        lk for lk in await _our_links(db, library_id) if (lk.target, lk.subject_id) not in live
    ]
    if not stale:
        return [], []
    covered = covered | await _season_keys(db, library_id)
    retired: list[str] = []
    kept: list[str] = []
    for lk in stale:
        if not lk.created_by_tool:
            continue
        sec = await asyncio.to_thread(client.item_section, lk.external_id)
        if sec is None:  # gone from Plex already
            await delete_link(db, lk.target, lk.subject_kind, lk.subject_id)
            continue
        kids = set(await asyncio.to_thread(client.collection_children, lk.external_id))
        if kids and len(kids & covered) < RETIRE_SHARE * len(kids):
            kept.append(lk.title)
            await ctx.log(
                "warn",
                f"kept {lk.title!r}: only {len(kids & covered)} of {len(kids)} items are in the"
                " library's current collections",
                {"title": lk.title},
            )
            continue
        remote = await asyncio.to_thread(_read_remote, client, sec, lk, lk.title)
        desired = Desired(lk.title, (), sec, delete=True)
        changes = compute_changes(desired, remote)

        async def apply(_cs: Any, d: Desired = desired, r: PlexRemote = remote) -> None:
            await asyncio.to_thread(apply_collection, client, d, r)

        status = await apply_or_review(ctx, changes, apply)
        if status != "review":
            await delete_link(db, lk.target, lk.subject_kind, lk.subject_id)
            retired.append(lk.title)
    return retired, kept
