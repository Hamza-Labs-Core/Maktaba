"""``advance_after_stage`` — the only sanctioned ``videos.state`` mutator.

Story 1.6 names this function as the single point through which every
state transition flows: stage finishes, filesystem disappearances,
library merges, and integrity-sweep failures all call it. The lint
quarantine in plan-01-06 §11.2 (Python ``ruff`` rule, Go ``forbidigo``
rule) is what mechanically enforces the invariant; this module is the
sanctioned write site.

The function:

1. Opens a transaction and takes a row-level lock on the video (``SELECT
   … FOR UPDATE``).
2. Re-reads the current state inside the lock so concurrent advances
   serialize.
3. If the current state is one of :data:`states.TERMINAL_DROP_STATES`
   (``SUPERSEDED`` / ``CORRUPTED`` / ``FAILED``), logs
   ``late_stage_finish`` and commits a no-op (story edge case 1).
4. Looks up the ``(from, trigger, outcome)`` triple via
   :func:`states.lookup`. A miss raises :class:`IllegalStateTransition`
   without mutating state.
5. UPDATEs ``videos.state`` and ``updated_at``. The slot 0004
   ``videos_state_change_notify_trg`` trigger fires
   ``pg_notify('videos.state_changed', …)`` automatically; on SQLite
   the helper publishes on the in-process bus instead.
"""

from __future__ import annotations

import json
from contextlib import AbstractAsyncContextManager
from typing import Any, Protocol
from uuid import UUID

from ..db.pubsub import get_bus
from ..domain.states import (
    TERMINAL_DROP_STATES,
    IllegalStateTransition,
    Outcome,
    State,
    Trigger,
    lookup,
)

__all__ = ["VIDEOS_STATE_CHANGED", "advance_after_stage"]


# Canonical pubsub channel name for state transitions. Mirrors the
# Postgres NOTIFY channel from slot 0004 so SQLite callers can subscribe
# with the same string.
VIDEOS_STATE_CHANGED = "videos.state_changed"


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class _Logger(Protocol):
    def info(self, event: str, **kwargs: Any) -> Any: ...


class DBConn(Protocol):
    """The minimal connection shape :func:`advance_after_stage` needs.

    ``dialect`` is one of ``"postgres"`` or ``"sqlite"``. The Postgres
    NOTIFY trigger fires at the SQL level after the UPDATE commits; the
    SQLite helper publishes manually on the in-process pubsub bus
    because SQLite has no LISTEN/NOTIFY.
    """

    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


_LOCK_SQL = "SELECT state, library_id FROM videos WHERE id = $1 FOR UPDATE"
_UPDATE_SQL = "UPDATE videos SET state = $1, updated_at = now() WHERE id = $2"


async def advance_after_stage(
    db: DBConn,
    video_id: UUID,
    trigger: Trigger,
    outcome: str | Outcome,
    *,
    log: _Logger,
) -> State:
    """Apply the ``(from, trigger, outcome)`` transition to ``videos``.

    Returns the new state on success, or the unchanged state when the
    row is in a terminal-drop state (a late stage finish — logged as
    ``late_stage_finish``). Raises :class:`IllegalStateTransition` when
    the triple is not in the FSM; the row is left untouched in that
    case.

    The function is dialect-agnostic. On Postgres the NOTIFY trigger
    fires at the SQL layer; on SQLite this function publishes on the
    in-process bus after the transaction commits so subscribers see
    the same payload shape on either backend.
    """
    outcome_value = outcome.value if isinstance(outcome, Outcome) else outcome

    async with db.transaction():
        row = await db.fetchrow(_LOCK_SQL, video_id)
        if row is None:
            raise LookupError(f"video {video_id} not found")
        current = State(row["state"])
        library_id = row["library_id"]

        if current in TERMINAL_DROP_STATES:
            log.info(
                "late_stage_finish",
                video_id=str(video_id),
                current=current.value,
                trigger=trigger.value,
                outcome=outcome_value,
            )
            return current

        target = lookup(current, trigger, outcome_value)
        if target is None:
            raise IllegalStateTransition(video_id, current, trigger, outcome_value)

        await db.execute(_UPDATE_SQL, target.value, video_id)

        # SQLite has no LISTEN/NOTIFY, so the helper publishes manually
        # on the in-process bus. Postgres' AFTER UPDATE trigger handles
        # the equivalent fan-out at the SQL layer.
        if db.dialect == "sqlite":
            get_bus().publish(
                VIDEOS_STATE_CHANGED,
                {
                    "video_id": str(video_id),
                    "library_id": str(library_id) if library_id is not None else None,
                    "old_state": current.value,
                    "new_state": target.value,
                },
            )
        return target


def serialize_notify_payload(
    video_id: UUID,
    library_id: UUID | str | None,
    old_state: State,
    new_state: State,
) -> str:
    """Build the JSON payload the slot 0004 trigger emits.

    Test helper — production code path goes through the SQL trigger.
    Exposed so tests can compare a Python-built payload against what
    Postgres NOTIFY delivers.
    """
    return json.dumps(
        {
            "video_id": str(video_id),
            "library_id": str(library_id) if library_id is not None else None,
            "old_state": old_state.value,
            "new_state": new_state.value,
        },
        separators=(",", ":"),
    )
