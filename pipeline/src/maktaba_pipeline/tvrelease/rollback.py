"""The reverse commit (story 33.9, design 10): the previous release goes on the air
again in one transaction - its lineup back into ``channels`` (channels the rolled-back
release removed come back, the ones it added are ``staged`` again), its art and settings,
``tv_routing`` flipped back to the previous generation (``epoch + 1``, no rollback point
left), the change ``rolled_back`` with its reason (``manual`` or ``auto:<cause>``).

The streams are the front door's: a warm rollback's operation (``tv_swap_ops`` kind
``rollback``, every channel at once) is written by the API (an admin's rollback) or by
the front door itself (a crash of the new live slot inside the grace), and the front
door switches the channels back to the previous slot - which still airs the previous
release - with the same HLS stitch, TS splice and relay source switch as the swap. This
commit marks that operation ``committed``. Without TV slots (one TV process) the flip is
the whole rollback: the engine reads the previous generation from its next block.

Not reverted: ``write_channels``' links and filler pools a swapped ``write_channels``
change wrote (the previous release plans from its own snapshot; a re-stage writes them
again). The rolled-back change's edits are kept in a new draft (story 33.9 AC6).
"""

from __future__ import annotations

from datetime import datetime
from typing import Any

from ..engine.sql import is_pg, now, to_ts
from ..log import get_logger
from .build import load_release
from .commit import _apply_art, _apply_lineup, _apply_settings, _enqueue_plex, _lock
from .store import create_change, get_change, open_draft, transition

__all__ = ["rollback_change"]

_log = get_logger()


async def rollback_change(
    db: Any,
    change_id: str,
    *,
    op_id: int | None = None,
    reason: str = "manual",
    clock: datetime | None = None,
    actor_source: str = "system",
    actor_user_id: str | None = None,
) -> dict[str, Any]:
    """Put the previous release back on the air for the live (or swapping) change
    ``change_id``. Raises :class:`LookupError` when there is no rollback point."""
    clock = clock or now()
    ch = await get_change(db, change_id)
    if ch is None or ch.state not in ("live", "swapping"):
        raise LookupError(f"change {change_id} is not live")
    stamp = to_ts(db, clock)
    async with db.transaction():
        await _lock(db)
        route = await db.fetchrow("SELECT * FROM tv_routing WHERE scope = 'cluster'")
        if route is None or route["previous_release_id"] is None:
            raise LookupError("no_rollback_point")
        if str(route["live_release_id"] or "") != str(ch.row.get("release_id") or ""):
            # the change is not what airs: a swap whose commit has not run yet
            # (its previous is the release two changes back) or a stale request
            raise LookupError(f"change {change_id} is not the release on the air")
        prev = await load_release(db, str(route["previous_release_id"]))
        if prev is None:
            raise LookupError("the previous release is gone")
        epoch = int(route["epoch"] or 0) + 1
        lineup = await _apply_lineup(db, prev, stamp, removed_lifecycle="staged")
        await _apply_art(db, prev, stamp)
        settings = await _apply_settings(db, prev, stamp)
        await db.execute(
            "UPDATE tv_routing SET live_release_id = $1, live_gen = $2, previous_release_id = NULL,"
            " previous_gen = NULL, previous_until = NULL, epoch = $3, switched_at = $4"
            " WHERE scope = 'cluster'",
            prev.id,
            prev.sched_gen,
            epoch,
            stamp,
        )
        if op_id is not None:
            await db.execute(
                "UPDATE tv_swap_ops SET committed = $2, epoch = $3 WHERE id = $1",
                op_id,
                True if is_pg(db) else 1,
                epoch,
            )
        report = {
            "lineup": lineup,
            "settings": settings,
            "epoch": epoch,
            "to_gen": prev.sched_gen,
            "to_release": prev.id,
            "op_id": op_id,
            "reason": reason,
        }
        await transition(
            db, change_id, from_states=("live", "swapping"), to="rolled_back", action="rollback",
            actor_source=actor_source, detail={**report, "automatic": reason.startswith("auto:")},
            ended_at=clock, end_reason=reason,
        )  # fmt: skip
        if is_pg(db):
            await db.execute("SELECT pg_notify('tv_routing', $1)", str(epoch))
    report["plex_livetv"] = await _enqueue_plex(db, change_id)
    report["restage_draft"] = await _keep_draft(db, ch, actor_user_id)
    _log.info("tv_rollback_committed", change_id=change_id, gen=prev.sched_gen, reason=reason)
    return report


async def _keep_draft(db: Any, ch: Any, actor: str | None) -> str | None:
    """A new draft with the rolled-back change's edits, so it can be staged again."""
    pending = ch.pending or {}
    if not pending or await open_draft(db, "restage") is not None:
        return None
    try:
        return await create_change(
            db, state="draft", title=f"Re-stage #{ch.row.get('number') or ''}".strip(),
            source={"kind": "restage", "change_id": ch.id}, pending=pending, created_by=actor,
        )  # fmt: skip
    except Exception:  # noqa: BLE001 - the rollback stands without it
        _log.exception("tv_rollback_restage_failed", change_id=ch.id)
        return None
