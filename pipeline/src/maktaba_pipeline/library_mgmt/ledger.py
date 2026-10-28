"""The file-action ledger (Epic 47, story 47.2): ``file_actions``.

One row per file operation: the audit, the crash-recovery journal and the undo list. A
row moves forward only::

    planned -> staged -> verified -> swapped -> done
       \\-------\\----------\\-----------\\--> rolled_back | failed

and ``done -> rolled_back`` when it is restored. ``failed -> done`` is the one way back,
for a dedup whose file move failed after its transaction committed: the retry moves the
file only.

Paths are stored in the database's form (``videos.path``), never this machine's: the
recovery on another worker maps them through its own path map.
"""

from __future__ import annotations

import uuid
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass
from datetime import datetime
from typing import Any, Literal, get_args
from uuid import UUID

from ..engine.sql import dumps, loads, now, parse_ts, to_ts, uid

__all__ = [
    "KINDS",
    "LIVE_JOB_STATES",
    "OPEN_STATES",
    "STATES",
    "TRANSITIONS",
    "ActionKind",
    "ActionState",
    "FileAction",
    "LedgerError",
    "action_from_row",
    "advance",
    "create_action",
    "get_action",
    "list_actions",
    "open_actions",
    "update_fields",
]

ActionKind = Literal["convert", "dedup", "youtube_fetch", "purge", "restore"]
ActionState = Literal["planned", "staged", "verified", "swapped", "done", "rolled_back", "failed"]

KINDS: tuple[ActionKind, ...] = get_args(ActionKind)
STATES: tuple[ActionState, ...] = get_args(ActionState)
#: a row in one of these was cut short (a crash, a kill): recovery finishes or undoes it
OPEN_STATES: tuple[ActionState, ...] = ("planned", "staged", "verified", "swapped")
#: a job in one of these may still be working on its row: recovery leaves it alone
LIVE_JOB_STATES: tuple[str, ...] = ("claimed", "running", "resuming")

TRANSITIONS: Mapping[ActionState, frozenset[ActionState]] = {
    "planned": frozenset({"staged", "swapped", "rolled_back", "failed"}),
    "staged": frozenset({"verified", "rolled_back", "failed"}),
    "verified": frozenset({"swapped", "rolled_back", "failed"}),
    "swapped": frozenset({"done", "rolled_back", "failed"}),
    "done": frozenset({"rolled_back"}),
    "failed": frozenset({"done", "rolled_back"}),
    "rolled_back": frozenset(),
}

#: the columns :func:`advance` and :func:`update_fields` may write
_FIELDS = frozenset(
    {
        "video_id",
        "old_path",
        "new_path",
        "trash_path",
        "old_hash",
        "new_hash",
        "old_size",
        "new_size",
        "verify",
        "error",
        "purge_after",
        "purged_at",
    }
)


class LedgerError(Exception):
    """A ledger row is missing, or a change would move it backwards."""


@dataclass(frozen=True, slots=True)
class FileAction:
    id: UUID
    library_id: UUID
    video_id: UUID | None
    job_id: int | None
    kind: ActionKind
    state: ActionState
    old_path: str | None
    new_path: str | None
    trash_path: str | None
    old_hash: str | None
    new_hash: str | None
    old_size: int | None
    new_size: int | None
    verify: dict[str, Any] | None
    error: str | None
    purge_after: datetime | None
    purged_at: datetime | None
    created_at: datetime | None
    updated_at: datetime | None

    @property
    def is_open(self) -> bool:
        return self.state in OPEN_STATES


def action_from_row(r: Any) -> FileAction:
    def opt_uuid(v: Any) -> UUID | None:
        return None if v is None else UUID(str(v))

    def opt_int(v: Any) -> int | None:
        return None if v is None else int(v)

    verify = loads(r["verify"])
    return FileAction(
        id=UUID(str(r["id"])),
        library_id=UUID(str(r["library_id"])),
        video_id=opt_uuid(r["video_id"]),
        job_id=opt_int(r["job_id"]),
        kind=r["kind"],
        state=r["state"],
        old_path=r["old_path"],
        new_path=r["new_path"],
        trash_path=r["trash_path"],
        old_hash=r["old_hash"],
        new_hash=r["new_hash"],
        old_size=opt_int(r["old_size"]),
        new_size=opt_int(r["new_size"]),
        verify=verify if isinstance(verify, dict) else None,
        error=r["error"],
        purge_after=parse_ts(r["purge_after"]),
        purged_at=parse_ts(r["purged_at"]),
        created_at=parse_ts(r["created_at"]),
        updated_at=parse_ts(r["updated_at"]),
    )


def _param(db: Any, name: str, value: Any) -> Any:
    if value is None:
        return None
    if name == "verify":
        return dumps(value)
    if name in ("purge_after", "purged_at"):
        return to_ts(db, value)
    if name == "video_id":
        return uid(value)
    if name in ("old_size", "new_size"):
        return int(value)
    return value


async def create_action(
    db: Any,
    *,
    library_id: UUID | str,
    kind: ActionKind,
    video_id: UUID | str | None = None,
    job_id: int | None = None,
    state: ActionState = "planned",
    old_path: str | None = None,
    new_path: str | None = None,
    trash_path: str | None = None,
    old_hash: str | None = None,
    new_hash: str | None = None,
    old_size: int | None = None,
    new_size: int | None = None,
    verify: Mapping[str, Any] | None = None,
    purge_after: datetime | None = None,
) -> FileAction:
    """Insert a ledger row (``planned`` unless ``state`` says otherwise) and return it."""
    if kind not in KINDS:
        raise LedgerError(f"unknown file action kind {kind!r}")
    if state not in STATES:
        raise LedgerError(f"unknown file action state {state!r}")
    action_id = uuid.uuid4()
    ts = to_ts(db, now())
    await db.execute(
        "INSERT INTO file_actions (id, library_id, video_id, job_id, kind, state, old_path,"
        " new_path, trash_path, old_hash, new_hash, old_size, new_size, verify, purge_after,"
        " created_at, updated_at)"
        " VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$16)",
        uid(action_id),
        uid(library_id),
        uid(video_id),
        job_id,
        kind,
        state,
        old_path,
        new_path,
        trash_path,
        old_hash,
        new_hash,
        _param(db, "old_size", old_size),
        _param(db, "new_size", new_size),
        _param(db, "verify", None if verify is None else dict(verify)),
        _param(db, "purge_after", purge_after),
        ts,
    )
    got = await get_action(db, action_id)
    assert got is not None
    return got


async def get_action(db: Any, action_id: UUID | str) -> FileAction | None:
    r = await db.fetchrow("SELECT * FROM file_actions WHERE id = $1", uid(action_id))
    return None if r is None else action_from_row(r)


async def advance(
    db: Any,
    action_id: UUID | str,
    state: ActionState,
    *,
    expect: ActionState | Iterable[ActionState] | None = None,
    **fields: Any,
) -> FileAction:
    """Move a row to ``state`` (and write ``fields``); the transition must be allowed.

    ``expect`` names the state(s) the caller believes the row is in; a row elsewhere (a
    recovery got there first) raises :class:`LedgerError` and changes nothing.
    """
    cur = await get_action(db, action_id)
    if cur is None:
        raise LedgerError(f"file action {action_id} not found")
    if expect is not None:
        wanted = {expect} if isinstance(expect, str) else set(expect)
        if cur.state not in wanted:
            raise LedgerError(
                f"file action {action_id} is {cur.state}, expected {'/'.join(sorted(wanted))}"
            )
    if state != cur.state and state not in TRANSITIONS[cur.state]:
        raise LedgerError(f"file action {action_id}: {cur.state} -> {state} is not allowed")
    bad = set(fields) - _FIELDS
    if bad:
        raise LedgerError(f"file action fields not writable: {sorted(bad)}")
    sets = ["state = $3", "updated_at = $4"]
    args: list[Any] = [uid(action_id), cur.state, state, to_ts(db, now())]
    for name, value in fields.items():
        args.append(_param(db, name, value))
        sets.append(f"{name} = ${len(args)}")
    hit = await db.fetchval(
        f"UPDATE file_actions SET {', '.join(sets)} WHERE id = $1 AND state = $2 RETURNING id",
        *args,
    )
    if hit is None:
        raise LedgerError(f"file action {action_id} changed under us (was {cur.state})")
    got = await get_action(db, action_id)
    assert got is not None
    return got


async def update_fields(db: Any, action_id: UUID | str, **fields: Any) -> None:
    """Write ``fields`` without a state change (``purged_at``, an ``error`` note)."""
    bad = set(fields) - _FIELDS
    if bad:
        raise LedgerError(f"file action fields not writable: {sorted(bad)}")
    if not fields:
        return
    args: list[Any] = [uid(action_id), to_ts(db, now())]
    sets = ["updated_at = $2"]
    for name, value in fields.items():
        args.append(_param(db, name, value))
        sets.append(f"{name} = ${len(args)}")
    await db.execute(f"UPDATE file_actions SET {', '.join(sets)} WHERE id = $1", *args)


async def open_actions(
    db: Any,
    *,
    library_id: UUID | str | None = None,
    video_id: UUID | str | None = None,
    include_live: bool = False,
) -> list[FileAction]:
    """Rows left in ``planned``/``staged``/``verified``/``swapped``, oldest first.

    Unless ``include_live``, a row whose job is still claimed or running is left out: that
    job may be mid-swap on another worker.
    """
    where = [f"fa.state IN ({', '.join(repr(s) for s in OPEN_STATES)})"]
    args: list[Any] = []
    if library_id is not None:
        args.append(uid(library_id))
        where.append(f"fa.library_id = ${len(args)}")
    if video_id is not None:
        args.append(uid(video_id))
        where.append(f"fa.video_id = ${len(args)}")
    if not include_live:
        live = ", ".join(repr(s) for s in LIVE_JOB_STATES)
        where.append(
            "NOT EXISTS (SELECT 1 FROM processing_jobs pj WHERE pj.id = fa.job_id"
            f" AND pj.state IN ({live}))"
        )
    rows = await db.fetch(
        f"SELECT fa.* FROM file_actions fa WHERE {' AND '.join(where)}"
        " ORDER BY fa.created_at, fa.id",
        *args,
    )
    return [action_from_row(r) for r in rows]


async def list_actions(
    db: Any,
    *,
    library_id: UUID | str | None = None,
    kinds: Sequence[ActionKind] | None = None,
    states: Sequence[ActionState] | None = None,
    limit: int = 200,
) -> list[FileAction]:
    """The newest rows first, filtered by library, kind and state."""
    where: list[str] = []
    args: list[Any] = []
    if library_id is not None:
        args.append(uid(library_id))
        where.append(f"library_id = ${len(args)}")
    if kinds:
        where.append(f"kind IN ({', '.join(repr(k) for k in kinds if k in KINDS)})")
    if states:
        where.append(f"state IN ({', '.join(repr(s) for s in states if s in STATES)})")
    args.append(int(limit))
    sql = "SELECT * FROM file_actions"
    if where:
        sql += " WHERE " + " AND ".join(where)
    sql += f" ORDER BY created_at DESC, id DESC LIMIT ${len(args)}"
    return [action_from_row(r) for r in await db.fetch(sql, *args)]
