"""The ``plex_collections_import`` stage: Plex's own collections, read into Maktaba (story 56.07).

A home that used Plex before Maktaba has collections people made there by hand. After
``plex_index`` (which maps each Plex rating key to a Maktaba video in ``plex_items``),
this stage reads every collection of the library's Plex sections and its children, in
Plex's order (its custom order when one is set: the children listing follows it), and
keeps one Maktaba collection per Plex collection (``source_kind`` ``plex``, ``source_ref``
``<server>:<rating key>``) with the children that are Maktaba videos:

* a collection Maktaba wrote (``external_links`` ``plex_collection``, ``plex_topic``,
  ``plex_clip_group``, or the ``maktaba-auto`` label) is skipped, so nothing loops;
* children with no Maktaba video are counted (``collections.auto_rule``: ``missing`` and
  their rating keys), not imported; a collection with fewer than :data:`MIN_MAPPED`
  videos is skipped;
* a smart Plex collection is read as its members of the moment, on every run;
* a collection gone from Plex is deleted, an admin's choices on the others are kept
  (:mod:`maktaba_pipeline.engine.sourced_collections`).

Every Plex read happens before any write: Plex unreachable fails the job with the
dependency class (it parks and retries) and changes nothing. The token never reaches a
log line or an error (:func:`_scrub`).
"""

from __future__ import annotations

import asyncio
from typing import Any
from uuid import UUID

from ...engine.context import StageContext, StageFailure
from ...engine.graph import StageResult
from ...engine.hashing import canonical_hash
from ...engine.sourced_collections import Desired, sync_sourced
from ...engine.sql import uid
from ...integrations.plex import PlexClient, PlexError, TransportError
from .collection import LABEL, PlexService

__all__ = ["MIN_MAPPED", "OWN_TARGETS", "plan_plex_collections", "run_collections_import"]

#: a Plex collection needs this many Maktaba videos to be imported
MIN_MAPPED = 2
#: external_links targets of the collections Maktaba writes to Plex
OWN_TARGETS = ("plex_collection", "plex_topic", "plex_clip_group")
#: the missing rating keys kept per collection
MAX_MISSING_KEYS = 200


def _scrub(text: str, token: str) -> str:
    return text.replace(token, "***") if token else text


def _wrap(exc: Exception, token: str) -> StageFailure:
    kind = "plex_unreachable" if isinstance(exc, TransportError) else "plex_error"
    return StageFailure(kind, _scrub(str(exc), token), retryable=True)


def _read(client: PlexClient, sections: list[str], own: set[str]) -> tuple[str, list[Any]]:
    """``(server, [(section, collection, children)])``: every Plex read the stage makes."""
    machine = client.machine_identifier()
    out: list[Any] = []
    for sec in sections:
        for c in client.collections(sec):
            if c.rating_key in own or LABEL in c.labels:
                continue
            out.append((sec, c, client.collection_children(c.rating_key)))
    return machine, out


async def plan_plex_collections(
    db: Any, library_id: UUID, server: str, listed: list[Any]
) -> tuple[list[Desired], int]:
    """The collections to keep, and how many children are not Maktaba videos."""
    rows = await db.fetch(
        "SELECT p.rating_key AS rk, p.video_id AS vid FROM plex_items p"
        " JOIN videos v ON v.id = p.video_id"
        " WHERE p.server = $1 AND p.library_id = $2 AND v.deleted_at IS NULL",
        server,
        uid(library_id),
    )
    video_of = {str(r["rk"]): str(r["vid"]) for r in rows}
    out: list[Desired] = []
    missing_total = 0
    for _sec, c, children in listed:
        vids = [video_of[k] for k in children if k in video_of]
        missing = [k for k in children if k not in video_of]
        if len(dict.fromkeys(vids)) < MIN_MAPPED:
            continue
        missing_total += len(missing)
        out.append(
            Desired(
                "plex",
                f"{server}:{c.rating_key}",
                c.title or c.rating_key,
                tuple(dict.fromkeys(vids)),
                description=getattr(c, "summary", "") or None,
                rule={
                    "smart": bool(c.smart),
                    "missing": len(missing),
                    "missing_keys": missing[:MAX_MISSING_KEYS],
                },
            )
        )
    return out, missing_total


async def run_collections_import(
    db: Any, job: Any, ctx: StageContext, service: PlexService
) -> StageResult:
    library_id: UUID = job.subject.id
    settings = ctx.extension_settings("plex")
    token = str(settings.get("token") or "")
    sections = [
        str(r["section_key"])
        for r in await db.fetch(
            "SELECT DISTINCT section_key FROM plex_items WHERE library_id = $1"
            " ORDER BY section_key",
            uid(library_id),
        )
    ]
    if not sections:
        # the library is in no Plex section plex_index found: nothing to read, and the
        # collections already imported stay until a listing says otherwise
        return StageResult(
            output_hash=canonical_hash({"sections": []}),
            outcome="unchanged",
            summary={"sections": 0},
        )
    own = {
        str(r["external_id"])
        for r in await db.fetch(
            "SELECT external_id FROM external_links WHERE target IN ($1, $2, $3)", *OWN_TARGETS
        )
    }
    client = service.client(settings)
    try:
        server, listed = await asyncio.to_thread(_read, client, sections, own)
    except (PlexError, TransportError) as exc:
        raise _wrap(exc, token) from None
    plan, missing = await plan_plex_collections(db, library_id, server, listed)
    stats = await sync_sourced(db, library_id, ("plex",), plan)
    return StageResult(
        output_hash=canonical_hash(sorted([d.source_ref, d.membership_hash()] for d in plan)),
        outcome="changed" if stats.writes else "unchanged",
        summary={
            "sections": len(sections),
            "listed": len(listed),
            "imported": len(plan),
            "missing_videos": missing,
            **stats.as_dict(),
        },
    )
