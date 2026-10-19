"""TV change rows and their events (slot 0127), the pipeline's side.

The state machine itself is the API's (``api/internal/tvchange``, story 33.10): the
pipeline writes the transitions its jobs perform - a producer's ``draft`` / ``staged``,
the stage job's ``staged`` (or ``failed``), the swap job's ``live`` - always through
:func:`transition`, which checks the state it starts from, so a change an admin
cancelled meanwhile is never staged or swapped behind their back.
"""

from __future__ import annotations

import uuid
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from ..engine.sql import dumps, is_pg, loads, now, parse_ts, to_ts

__all__ = [
    "IN_FLIGHT",
    "Change",
    "StateConflict",
    "create_change",
    "event",
    "get_change",
    "in_flight",
    "open_draft",
    "transition",
    "update_change",
]

#: the states a change is "in flight" in: at most one at a time (README R9.4)
IN_FLIGHT = ("staged", "verifying", "ready", "approved", "swapping")
_JSON_COLS = ("kinds", "source", "change_set", "diff", "verify_report", "approved_by")
_TS_COLS = (
    "approved_at",
    "anchor_at",
    "swap_at",
    "created_at",
    "updated_at",
    "staged_at",
    "ready_at",
    "swapped_at",
    "ended_at",
)


class StateConflict(RuntimeError):
    """The change is not in the state a transition starts from."""


@dataclass(slots=True)
class Change:
    id: str
    number: int
    state: str
    row: dict[str, Any]

    @property
    def pending(self) -> dict[str, Any]:
        diff = self.row.get("diff") or {}
        got = diff.get("pending") if isinstance(diff, dict) else None
        return got if isinstance(got, dict) else {}

    @property
    def source(self) -> dict[str, Any]:
        s = self.row.get("source")
        return s if isinstance(s, dict) else {}


def _decode(row: Any) -> Change:
    d = dict(row)
    for c in _JSON_COLS:
        d[c] = (
            loads(d.get(c))
            if d.get(c) not in (None, "")
            else ([] if c in ("kinds", "approved_by") else {})
        )
    for c in _TS_COLS:
        d[c] = parse_ts(d.get(c))
    for c in ("id", "release_id", "base_release_id", "superseded_by"):
        if d.get(c) is not None:
            d[c] = str(d[c])
    return Change(id=str(d["id"]), number=int(d.get("number") or 0), state=str(d["state"]), row=d)


async def get_change(db: Any, change_id: str) -> Change | None:
    row = await db.fetchrow("SELECT * FROM tv_changes WHERE id = $1", change_id)
    return None if row is None else _decode(row)


async def in_flight(db: Any) -> Change | None:
    """The change in ``staged … swapping``, if any."""
    marks = ",".join(f"'{s}'" for s in IN_FLIGHT)
    row = await db.fetchrow(
        f"SELECT * FROM tv_changes WHERE state IN ({marks}) ORDER BY created_at LIMIT 1"
    )
    return None if row is None else _decode(row)


async def open_draft(db: Any, kind: str) -> Change | None:
    """The open draft of a producer kind (``write_channels``, ``editor``, ``settings``,
    ``schedule``): at most one per kind (design 11.2)."""
    for row in await db.fetch(
        "SELECT * FROM tv_changes WHERE state = 'draft' ORDER BY created_at DESC"
    ):
        ch = _decode(row)
        if ch.source.get("kind") == kind:
            return ch
    return None


async def create_change(
    db: Any,
    *,
    state: str,
    title: str,
    source: dict[str, Any],
    pending: dict[str, Any],
    change_set: dict[str, Any] | None = None,
    digest: str | None = None,
    created_by: str | None = None,
    actor_source: str = "system",
) -> str:
    """Insert a change (``draft`` or ``staged``) and its creation event."""
    cid = str(uuid.uuid4())
    stamp = to_ts(db, now())
    diff = {"pending": pending}
    if is_pg(db):
        await db.execute(
            "INSERT INTO tv_changes (id, state, title, source, change_set, digest, diff,"
            " created_by, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)",
            cid, state, title, dumps(source), dumps(change_set or {}), digest, dumps(diff),
            created_by, stamp,
        )  # fmt: skip
    else:
        await db.execute(
            "INSERT INTO tv_changes (id, number, state, title, source, change_set, digest, diff,"
            " created_by, created_at, updated_at) VALUES ($1,"
            " (SELECT COALESCE(MAX(number), 0) + 1 FROM tv_changes),$2,$3,$4,$5,$6,$7,$8,$9,$9)",
            cid, state, title, dumps(source), dumps(change_set or {}), digest, dumps(diff),
            created_by, stamp,
        )  # fmt: skip
    await event(
        db, cid, from_state=None, to_state=state, action="create", actor_source=actor_source,
        actor_user_id=created_by, detail={"source": source},
    )  # fmt: skip
    return cid


async def update_change(db: Any, change_id: str, **fields: Any) -> None:
    """Set columns of a change (JSON columns are encoded, timestamps converted)."""
    if not fields:
        return
    sets: list[str] = []
    args: list[Any] = [change_id]
    for k, v in {**fields, "updated_at": now()}.items():
        if k in _JSON_COLS:
            v = dumps(v)
        elif k in _TS_COLS and isinstance(v, datetime):
            v = to_ts(db, v)
        args.append(v)
        sets.append(f"{k} = ${len(args)}")
    await db.execute(f"UPDATE tv_changes SET {', '.join(sets)} WHERE id = $1", *args)


async def event(
    db: Any,
    change_id: str,
    *,
    from_state: str | None,
    to_state: str | None,
    action: str,
    actor_source: str = "system",
    actor_user_id: str | None = None,
    detail: dict[str, Any] | None = None,
) -> None:
    await db.execute(
        "INSERT INTO tv_change_events (change_id, at, actor_user_id, actor_source, from_state,"
        " to_state, action, detail) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",
        change_id, to_ts(db, now()), actor_user_id, actor_source, from_state, to_state, action,
        dumps(detail or {}),
    )  # fmt: skip


async def transition(
    db: Any,
    change_id: str,
    *,
    from_states: tuple[str, ...],
    to: str,
    action: str,
    actor_source: str = "system",
    detail: dict[str, Any] | None = None,
    **fields: Any,
) -> None:
    """Move a change from one of ``from_states`` to ``to`` (with ``fields``), with its
    event; :class:`StateConflict` when it is elsewhere (an admin cancelled it)."""
    cur = await db.fetchval("SELECT state FROM tv_changes WHERE id = $1", change_id)
    if cur not in from_states:
        raise StateConflict(f"change {change_id} is {cur}, not {'/'.join(from_states)}")
    row = await db.fetchrow(
        "UPDATE tv_changes SET state = $2, updated_at = $4 WHERE id = $1 AND state = $3"
        " RETURNING id",
        change_id,
        to,
        cur,
        to_ts(db, now()),
    )
    if row is None:  # moved meanwhile
        raise StateConflict(f"change {change_id} left {cur}")
    cur_from = str(cur)
    if fields:
        await update_change(db, change_id, **fields)
    await event(
        db, change_id, from_state=cur_from, to_state=to, action=action,
        actor_source=actor_source, detail=detail,
    )  # fmt: skip
