"""Collections a source keeps in step (Epic 56, stories 56.06 and 56.07; slot 0187).

A YouTube playlist, a YouTube channel or a Plex collection becomes a ``collections`` row
with ``source_kind`` and ``source_ref``: the stage that reads the source writes its members
in the source's order (``hand_ordered``, so the API lists them by position), and its name,
and nothing else. An admin's choices on such a collection (``name_override``,
``hidden_at``, ``show_on_library``, ``show_on_home``, ``sort_title``) are separate columns the
stages never write, so they outlive every run (README §11, decision 4).

:func:`sync_sourced` makes a library's sourced collections of some kinds match what the
source says now: an unchanged collection (the same ``membership_hash``) writes nothing; a
changed one has its members replaced in one transaction; one the source no longer has is
deleted (its admin's flags with it).
"""

from __future__ import annotations

import uuid
from collections.abc import Sequence
from dataclasses import dataclass, field
from typing import Any
from uuid import UUID

from .hashing import canonical_hash
from .sql import dumps, is_pg, now, to_ts, uid

__all__ = ["Desired", "SyncStats", "sync_sourced"]


@dataclass(frozen=True, slots=True)
class Desired:
    """One collection as its source has it now."""

    source_kind: str  # youtube_playlist | youtube_channel | plex
    source_ref: str
    name: str
    items: tuple[str, ...]  # video ids, in the source's order
    description: str | None = None
    #: what the stage wants to keep beside it (``collections.auto_rule``), e.g. Plex's
    #: items that are not in Maktaba
    rule: dict[str, Any] = field(default_factory=dict)

    def membership_hash(self) -> str:
        return canonical_hash([self.name, self.description, list(self.items), self.rule])


@dataclass(slots=True)
class SyncStats:
    created: int = 0
    updated: int = 0
    deleted: int = 0
    unchanged: int = 0

    @property
    def writes(self) -> int:
        return self.created + self.updated + self.deleted

    def as_dict(self) -> dict[str, int]:
        return {
            "created": self.created,
            "updated": self.updated,
            "deleted": self.deleted,
            "unchanged": self.unchanged,
        }


_SUFFIX = {"youtube_playlist": "YouTube", "youtube_channel": "YouTube", "plex": "Plex"}


async def _free_name(db: Any, library_id: UUID, name: str, kind: str, own: str | None) -> str:
    """``name``, or the first free ``name (YouTube)``, ``name (YouTube 2)``… in the library
    (collection names are unique per library); ``own`` is the collection being renamed."""
    taken = {
        str(r["name"])
        for r in await db.fetch(
            "SELECT id, name FROM collections WHERE library_id = $1", uid(library_id)
        )
        if own is None or str(r["id"]) != own
    }
    if name not in taken:
        return name
    base = f"{name} ({_SUFFIX.get(kind, kind)}"
    for n in range(1, 1000):
        cand = f"{base})" if n == 1 else f"{base} {n})"
        if cand not in taken:
            return cand
    return f"{name} ({uuid.uuid4().hex[:6]})"


async def _write_items(db: Any, cid: str, items: Sequence[str]) -> None:
    await db.execute("DELETE FROM collection_items WHERE collection_id = $1", cid)
    for pos, vid in enumerate(dict.fromkeys(items), 1):
        await db.execute(
            "INSERT INTO collection_items (collection_id, video_id, position) VALUES ($1,$2,$3)",
            cid,
            vid,
            pos * 10,
        )


async def sync_sourced(
    db: Any, library_id: UUID, kinds: Sequence[str], desired: Sequence[Desired]
) -> SyncStats:
    """Make ``library_id``'s collections of ``kinds`` the ``desired`` ones."""
    stats = SyncStats()
    want = {(d.source_kind, d.source_ref): d for d in desired}
    ph = ", ".join(f"${i + 2}" for i in range(len(kinds)))
    have = {
        (str(r["source_kind"]), str(r["source_ref"])): (str(r["id"]), r["membership_hash"])
        for r in await db.fetch(
            "SELECT id, source_kind, source_ref, membership_hash FROM collections"  # noqa: S608
            f" WHERE library_id = $1 AND source_kind IN ({ph})",
            uid(library_id),
            *kinds,
        )
    }
    stamp = to_ts(db, now())
    yes = True if is_pg(db) else 1
    async with db.transaction():
        for key, (cid, _) in sorted(have.items()):
            if key not in want:
                await db.execute("DELETE FROM collection_items WHERE collection_id = $1", cid)
                await db.execute("DELETE FROM collections WHERE id = $1", cid)
                stats.deleted += 1
        for key, d in sorted(want.items()):
            h = d.membership_hash()
            got = have.get(key)
            if got is not None and got[1] == h:
                stats.unchanged += 1
                continue
            rule = dumps({"source": d.source_kind, **d.rule})
            if got is None:
                cid = str(uuid.uuid4())
                name = await _free_name(db, library_id, d.name, d.source_kind, None)
                await db.execute(
                    "INSERT INTO collections (id, library_id, name, description, is_smart,"
                    " origin, auto_rule, source_kind, source_ref, membership_hash, hand_ordered,"
                    " created_at, updated_at)"
                    " VALUES ($1,$2,$3,$4,$5,'auto',$6,$7,$8,$9,$10,$11,$11)",
                    cid,
                    uid(library_id),
                    name,
                    d.description,
                    False if is_pg(db) else 0,
                    rule,
                    d.source_kind,
                    d.source_ref,
                    h,
                    yes,
                    stamp,
                )
                stats.created += 1
            else:
                cid = got[0]
                name = await _free_name(db, library_id, d.name, d.source_kind, cid)
                await db.execute(
                    "UPDATE collections SET name = $2, description = $3, auto_rule = $4,"
                    " membership_hash = $5, hand_ordered = $6, updated_at = $7 WHERE id = $1",
                    cid,
                    name,
                    d.description,
                    rule,
                    h,
                    yes,
                    stamp,
                )
                stats.updated += 1
            await _write_items(db, cid, d.items)
    return stats
