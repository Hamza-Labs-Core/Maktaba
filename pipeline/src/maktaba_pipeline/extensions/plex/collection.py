"""The ``plex_collection`` stage: one manual Plex collection per season.

Split exactly as plan notes N1 and N1a ask:

* :func:`compute_changes` is pure: desired collection against the current Plex
  state gives a :class:`~maktaba_pipeline.engine.changes.ChangeSet`;
* :func:`apply_collection` performs it (delete before recreate, because Plex
  stores manual-collection membership as a tag named after the collection);
* the handler runs both through :func:`~maktaba_pipeline.engine.changes.apply_or_review`
  and verifies by reading the collection back.

Safety rules kept from the prototype: a collection this tool did not create
(``external_links.created_by_tool``) is never changed or deleted, and a foreign
collection with the same title is left alone (the stage reports a conflict).
"""

from __future__ import annotations

import asyncio
import hashlib
import time
from collections import Counter
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any
from uuid import UUID

from ...engine.changes import ChangeSet, apply_or_review, diff_order
from ...engine.context import StageContext, StageFailure
from ...engine.graph import StageResult, Subject
from ...engine.hashing import canonical_hash
from ...engine.paths import PathMap
from ...integrations.plex import PlexClient, PlexError, PlexItem, PlexSection, TransportError
from ..links import Link, delete_link, get_link, get_links, put_link
from ..plex_items import items_for_videos
from ..seasons import Member, load_members, load_season

__all__ = [
    "COLLECTION_TARGET",
    "ITEM_TARGET",
    "LABEL",
    "ClientFactory",
    "Desired",
    "PlexRemote",
    "RemoteCollection",
    "apply_collection",
    "collection_hash",
    "compute_changes",
    "item_content_hash",
    "section_of",
]

COLLECTION_TARGET = "plex_collection"
ITEM_TARGET = "plex_item"
#: every collection this tool creates carries this Plex label (shown "Maktaba-auto" in Plex)
LABEL = "maktaba-auto"
INDEX_TTL_SEC = 120.0

ClientFactory = Callable[[Mapping[str, str]], PlexClient]


@dataclass(frozen=True, slots=True)
class Desired:
    """The collection a season should be."""

    title: str
    keys: tuple[str, ...]
    section: str
    #: True when the season is retired: our collection should go
    delete: bool = False


@dataclass(frozen=True, slots=True)
class RemoteCollection:
    rating_key: str
    title: str
    children: tuple[str, ...]


@dataclass(frozen=True, slots=True)
class PlexRemote:
    """The slice of Plex state a season's collection depends on."""

    #: the collection this tool created for the season (None when absent in Plex)
    ours: RemoteCollection | None = None
    #: a collection the tool did not create already carries the desired title
    foreign_title: bool = False


def collection_hash(desired: Desired) -> str:
    """What a written collection is: title, ordered keys and the label it carries."""
    return canonical_hash({"title": desired.title, "keys": list(desired.keys), "label": LABEL})


def item_content_hash(section_key: str, file: str) -> str:
    """``<section>:<sha1(file)>``: the cached rating key also remembers its section."""
    return f"{section_key}:{hashlib.sha1(file.encode()).hexdigest()[:16]}"  # noqa: S324


def section_of(link: Link) -> str:
    return link.content_hash.split(":", 1)[0]


def _norm(path: str) -> str:
    return path.replace("\\", "/")


# ------------------------------------------------------------------ pure compute


def compute_changes(desired: Desired, remote: PlexRemote) -> ChangeSet:
    """Diff the desired collection against Plex. Pure; writes nothing."""
    ours = remote.ours
    base_meta: dict[str, Any] = {"title": desired.title, "section": desired.section}
    if desired.delete:
        if ours is None:
            return ChangeSet("plex_collection")
        return ChangeSet(
            "plex_collection",
            removed=tuple(ours.children),
            meta={**base_meta, "action": "delete", "collection": ours.rating_key},
        )
    if remote.foreign_title and (ours is None or ours.title != desired.title):
        return ChangeSet("plex_collection", meta={**base_meta, "action": "conflict"})
    if ours is None:
        if not desired.keys:
            return ChangeSet("plex_collection")
        return ChangeSet(
            "plex_collection",
            added=tuple(desired.keys),
            meta={**base_meta, "action": "create"},
        )
    added, removed, reordered = diff_order(ours.children, desired.keys)
    changed: tuple[Any, ...] = ()
    if ours.title != desired.title:
        changed = ({"key": "title", "from": ours.title, "to": desired.title},)
    if not (added or removed or reordered or changed):
        return ChangeSet("plex_collection")
    return ChangeSet(
        "plex_collection",
        added=added,
        removed=removed,
        changed=changed,
        reordered=tuple(desired.keys) if reordered else (),
        meta={**base_meta, "action": "recreate", "collection": ours.rating_key},
    )


# ------------------------------------------------------------------ apply


def apply_collection(client: PlexClient, desired: Desired, remote: PlexRemote) -> str | None:
    """Perform the change (blocking). Returns the new collection's rating key, if any.

    The old collection is deleted **before** the new one is created (Plex strips
    the membership tag of a same-named collection when it is deleted).
    """
    return apply_collection_report(client, desired, remote)[0]


def apply_collection_report(
    client: PlexClient, desired: Desired, remote: PlexRemote
) -> tuple[str | None, dict[str, str]]:
    """:func:`apply_collection`, returning also the items Plex refused (and why)."""
    ours = remote.ours
    machine = client.machine_identifier() if not desired.delete else ""
    if ours is not None:
        client.delete_metadata(ours.rating_key)
    if desired.delete:
        return None, {}
    new, refused = client.create_collection_report(
        machine, desired.section, desired.title, list(desired.keys)
    )
    client.add_label(desired.section, new, LABEL)
    return new, refused


# ------------------------------------------------------------------ handler


@dataclass(slots=True)
class PlexIndex:
    """Plex's items by (normalised) file path; cached briefly across jobs."""

    exact: dict[str, PlexItem] = field(default_factory=dict)
    folded: dict[str, PlexItem] = field(default_factory=dict)

    def find(self, plex_path: str) -> PlexItem | None:
        p = _norm(plex_path)
        return self.exact.get(p) or self.folded.get(p.casefold())


class PlexService:
    """Everything the stage needs besides the database: client factory, index cache, clock."""

    def __init__(self, client_factory: ClientFactory, clock: Callable[[], float] = time.monotonic):
        self._factory = client_factory
        self._clock = clock
        self._index: dict[str, tuple[float, PlexIndex]] = {}

    def client(self, settings: Mapping[str, str]) -> PlexClient:
        return self._factory(settings)

    def index(self, settings: Mapping[str, str], client: PlexClient) -> PlexIndex:
        """Blocking: list every section's items (cached for a couple of minutes)."""
        key = settings.get("url", "")
        hit = self._index.get(key)
        if hit is not None and self._clock() - hit[0] < INDEX_TTL_SEC:
            return hit[1]
        idx = PlexIndex()
        sections: list[PlexSection] = client.sections()
        for sec in sections:
            for item in client.section_items(sec):
                p = _norm(item.file)
                idx.exact[p] = item
                idx.folded.setdefault(p.casefold(), item)
        self._index[key] = (self._clock(), idx)
        return idx

    def invalidate(self, settings: Mapping[str, str]) -> None:
        self._index.pop(settings.get("url", ""), None)


def path_map_of(settings: Mapping[str, str]) -> PathMap:
    return PathMap.parse(settings.get("path_map"))


async def resolve_members(
    db: Any, service: PlexService, settings: Mapping[str, str], members: Sequence[Member]
) -> dict[UUID, Link]:
    """Plex rating key (as a cached ``plex_item`` link) for every member that has one.

    Members without a cached key are looked up in ``plex_items`` (the
    ``plex_index`` stage's saved listing) and only then matched against Plex by
    file path (the
    Plex-side view of the database path, via the extension's ``path_map``); the
    match is cached in ``external_links`` so later runs never touch the network.
    """
    cached = await get_links(db, ITEM_TARGET, "video", [str(m.video_id) for m in members])
    found: dict[UUID, Link] = {
        m.video_id: cached[str(m.video_id)] for m in members if str(m.video_id) in cached
    }
    missing = [m for m in members if m.video_id not in found]
    if not missing:
        return found
    # the plex_index stage's saved listing, before any network call
    indexed = await items_for_videos(db, [str(m.video_id) for m in missing])
    for m in missing:
        it = indexed.get(str(m.video_id))
        if it is None or it.file is None:
            continue
        link = Link(
            ITEM_TARGET,
            "video",
            str(m.video_id),
            it.rating_key,
            m.title,
            item_content_hash(it.section_key, it.file),
            False,
        )
        await put_link(db, link)
        found[m.video_id] = link
    missing = [m for m in missing if m.video_id not in found]
    if not missing:
        return found
    pm = path_map_of(settings)
    client = service.client(settings)
    index = await asyncio.to_thread(service.index, settings, client)
    for m in missing:
        item = index.find(pm.to_local(m.path))
        if item is None:
            continue
        link = Link(
            ITEM_TARGET,
            "video",
            str(m.video_id),
            item.rating_key,
            item.title,
            item_content_hash(item.section_key, item.file),
            False,
        )
        await put_link(db, link)
        found[m.video_id] = link
    return found


def _read_remote(client: PlexClient, section: str, link: Link | None, title: str) -> PlexRemote:
    cols = client.collections(section)
    ours_col = None
    if link is not None and link.created_by_tool:
        ours_col = next((c for c in cols if c.rating_key == link.external_id), None)
    foreign = any(
        c.title == title and (ours_col is None or c.rating_key != ours_col.rating_key) for c in cols
    )
    ours = None
    if ours_col is not None:
        ours = RemoteCollection(
            ours_col.rating_key,
            ours_col.title,
            tuple(client.collection_children(ours_col.rating_key)),
        )
    return PlexRemote(ours, foreign)


def _wrap(exc: Exception) -> StageFailure:
    kind = "plex_unreachable" if isinstance(exc, TransportError) else "plex_error"
    return StageFailure(kind, str(exc), retryable=True)


async def run_collection(db: Any, job: Any, ctx: StageContext, service: PlexService) -> StageResult:
    season_id: UUID = job.subject.id
    settings = ctx.extension_settings("plex")
    season = await load_season(db, season_id)
    link = await get_link(db, COLLECTION_TARGET, "season", str(season_id))
    if season is None or season.retired:
        return await _retire(db, ctx, service, settings, season_id, link)

    members = await load_members(db, season_id)
    try:
        resolved = await resolve_members(db, service, settings, members)
    except (PlexError, TransportError) as exc:
        raise _wrap(exc) from exc
    keyed = [(m, resolved[m.video_id]) for m in members if m.video_id in resolved]
    summary: dict[str, Any] = {
        "title": season.title,
        "members": len(members),
        "resolved": len(keyed),
        "missing": len(members) - len(keyed),
    }
    if not keyed:
        return StageResult(
            output_hash=canonical_hash({"title": season.title, "keys": []}),
            outcome="waiting_inputs",
            summary={**summary, "reason": "no_plex_items"},
        )
    # a collection lives in one library section: take the section most members are in
    sections = Counter(section_of(lk) for _, lk in keyed)
    section = sorted(sections.items(), key=lambda kv: (-kv[1], kv[0]))[0][0]
    keys = tuple(lk.external_id for _, lk in keyed if section_of(lk) == section)
    summary["other_section"] = len(keyed) - len(keys)
    desired = Desired(season.title, keys, section)
    output_hash = collection_hash(desired)

    client = service.client(settings)
    try:
        remote = await asyncio.to_thread(_read_remote, client, section, link, desired.title)
    except (PlexError, TransportError) as exc:
        raise _wrap(exc) from exc
    changes = compute_changes(desired, remote)
    if changes.meta.get("action") == "conflict":
        return StageResult(
            output_hash=output_hash,
            outcome="waiting_inputs",
            summary={**summary, "reason": "title_conflict"},
            log=(
                (
                    "warn",
                    f"a Plex collection titled {desired.title!r} exists that this tool did not "
                    "create; left untouched",
                    {"title": desired.title},
                ),
            ),
        )

    async def apply(cs: ChangeSet) -> None:
        await _apply(db, ctx, service, settings, client, season_id, desired, remote, link)

    try:
        status = await apply_or_review(ctx, changes, apply)
    except (PlexError, TransportError) as exc:
        raise _wrap(exc) from exc
    if (
        status == "noop"
        and link is not None
        and remote.ours is not None
        and (link.content_hash != output_hash or link.title != desired.title)
    ):
        if link.content_hash != output_hash:  # e.g. made before collections were labelled
            try:
                await asyncio.to_thread(client.add_label, section, remote.ours.rating_key, LABEL)
            except (PlexError, TransportError) as exc:
                raise _wrap(exc) from exc
        # keep the link's content hash current even though Plex needed nothing
        await put_link(
            db, Link(*_link_fields(season_id, remote.ours.rating_key, desired, output_hash))
        )
    summary["status"] = status
    summary["changes"] = changes.to_json()
    return StageResult(
        output_hash=output_hash,
        outcome="awaiting_review" if status == "review" else None,
        summary=summary,
    )


def _link_fields(
    season_id: UUID, collection_key: str, desired: Desired, content_hash: str
) -> tuple[str, str, str, str, str, str, bool]:
    return (
        COLLECTION_TARGET,
        "season",
        str(season_id),
        collection_key,
        desired.title,
        content_hash,
        True,
    )


async def _apply(
    db: Any,
    ctx: StageContext,
    service: PlexService,
    settings: Mapping[str, str],
    client: PlexClient,
    season_id: UUID,
    desired: Desired,
    remote: PlexRemote,
    link: Link | None,
) -> None:
    if remote.ours is not None:
        # the old collection goes first; forget the link until the new one is verified
        await delete_link(db, COLLECTION_TARGET, "season", str(season_id))
    new_key = await asyncio.to_thread(apply_collection, client, desired, remote)
    if new_key is None:  # delete action
        return
    content_hash = collection_hash(desired)
    await put_link(db, Link(*_link_fields(season_id, new_key, desired, content_hash)))
    got = await asyncio.to_thread(client.collection_children, new_key)
    if tuple(got) != desired.keys:
        # cached keys may be stale (items re-added in Plex): resolve them again next time
        service.invalidate(settings)
        await db.execute(
            "DELETE FROM external_links WHERE target = $1 AND subject_kind = 'video'"
            " AND subject_id IN (SELECT CAST(e.video_id AS TEXT) FROM series_episodes e"
            "  JOIN series_seasons ss ON ss.series_id = e.series_id"
            "   AND ss.season = COALESCE(e.season_override, e.season) WHERE ss.id = $2)",
            ITEM_TARGET,
            str(season_id),
        )
        raise StageFailure(
            "readback_mismatch",
            f"{desired.title}: Plex holds {len(got)} items (expected {len(desired.keys)}), "
            f"order ok={tuple(got) == desired.keys}",
        )


async def _retire(
    db: Any,
    ctx: StageContext,
    service: PlexService,
    settings: Mapping[str, str],
    season_id: UUID,
    link: Link | None,
) -> StageResult:
    """The season is gone: remove the collection this tool made for it (never a foreign one)."""
    out = StageResult(output_hash=canonical_hash({"retired": True}), summary={"retired": True})
    if link is None or not link.created_by_tool:
        return out
    client = service.client(settings)
    desired = Desired(link.title, (), "", delete=True)
    try:
        sec = await asyncio.to_thread(client.item_section, link.external_id)
        remote = PlexRemote()
        if sec is not None:
            remote = await asyncio.to_thread(_read_remote, client, sec, link, link.title)
    except (PlexError, TransportError) as exc:
        raise _wrap(exc) from exc
    changes = compute_changes(desired, remote)

    async def apply(cs: ChangeSet) -> None:
        await asyncio.to_thread(apply_collection, client, desired, remote)

    try:
        status = await apply_or_review(ctx, changes, apply)
    except (PlexError, TransportError) as exc:
        raise _wrap(exc) from exc
    if status != "review":
        await delete_link(db, COLLECTION_TARGET, "season", str(season_id))
    return StageResult(
        output_hash=out.output_hash,
        outcome="awaiting_review" if status == "review" else None,
        summary={"retired": True, "status": status, "changes": changes.to_json()},
    )


async def fingerprint(db: Any, subject: Subject, write_mode: str) -> str | None:
    """Database-only input fingerprint (never touches the network)."""
    season = await load_season(db, subject.id)
    if season is None:
        return None
    members = await load_members(db, subject.id)
    # Season membership and order, not the cached rating keys: the first run caches them as a
    # side effect, and a fingerprint that moved with that cache would make the stage run twice.
    order = [str(m.video_id) for m in members]
    return canonical_hash(
        {
            "members": order,
            "title": season.title,
            "membership": season.membership_hash,
            "retired": season.retired,
            "write_mode": write_mode,
        }
    )
