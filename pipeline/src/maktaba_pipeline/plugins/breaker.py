"""The plugins' circuit breaker (Epic 54, story 54.09 §2).

The dispatcher sees every plugin job's outcome, so it keeps each plugin's breaker, in
``plugin_health`` (one row per plugin, written in one statement per outcome so two jobs of
a plugin finishing together never lose one):

- ``recent``: the last 20 outcomes ``{at, cause, ms, memory}`` (``cause`` "" for ok);
- ``crashes``: the traps, timeouts and memory caps of the last 10 minutes;
- ``refusals``: the plugin API's refusals of the last 10 minutes, as ``{at, n}``.

It **trips** when one plugin has 5 traps/timeouts/memory caps in 10 minutes, more than half
of its last 20 jobs failed for a plugin cause (counted once 20 are there), or 50 API
refusals in 10 minutes. Tripping marks the row and asks the API to hold the plugin (only
the API writes desired state, Epic 51 H13): ``POST /internal/plugins/breaker`` with the
supervisor's per-boot local token. The hold has no end time; an admin's Start clears the
row (the API does), and the windows start again. Times are wall-clock seconds, so the
windows survive a restart of the worker.
"""

from __future__ import annotations

import asyncio
import json
import time
from typing import Any

from ..log import get_logger

__all__ = [
    "CRASH_CAUSES",
    "CRASH_LIMIT",
    "LAST_N",
    "REFUSAL_LIMIT",
    "WINDOW_S",
    "record",
    "should_trip",
]

_log = get_logger()

WINDOW_S = 600
CRASH_LIMIT = 5
LAST_N = 20
REFUSAL_LIMIT = 50
CRASH_CAUSES = frozenset({"plugin_trap", "plugin_timeout", "plugin_memory"})

_RECORD_PG = """
INSERT INTO plugin_health AS h (plugin_id, recent, crashes, refusals, jobs_done, jobs_failed,
    last_error_code, last_error_at, updated_at)
VALUES ($1, jsonb_build_array($2::jsonb),
    CASE WHEN $3 THEN jsonb_build_array($2::jsonb) ELSE '[]'::jsonb END,
    CASE WHEN $4 > 0 THEN jsonb_build_array(jsonb_build_object('at', $5::float8, 'n', $4))
        ELSE '[]'::jsonb END,
    CASE WHEN $6 = '' THEN 1 ELSE 0 END, CASE WHEN $6 = '' THEN 0 ELSE 1 END,
    $6, CASE WHEN $6 = '' THEN NULL ELSE now() END, now())
ON CONFLICT (plugin_id) DO UPDATE SET
    recent = (SELECT COALESCE(jsonb_agg(e ORDER BY o), '[]'::jsonb) FROM (
        SELECT e, o FROM jsonb_array_elements(h.recent || jsonb_build_array($2::jsonb))
            WITH ORDINALITY AS t(e, o) ORDER BY o DESC LIMIT $7) x),
    crashes = (SELECT COALESCE(jsonb_agg(e), '[]'::jsonb) FROM jsonb_array_elements(
        h.crashes || CASE WHEN $3 THEN jsonb_build_array($2::jsonb) ELSE '[]'::jsonb END) e
        WHERE (e->>'at')::float8 > $5::float8 - $8),
    refusals = (SELECT COALESCE(jsonb_agg(e), '[]'::jsonb) FROM jsonb_array_elements(
        h.refusals || CASE WHEN $4 > 0
            THEN jsonb_build_array(jsonb_build_object('at', $5::float8, 'n', $4))
            ELSE '[]'::jsonb END) e WHERE (e->>'at')::float8 > $5::float8 - $8),
    jobs_done = h.jobs_done + CASE WHEN $6 = '' THEN 1 ELSE 0 END,
    jobs_failed = h.jobs_failed + CASE WHEN $6 = '' THEN 0 ELSE 1 END,
    last_error_code = CASE WHEN $6 = '' THEN h.last_error_code ELSE $6 END,
    last_error_at = CASE WHEN $6 = '' THEN h.last_error_at ELSE now() END,
    updated_at = now()
RETURNING recent, crashes, refusals, tripped
"""

_TRIP_PG = """
UPDATE plugin_health SET tripped = true, tripped_at = now(), trip_reason = $2, updated_at = now()
WHERE plugin_id = $1 AND NOT tripped RETURNING plugin_id
"""


def _list(v: Any) -> list[dict[str, Any]]:
    if isinstance(v, str | bytes):
        v = json.loads(v)
    return list(v or [])


def should_trip(recent: list[dict[str, Any]], crashes: list[dict[str, Any]],
                refusals: list[dict[str, Any]], now: float) -> str:  # fmt: skip
    """Why the breaker trips ("" when it does not), from the windows as stored."""
    if sum(1 for c in crashes if float(c.get("at", 0)) > now - WINDOW_S) >= CRASH_LIMIT:
        return "crashes"
    if len(recent) >= LAST_N:
        failed = sum(1 for r in recent[-LAST_N:] if str(r.get("cause") or "").startswith("plugin_"))
        if failed * 2 > LAST_N:
            return "failures"
    refused = sum(int(r.get("n", 0)) for r in refusals if float(r.get("at", 0)) > now - WINDOW_S)
    if refused >= REFUSAL_LIMIT:
        return "refusals"
    return ""


async def record(
    db: Any,
    plugin_id: str,
    cause: str,
    *,
    ms: int = 0,
    memory: int = 0,
    refusals: int = 0,
    now: float | None = None,
) -> str:
    """Record one job's outcome (``cause`` "" for ok, else its failure cause) and the API
    refusals it met; trip the breaker when the windows say so. Answers why it tripped now
    ("" when it did not, or had already). Never raises: a breaker that cannot write must
    not fail the job."""
    if getattr(db, "dialect", "postgres") != "postgres":
        return ""  # plugins run on a Postgres home only (#693)
    at = time.time() if now is None else now
    entry = json.dumps({"at": at, "cause": cause, "ms": ms, "memory": memory})
    try:
        row = await db.fetchrow(
            _RECORD_PG,
            plugin_id,
            entry,
            cause in CRASH_CAUSES,
            refusals,
            at,
            cause,
            LAST_N,
            float(WINDOW_S),
        )
        if row is None:
            return ""
        if row["tripped"]:
            # tripped already: ask again (the hold may not have been written); the API
            # answers held false for a plugin it holds
            await _ask_hold(plugin_id, "")
            return ""
        why = should_trip(_list(row["recent"]), _list(row["crashes"]), _list(row["refusals"]), at)
        if not why:
            return ""
        if await db.fetchrow(_TRIP_PG, plugin_id, why) is None:
            return ""  # another job tripped it first
    except Exception:  # noqa: BLE001 - the job's outcome stands
        _log.exception("plugin_breaker_not_recorded", plugin=plugin_id)
        return ""
    _log.warning("plugin_breaker_tripped", plugin=plugin_id, reason=why)
    await _ask_hold(plugin_id, why)
    return why


async def _ask_hold(plugin_id: str, why: str) -> None:
    from .dispatch import _api

    try:
        status, body = await asyncio.to_thread(
            _api, "POST", "/internal/plugins/breaker", {"plugin_id": plugin_id, "reason": why}
        )
    except Exception as exc:  # noqa: BLE001 - the row says tripped; the next trip asks again
        _log.warning("plugin_breaker_hold_failed", plugin=plugin_id, error=str(exc))
        return
    if status != 200:
        _log.warning("plugin_breaker_hold_refused", plugin=plugin_id, status=status, body=body)
