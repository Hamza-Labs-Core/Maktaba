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
    "OPEN",
    "REOPEN_WHY",
    "SOURCE_AUTO_UPDATE",
    "Change",
    "StateConflict",
    "create_change",
    "draft_with_digest",
    "event",
    "get_change",
    "in_flight",
    "open_change",
    "open_changes",
    "reopen",
    "transition",
    "update_change",
]

#: the states a change is "in flight" in: at most one at a time (README R9.4)
IN_FLIGHT = ("staged", "verifying", "ready", "approved", "swapping")
#: the states the open change is in: an update joins it until it is approved (the
#: owner's rule, story 33.12 AC3); approved, swapping or live, it takes no more
OPEN = ("draft", "staged", "verifying", "ready")
#: the reopen event's reason (a checked change an update joined goes back to draft)
REOPEN_WHY = "updated after it was checked: it is checked again"
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


#: the automatic updater's change of the streaming binary (Epic 35 story 35.6): a change
#: of its own, which no update joins (the API's tvchange.SourceAutoUpdate)
SOURCE_AUTO_UPDATE = "auto_update"


async def open_changes(db: Any) -> list[Change]:
    """The changes no one approved in ``draft … ready``, oldest first. There is one at
    most (the open change); a second is one opened before the rule (or while the
    first was in flight) and joins the first (``stage.collapse_open``). The automatic
    updater's change is none of them: it queues behind the change in flight on its own."""
    marks = ",".join(f"'{s}'" for s in OPEN)
    rows = await db.fetch(
        f"SELECT * FROM tv_changes WHERE state IN ({marks}) ORDER BY number, created_at"
    )
    return [
        ch
        for ch in map(_decode, rows)
        if not ch.row.get("approved_by") and ch.source.get("kind") != SOURCE_AUTO_UPDATE
    ]


async def open_change(db: Any) -> Change | None:
    """The open change: the oldest change no one approved in ``draft``, ``staged``,
    ``verifying`` or ``ready``. Every update to the TV joins it, whatever its kind
    (``merge``'s rules); joined after it was checked, it goes back to draft
    (:func:`reopen`). Approved, swapping or live, a change takes no more updates."""
    got = await open_changes(db)
    return got[0] if got else None


async def reopen(
    db: Any,
    ch: Change,
    *,
    restage: bool,
    actor_source: str = "system",
    detail: dict[str, Any] | None = None,
) -> Change:
    """An update joined ``ch`` after it was checked (``staged`` … ``ready``): it goes
    back to ``draft`` with a ``reopen`` event (its verify report kept in the event as
    history; its release is built anew when it is staged again). ``restage`` (the
    write mode stages by itself) asks the controller to stage it once more: one stage
    per update, as a failed check asks none. A draft is returned as it is."""
    if ch.state == "draft":
        return ch
    diff = dict(ch.row.get("diff") or {})
    diff.pop("stage", None)
    diff.pop("update_hold", None)  # a new update: its check is a new one
    if restage:
        diff["stage"] = {"requested_at": now().strftime("%Y-%m-%dT%H:%M:%SZ")}
    await transition(
        db, ch.id, from_states=(ch.state,), to="draft", action="reopen",
        actor_source=actor_source,
        detail={"why": REOPEN_WHY, "from": ch.state, "report": ch.row.get("verify_report") or {},
                **(detail or {})},
        diff=diff,
    )  # fmt: skip
    got = await get_change(db, ch.id)
    if got is None:
        raise LookupError(f"no TV change {ch.id}")
    return got


async def draft_with_digest(db: Any, digest: str) -> Change | None:
    """The draft holding the channel plan ``digest`` (approved or not): the queue's
    Apply re-runs that very plan into the change its Approve was given to."""
    row = await db.fetchrow(
        "SELECT * FROM tv_changes WHERE state = 'draft' AND digest = $1"
        " ORDER BY created_at DESC LIMIT 1",
        digest,
    )
    return None if row is None else _decode(row)


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
    origin: str | None = None,
) -> str:
    """Insert a change (``draft`` or ``staged``) and its creation event. ``origin`` (slot
    0169): ``auto`` | ``manual``; None: the producer's (``config.origin_for``)."""
    from .config import origin_for

    cid = str(uuid.uuid4())
    stamp = to_ts(db, now())
    diff = {"pending": pending}
    if origin not in ("auto", "manual"):
        origin = origin_for(str(source.get("kind", "")), created_by)
    if is_pg(db):
        await db.execute(
            "INSERT INTO tv_changes (id, state, title, source, change_set, digest, diff,"
            " created_by, created_at, updated_at, origin)"
            " VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$10)",
            cid, state, title, dumps(source), dumps(change_set or {}), digest, dumps(diff),
            created_by, stamp, origin,
        )  # fmt: skip
    else:
        await db.execute(
            "INSERT INTO tv_changes (id, number, state, title, source, change_set, digest, diff,"
            " created_by, created_at, updated_at, origin) VALUES ($1,"
            " (SELECT COALESCE(MAX(number), 0) + 1 FROM tv_changes),"
            "$2,$3,$4,$5,$6,$7,$8,$9,$9,$10)",
            cid, state, title, dumps(source), dumps(change_set or {}), digest, dumps(diff),
            created_by, stamp, origin,
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
