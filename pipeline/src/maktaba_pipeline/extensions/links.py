"""``external_links`` access shared by the built-in extensions.

``external_links`` remembers which external object belongs to which Maktaba
subject, and whether this tool created it (``created_by_tool``): an object the
tool did not create is never changed or deleted.
"""

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass
from typing import Any

from ..engine.sql import now, to_ts

__all__ = ["Link", "delete_link", "get_link", "get_links", "put_link"]

_CHUNK = 400


@dataclass(frozen=True, slots=True)
class Link:
    target: str
    subject_kind: str
    subject_id: str
    external_id: str
    title: str
    content_hash: str
    created_by_tool: bool


def _link(r: Any) -> Link:
    return Link(
        str(r["target"]),
        str(r["subject_kind"]),
        str(r["subject_id"]),
        str(r["external_id"]),
        str(r["title"]),
        str(r["content_hash"]),
        bool(r["created_by_tool"]),
    )


_COLS = "target, subject_kind, subject_id, external_id, title, content_hash, created_by_tool"


async def get_link(db: Any, target: str, subject_kind: str, subject_id: str) -> Link | None:
    row = await db.fetchrow(
        f"SELECT {_COLS} FROM external_links"
        " WHERE target = $1 AND subject_kind = $2 AND subject_id = $3",
        target,
        subject_kind,
        str(subject_id),
    )
    return None if row is None else _link(row)


async def get_links(
    db: Any, target: str, subject_kind: str, subject_ids: Sequence[str]
) -> dict[str, Link]:
    """Links of one target for many subjects, keyed by subject id."""
    out: dict[str, Link] = {}
    ids = [str(i) for i in dict.fromkeys(subject_ids)]
    for i in range(0, len(ids), _CHUNK):
        chunk = ids[i : i + _CHUNK]
        marks = ",".join(f"${n + 3}" for n in range(len(chunk)))
        rows = await db.fetch(
            f"SELECT {_COLS} FROM external_links WHERE target = $1 AND subject_kind = $2"
            f" AND subject_id IN ({marks})",
            target,
            subject_kind,
            *chunk,
        )
        out.update({str(r["subject_id"]): _link(r) for r in rows})
    return out


async def put_link(db: Any, link: Link) -> None:
    await db.execute(
        f"INSERT INTO external_links ({_COLS}, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)"
        " ON CONFLICT (target, subject_kind, subject_id) DO UPDATE SET"
        " external_id = EXCLUDED.external_id, title = EXCLUDED.title,"
        " content_hash = EXCLUDED.content_hash, created_by_tool = EXCLUDED.created_by_tool,"
        " updated_at = EXCLUDED.updated_at",
        link.target,
        link.subject_kind,
        link.subject_id,
        link.external_id,
        link.title,
        link.content_hash,
        link.created_by_tool,
        to_ts(db, now()),
    )


async def delete_link(db: Any, target: str, subject_kind: str, subject_id: str) -> None:
    await db.execute(
        "DELETE FROM external_links WHERE target = $1 AND subject_kind = $2 AND subject_id = $3",
        target,
        subject_kind,
        str(subject_id),
    )
