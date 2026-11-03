"""SIGTERM-driven graceful shutdown for the pipeline worker (Story 6.8).

The whole queue layer must shut down cleanly on ``SIGTERM`` so that no
running job is forgotten in ``claimed`` / ``running`` / ``resuming``.
The protocol from architecture §7.8:

1. First signal sets a global :class:`asyncio.Event`. The claim loop
   stops accepting new claims (it observes the event between iterations).
2. Mark every in-flight row owned by this worker with
   ``pause_requested = true`` in a single UPDATE keyed on
   ``claimed_by = $worker_id``.
3. Poll until all those rows have transitioned to ``paused`` (the
   per-segment cooperative check in
   :mod:`maktaba_pipeline.pipeline.control` honours the flag) OR
   ``shutdown_grace_sec`` (default 120 s) has elapsed.
4. Force-pause any stragglers — same UPDATE the API's
   ``?force=true`` endpoint runs, restricted to this worker's rows.
5. Cancel in-flight asyncio tasks; close the DB pool; exit 0.

Second SIGTERM short-circuits to :func:`os._exit(130)` — at that point
the operator has lost patience and the reaper (Story 6.6) is the
cleanup path.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import signal
from collections.abc import Callable, Collection
from typing import Any

from ..log import get_logger
from .signals import install_shutdown_handlers

__all__ = [
    "DEFAULT_SHUTDOWN_GRACE_SEC",
    "ShutdownOrchestrator",
    "drain_stages",
    "release_claims",
]


_log = get_logger()


DEFAULT_SHUTDOWN_GRACE_SEC: float = 120.0


_PAUSE_CLAIMED_PG = """
UPDATE processing_jobs
   SET pause_requested = true
 WHERE claimed_by = $1
   AND state IN ('claimed', 'running', 'resuming')
RETURNING id
"""

_PAUSE_CLAIMED_SQLITE = """
UPDATE processing_jobs
   SET pause_requested = 1
 WHERE claimed_by = ?
   AND state IN ('claimed', 'running', 'resuming')
RETURNING id
"""

_COUNT_REMAINING_PG = """
SELECT count(*) AS n FROM processing_jobs
 WHERE claimed_by = $1
   AND state IN ('claimed', 'running', 'resuming')
"""

_COUNT_REMAINING_SQLITE = """
SELECT count(*) AS n FROM processing_jobs
 WHERE claimed_by = ?
   AND state IN ('claimed', 'running', 'resuming')
"""

_FORCE_PAUSE_PG = """
UPDATE processing_jobs
   SET state            = 'paused',
       paused_at        = now(),
       paused_at_sec    = last_segment_end_sec,
       paused_reason    = 'shutdown',
       pause_requested  = false,
       claimed_by       = NULL
 WHERE claimed_by = $1
   AND state IN ('claimed', 'running', 'resuming')
RETURNING id
"""

_FORCE_PAUSE_SQLITE = """
UPDATE processing_jobs
   SET state            = 'paused',
       paused_at        = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
       paused_at_sec    = last_segment_end_sec,
       paused_reason    = 'shutdown',
       pause_requested  = 0,
       claimed_by       = NULL
 WHERE claimed_by = ?
   AND state IN ('claimed', 'running', 'resuming')
RETURNING id
"""


# the stage-restricted twins of the statements above, for a component's drain (Epic 51,
# story 51.06): the same flag, the same force-pause (with the drain's reason), only the
# rows of the stages that stopped. $1 the worker, $2 the stages (a JSON list on SQLite)
_STAGES_PG = (
    "claimed_by = $1 AND stage = ANY($2::text[]) AND state IN ('claimed', 'running', 'resuming')"
)
_STAGES_SQLITE = (
    "claimed_by = $1 AND stage IN (SELECT value FROM json_each($2))"
    " AND state IN ('claimed', 'running', 'resuming')"
)

_DRAIN_ASK_PG = f"UPDATE processing_jobs SET pause_requested = true WHERE {_STAGES_PG} RETURNING id"
_DRAIN_ASK_SQLITE = (
    f"UPDATE processing_jobs SET pause_requested = 1 WHERE {_STAGES_SQLITE} RETURNING id"
)
_DRAIN_COUNT_PG = f"SELECT count(*) AS n FROM processing_jobs WHERE {_STAGES_PG}"
_DRAIN_COUNT_SQLITE = f"SELECT count(*) AS n FROM processing_jobs WHERE {_STAGES_SQLITE}"
_DRAIN_FORCE_PG = _FORCE_PAUSE_PG.replace("'shutdown'", "$3").replace(
    "WHERE claimed_by = $1\n   AND state IN ('claimed', 'running', 'resuming')",
    "WHERE " + _STAGES_PG,
)
_DRAIN_FORCE_SQLITE = _FORCE_PAUSE_SQLITE.replace("'shutdown'", "$3").replace(
    "WHERE claimed_by = ?\n   AND state IN ('claimed', 'running', 'resuming')",
    "WHERE " + _STAGES_SQLITE,
)
_DRAIN_UNASK_PG = f"UPDATE processing_jobs SET pause_requested = false WHERE {_STAGES_PG}"
_DRAIN_UNASK_SQLITE = f"UPDATE processing_jobs SET pause_requested = 0 WHERE {_STAGES_SQLITE}"


class ShutdownOrchestrator:
    """SIGTERM/SIGINT trap + graceful drain of in-flight jobs.

    Construct one per worker process. Call :meth:`install` on the
    main loop to wire the signal handlers, then await
    :meth:`run_after_signal` (typically alongside the claim loop's
    own task). The orchestrator exits when all the worker's rows
    have reached ``paused`` or ``shutdown_grace_sec`` has elapsed.
    """

    def __init__(
        self,
        db: Any,
        *,
        worker_id: str,
        grace_sec: float = DEFAULT_SHUTDOWN_GRACE_SEC,
        poll_sec: float = 1.0,
    ) -> None:
        if grace_sec < 0:
            raise ValueError("grace_sec must be >= 0")
        if poll_sec <= 0:
            raise ValueError("poll_sec must be > 0")
        self.db = db
        self.worker_id = worker_id
        self.grace_sec = grace_sec
        self.poll_sec = poll_sec
        self.shutdown_event = asyncio.Event()
        self._signal_count = 0
        self._loop: asyncio.AbstractEventLoop | None = None
        self._uninstall: Callable[[], None] | None = None

    # ---- signal wiring ----------------------------------------------------

    def install(self, loop: asyncio.AbstractEventLoop | None = None) -> None:
        """Wire the platform's shutdown signals to :meth:`_on_signal`.

        SIGTERM and SIGINT on POSIX; SIGINT, SIGBREAK and SIGTERM on
        Windows (see :mod:`maktaba_pipeline.pipeline.signals`). Signals
        the platform lacks are skipped and installation never blocks.
        Re-installing replaces the existing handlers; :meth:`uninstall`
        restores the previous ones.
        """
        self.uninstall()
        self._loop = loop or asyncio.get_event_loop()
        self._uninstall = install_shutdown_handlers(self._loop, self._on_signal)

    def uninstall(self) -> None:
        """Restore the signal handlers that were active before :meth:`install`."""
        uninstall, self._uninstall = self._uninstall, None
        if uninstall is not None:
            uninstall()

    def _on_signal(self, sig: signal.Signals) -> None:
        self._signal_count += 1
        _log.info(
            "shutdown_signal_received",
            signal=sig.name,
            count=self._signal_count,
        )
        if self._signal_count >= 2:
            self._force_exit(sig)
            return
        self.shutdown_event.set()

    def _force_exit(self, sig: signal.Signals) -> None:
        _log.warning(
            "shutdown_forced_by_second_signal",
            signal=sig.name,
        )
        if self._loop is not None:
            for task in asyncio.all_tasks(self._loop):
                task.cancel()
        # os._exit, not sys.exit — we do not want atexit handlers
        # (or pytest's loop-cleanup) to run the orchestration logic
        # again. The reaper sweeps any orphans within stale_claim_sec.
        os._exit(130)

    # ---- orchestration ----------------------------------------------------

    async def run_after_signal(self) -> dict[str, int]:
        """Wait for shutdown, then drain. Returns ``{cooperative, forced}`` counts."""
        await self.shutdown_event.wait()
        return await self.drain()

    async def drain(self) -> dict[str, int]:
        """Run the four-step drain. Public so tests can call without a signal.

        Returns a dict with ``cooperative`` (rows that paused on their
        own within the grace window) and ``forced`` (rows we had to
        force-pause). The two add up to the row count we asked to
        pause in step 2.
        """
        _log.info(
            "shutdown_orchestration_started",
            worker_id=self.worker_id,
            grace_sec=self.grace_sec,
        )

        # Step 2: ask all our in-flight rows to pause cooperatively.
        if getattr(self.db, "dialect", "sqlite") == "postgres":
            sql = _PAUSE_CLAIMED_PG
        else:
            sql = _PAUSE_CLAIMED_SQLITE
        asked = await _fetch_all(self.db, sql, self.worker_id)
        n_asked = len(asked)
        _log.info(
            "shutdown_pause_requested_for_in_flight",
            count=n_asked,
            worker_id=self.worker_id,
        )
        if n_asked == 0:
            return {"cooperative": 0, "forced": 0}

        # Step 3: poll until count == 0 or grace elapses.
        loop = asyncio.get_event_loop()
        deadline = loop.time() + self.grace_sec
        remaining = n_asked
        while True:
            remaining = await self._count_remaining()
            if remaining == 0:
                _log.info(
                    "shutdown_all_paused_cleanly",
                    worker_id=self.worker_id,
                    cooperative=n_asked,
                )
                return {"cooperative": n_asked, "forced": 0}
            if loop.time() >= deadline:
                break
            # Sleep up to poll_sec, but no longer than the remaining
            # grace window — keeps the worst-case drain time bounded
            # by ``grace_sec + poll_sec``.
            sleep_for = min(self.poll_sec, max(0.0, deadline - loop.time()))
            await asyncio.sleep(sleep_for)

        # Step 4: force-pause stragglers.
        if getattr(self.db, "dialect", "sqlite") == "postgres":
            forced_sql = _FORCE_PAUSE_PG
        else:
            forced_sql = _FORCE_PAUSE_SQLITE
        forced = await _fetch_all(self.db, forced_sql, self.worker_id)
        n_forced = len(forced)
        _log.warning(
            "shutdown_force_paused_after_grace",
            count=n_forced,
            worker_id=self.worker_id,
            ids=[int(r["id"]) for r in forced if r is not None],
        )
        return {"cooperative": n_asked - n_forced, "forced": n_forced}

    async def _count_remaining(self) -> int:
        sql = (
            _COUNT_REMAINING_PG
            if getattr(self.db, "dialect", "sqlite") == "postgres"
            else _COUNT_REMAINING_SQLITE
        )
        row = await self.db.fetchrow(sql, self.worker_id)
        if row is None:
            return 0
        return int(row["n"])


async def release_claims(db: Any, worker_id: str) -> list[int]:
    """Put every row ``worker_id`` still holds back to ``paused`` at once (the force step
    above, without the cooperative wait); the ids. For a worker with nothing in flight:
    what is still claimed by it is left over (a node whose jobs ended while it could not
    reach the home's database), and the lease would release it only later."""
    sql = _FORCE_PAUSE_PG if getattr(db, "dialect", "sqlite") == "postgres" else _FORCE_PAUSE_SQLITE
    rows = await _fetch_all(db, sql, worker_id)
    return [int(r["id"]) for r in rows if r is not None]


async def _fetch_all(db: Any, sql: str, *args: Any) -> list[Any]:
    """Run ``sql`` and return all rows, preferring ``db.fetch`` when present.

    The minimal :class:`maktaba_pipeline.db.jobs.DBConn` Protocol only
    exposes :func:`fetchrow`. The drain loop's UPDATE … RETURNING
    semantically returns N rows; when the DB wrapper exposes
    :func:`fetch` we use it. For the tests' fake DB we iterate
    fetchrow until it returns ``None``.
    """
    fetch = getattr(db, "fetch", None)
    if fetch is not None:
        return list(await fetch(sql, *args))
    # Fall back: call fetchrow once. The fake DB used in tests
    # returns either a single row or a list-shaped helper; this branch
    # is exercised by the unit tests' ``FakeDB`` which implements
    # fetch() for these queries.
    out = await db.fetchrow(sql, *args)
    if out is None:
        return []
    return [out]


def _stages_arg(db: Any, stages: Collection[str]) -> Any:
    names = sorted(set(stages))
    return names if getattr(db, "dialect", "sqlite") == "postgres" else json.dumps(names)


async def drain_stages(
    db: Any,
    *,
    worker_id: str,
    stages: Collection[str],
    grace_sec: float,
    reason: str = "component_off",
    poll_sec: float = 1.0,
    cancel: Callable[[Collection[str]], int] | None = None,
) -> dict[str, int]:
    """Drain this worker's jobs of ``stages`` (a component that stopped, Epic 51) the way
    :meth:`ShutdownOrchestrator.drain` drains all of them: ask them to pause, wait up to
    ``grace_sec`` for those that checkpoint, then force-pause the rest (state ``paused``,
    ``paused_reason`` = ``reason``, the checkpoint kept, no attempt counted: the claim of
    a paused job is not a new attempt) and cancel their tasks with ``cancel``. Jobs of the
    other stages are not touched. Returns ``{cooperative, forced}``.

    Cancelled meanwhile (the component came back within the grace), it clears the flag of
    the jobs still running, which carry on."""
    if not stages:
        return {"cooperative": 0, "forced": 0}
    pg = getattr(db, "dialect", "sqlite") == "postgres"
    arg = _stages_arg(db, stages)
    asked = await _fetch_all(db, _DRAIN_ASK_PG if pg else _DRAIN_ASK_SQLITE, worker_id, arg)
    n_asked = len(asked)
    if n_asked == 0:
        return {"cooperative": 0, "forced": 0}
    _log.info(
        "component_drain_started",
        worker_id=worker_id,
        stages=sorted(stages),
        jobs=n_asked,
        grace_sec=grace_sec,
    )
    loop = asyncio.get_event_loop()
    deadline = loop.time() + grace_sec
    try:
        while True:
            row = await db.fetchrow(_DRAIN_COUNT_PG if pg else _DRAIN_COUNT_SQLITE, worker_id, arg)
            if row is None or int(row["n"]) == 0:
                _log.info("component_drain_clean", worker_id=worker_id, jobs=n_asked)
                return {"cooperative": n_asked, "forced": 0}
            if loop.time() >= deadline:
                break
            await asyncio.sleep(min(poll_sec, max(0.0, deadline - loop.time())))
    except asyncio.CancelledError:
        with contextlib.suppress(Exception):
            await asyncio.shield(
                db.execute(_DRAIN_UNASK_PG if pg else _DRAIN_UNASK_SQLITE, worker_id, arg)
            )
        raise
    forced = await _fetch_all(
        db, _DRAIN_FORCE_PG if pg else _DRAIN_FORCE_SQLITE, worker_id, arg, reason
    )
    n_forced = len(forced)
    cancelled = cancel(stages) if cancel is not None and n_forced else 0
    _log.warning(
        "component_drain_forced",
        worker_id=worker_id,
        stages=sorted(stages),
        ids=[int(r["id"]) for r in forced if r is not None],
        cancelled=cancelled,
        after_sec=grace_sec,
    )
    return {"cooperative": n_asked - n_forced, "forced": n_forced}
