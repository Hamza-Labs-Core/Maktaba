"""The ``dizquetv_show`` stage: one dizqueTV custom show per season.

Ported from ``scripts/mediaintel/dizquetv_shows.py``. The show holds the season's
episodes in order, built from the Plex rating keys cached in ``external_links``
(``target='plex_item'``, written by ``plex_index`` and ``plex_collection``) and the
item records ``plex_index`` saved in ``plex_items``; Plex is asked only for what
the index lacks. A season whose
episodes are not all known to Plex yet finishes with ``waiting_inputs``; the edge
``plex_collection -> dizquetv_show`` runs it again when Plex has caught up.

Safety: only the show this tool created (``external_links``, ``created_by_tool``)
is ever updated or deleted. A show the user made with the same name is left
alone and reported as a conflict.
"""

from __future__ import annotations

import asyncio
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any
from uuid import UUID

from ...engine.changes import ChangeSet, apply_or_review, diff_order
from ...engine.context import StageContext, StageFailure
from ...engine.graph import StageResult, Subject
from ...engine.hashing import canonical_hash
from ...integrations.dizquetv import DizqueError, DizqueTV, program_from_plex
from ...integrations.plex import PlexClient, PlexError, TransportError
from ...text.arabic import fold
from ..links import Link, delete_link, get_link, get_links, put_link
from ..plex_items import details_for
from ..seasons import load_members, load_season

__all__ = [
    "ITEM_TARGET",
    "SHOW_TARGET",
    "DesiredShow",
    "RemoteShow",
    "ShowRemote",
    "ShowsService",
    "apply_show",
    "compute_changes",
    "fingerprint",
    "run_show",
]

SHOW_TARGET = "dizquetv_show"
ITEM_TARGET = "plex_item"


@dataclass(frozen=True, slots=True)
class DesiredShow:
    name: str
    keys: tuple[str, ...]
    delete: bool = False


@dataclass(frozen=True, slots=True)
class RemoteShow:
    id: str
    name: str
    keys: tuple[str, ...]


@dataclass(frozen=True, slots=True)
class ShowRemote:
    ours: RemoteShow | None = None
    #: a show the tool did not create already has this (folded) name
    foreign_name: bool = False


class ShowsService:
    def __init__(
        self,
        dz_factory: Callable[[Mapping[str, str]], DizqueTV],
        plex_factory: Callable[[Mapping[str, str]], PlexClient],
    ) -> None:
        self.dz = dz_factory
        self.plex = plex_factory


def compute_changes(desired: DesiredShow, remote: ShowRemote) -> ChangeSet:
    """Diff the desired show against dizqueTV. Pure; writes nothing."""
    ours = remote.ours
    meta: dict[str, Any] = {"name": desired.name}
    if desired.delete:
        if ours is None:
            return ChangeSet("dizquetv_show")
        return ChangeSet(
            "dizquetv_show",
            removed=ours.keys,
            meta={**meta, "action": "delete", "show": ours.id},
        )
    if remote.foreign_name and ours is None:
        return ChangeSet("dizquetv_show", meta={**meta, "action": "conflict"})
    if ours is None:
        if not desired.keys:
            return ChangeSet("dizquetv_show")
        return ChangeSet("dizquetv_show", added=desired.keys, meta={**meta, "action": "create"})
    added, removed, reordered = diff_order(ours.keys, desired.keys)
    changed: tuple[Any, ...] = ()
    if ours.name != desired.name:
        changed = ({"key": "name", "from": ours.name, "to": desired.name},)
    if not (added or removed or reordered or changed):
        return ChangeSet("dizquetv_show")
    return ChangeSet(
        "dizquetv_show",
        added=added,
        removed=removed,
        changed=changed,
        reordered=desired.keys if reordered else (),
        meta={**meta, "action": "update", "show": ours.id},
    )


def read_remote(dz: DizqueTV, link: Link | None, name: str) -> ShowRemote:
    """Blocking: find the show we own and any foreign show with the same name."""
    shows = dz.get("/api/shows") or []
    ours: RemoteShow | None = None
    own_id = link.external_id if link is not None and link.created_by_tool else None
    if own_id is not None:
        for s in shows:
            if str(s["id"]) == own_id:
                doc = dz.get(f"/api/show/{own_id}")
                keys = tuple(str(p.get("ratingKey")) for p in doc.get("content") or [])
                ours = RemoteShow(own_id, str(s.get("name", "")), keys)
                break
    wanted = fold(name)
    foreign = any(fold(str(s.get("name", ""))) == wanted and str(s["id"]) != own_id for s in shows)
    return ShowRemote(ours, foreign)


def apply_show(
    dz: DizqueTV,
    plex: PlexClient,
    desired: DesiredShow,
    remote: ShowRemote,
    saved: Mapping[str, Mapping[str, Any]] | None = None,
) -> tuple[str | None, int]:
    """Perform the change (blocking); returns ``(show id, programme count)``.

    Programmes are built from ``saved`` (the ``plex_index`` stage's records, by
    rating key) and only the keys it lacks are asked of Plex. The show is read
    back and its ratingKey order verified.
    """
    ours = remote.ours
    if desired.delete:
        if ours is not None:
            dz.call("DELETE", f"/api/show/{ours.id}")
        return None, 0
    server = dz.plex_server_name()
    if server is None:
        raise StageFailure(
            "dizquetv_no_plex_server", "dizqueTV has no Plex server configured", retryable=False
        )
    meta: dict[str, Mapping[str, Any]] = dict(saved or {})
    missing = [k for k in desired.keys if k not in meta]
    if missing:
        meta.update(plex.metadata(missing))
    content: list[dict[str, Any]] = []
    for k in desired.keys:
        m = meta.get(k)
        prog = None if m is None else program_from_plex(m, server, plex.uri, plex.token)
        if prog is not None:
            content.append(prog)
    body = {"name": desired.name, "content": content}
    if ours is None:
        sid = str(dz.call("PUT", "/api/show", body)["id"])
    else:
        sid = ours.id
        dz.call("POST", f"/api/show/{sid}", body)
    got = [str(p.get("ratingKey")) for p in dz.get(f"/api/show/{sid}")["content"]]
    if got != [p["ratingKey"] for p in content]:
        raise StageFailure(
            "readback_mismatch",
            f"{desired.name}: dizqueTV holds {len(got)} items, expected {len(content)}",
        )
    return sid, len(content)


def _wrap(exc: Exception) -> StageFailure:
    down = isinstance(exc, TransportError) or isinstance(exc.__cause__, TransportError)
    kind = "dizquetv_unreachable" if down else "dizquetv_error"
    return StageFailure(kind, str(exc), retryable=True)


async def run_show(db: Any, job: Any, ctx: StageContext, service: ShowsService) -> StageResult:
    season_id: UUID = job.subject.id
    dz = service.dz(ctx.extension_settings("dizquetv"))
    season = await load_season(db, season_id)
    link = await get_link(db, SHOW_TARGET, "season", str(season_id))

    if season is None or season.retired:
        desired = DesiredShow(link.title if link else "", (), delete=True)
        name = desired.name
    else:
        members = await load_members(db, season_id)
        cached = await get_links(db, ITEM_TARGET, "video", [str(m.video_id) for m in members])
        keys = tuple(
            cached[str(m.video_id)].external_id for m in members if str(m.video_id) in cached
        )
        if not members or len(keys) < len(members):
            return StageResult(
                output_hash=canonical_hash({"title": season.title, "keys": list(keys)}),
                outcome="waiting_inputs",
                summary={
                    "title": season.title,
                    "members": len(members),
                    "with_plex_item": len(keys),
                    "reason": "plex_items_missing",
                },
            )
        desired = DesiredShow(season.title, keys)
        name = season.title

    if desired.delete and (link is None or not link.created_by_tool):
        return StageResult(output_hash=canonical_hash({"retired": True}), summary={"retired": True})
    try:
        remote = await asyncio.to_thread(read_remote, dz, link, name)
    except (DizqueError, TransportError) as exc:
        raise _wrap(exc) from exc
    changes = compute_changes(desired, remote)
    output_hash = canonical_hash(
        {"name": desired.name, "keys": list(desired.keys), "delete": desired.delete}
    )
    if changes.meta.get("action") == "conflict":
        return StageResult(
            output_hash=output_hash,
            outcome="waiting_inputs",
            summary={"title": name, "reason": "name_conflict"},
            log=(
                (
                    "warn",
                    f"a dizqueTV show named {name!r} exists that this tool did not create; "
                    "left untouched",
                    {"name": name},
                ),
            ),
        )

    result: dict[str, Any] = {}

    async def apply(cs: ChangeSet) -> None:
        plex_settings = ctx.extension_settings("plex")
        if not desired.delete and (not plex_settings.get("url") or not plex_settings.get("token")):
            raise StageFailure(
                "plex_not_configured",
                "writing dizqueTV programmes needs the Plex settings (url and token)",
                retryable=False,
            )
        plex = service.plex(plex_settings)
        saved = {} if desired.delete else await details_for(db, desired.keys)
        try:
            sid, n = await asyncio.to_thread(apply_show, dz, plex, desired, remote, saved)
        except (DizqueError, PlexError, TransportError) as exc:
            raise _wrap(exc) from exc
        result["id"], result["n"] = sid, n

    status = await apply_or_review(ctx, changes, apply)
    if status == "applied":
        if desired.delete:
            await delete_link(db, SHOW_TARGET, "season", str(season_id))
        else:
            await put_link(
                db,
                Link(
                    SHOW_TARGET,
                    "season",
                    str(season_id),
                    str(result["id"]),
                    desired.name,
                    output_hash,
                    True,
                ),
            )
    elif (
        status == "noop"
        and remote.ours is not None
        and link is not None
        and (link.content_hash != output_hash or link.title != desired.name)
    ):
        await put_link(
            db,
            Link(
                SHOW_TARGET,
                "season",
                str(season_id),
                remote.ours.id,
                desired.name,
                output_hash,
                True,
            ),
        )
    return StageResult(
        output_hash=output_hash,
        outcome="awaiting_review" if status == "review" else None,
        summary={"title": name, "status": status, "changes": changes.to_json()},
    )


async def _plex_output(db: Any, season_id: str) -> str | None:
    row = await db.fetchrow(
        "SELECT output_hash FROM processing_jobs WHERE stage = 'plex_collection'"
        " AND subject_kind = 'season' AND subject_id = $1 AND state = 'done'"
        " AND outcome IN ('changed','unchanged','skipped_unchanged') ORDER BY id DESC LIMIT 1",
        season_id,
    )
    return None if row is None or row["output_hash"] is None else str(row["output_hash"])


async def fingerprint(db: Any, subject: Subject, write_mode: str) -> str | None:
    """Database-only: ordered cached members + Plex collection output + write mode."""
    season = await load_season(db, subject.id)
    if season is None:
        return None
    members = await load_members(db, subject.id)
    cached = await get_links(db, ITEM_TARGET, "video", [str(m.video_id) for m in members])
    keys: Sequence[str | None] = [
        cached[str(m.video_id)].external_id if str(m.video_id) in cached else None for m in members
    ]
    return canonical_hash(
        {
            "keys": list(keys),
            "title": season.title,
            "membership": season.membership_hash,
            "retired": season.retired,
            "plex": await _plex_output(db, str(subject.id)),
            "write_mode": write_mode,
        }
    )
