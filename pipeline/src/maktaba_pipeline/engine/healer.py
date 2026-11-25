"""Failures whose cause has passed come back by themselves (Epic 40, story 40.3).

A job that failed for good stays failed until someone retries it, and some causes pass: a
share that was mounted wrong, a runner that opened the stored path unmapped (fixed by
#1062, after 54,540 thumbnails of one home had failed as ``file_missing``), a NAS that was
busy for longer than a job's tries, a database that was restarted. A scan looks at missing
files again (:mod:`.missing_files`), but a library is scanned only when someone asks for it
or set an interval (``sweep_interval_sec`` is 0 by default), so this does not wait for one.

:func:`heal_once` runs in the queue-health leader round (:mod:`.watchers`), at most every
:data:`EVERY_S` in a process, on the share probes that round made. It takes at most
:data:`BATCH` videos' open failures (the latest job of the stage, not ignored by an admin,
the stage on) whose cause is one that passes (:data:`_PASSES`):

* ``file_missing``: the file could not be found;
* ``nas_timeout``, ``nas_partial_read``, ``nas_invalid_argument`` of class ``transient``: the
  tries are spent within about 16 minutes, a busy NAS is not always back by then;
* a database that closed the plugin's connection, or a row that went away under a job (a
  foreign key): nothing else queues those again.

``ffmpeg_rc``, ``db_error`` and the plugin causes are left out: they are as often a bug or a
bad file. So is a stage that runs once by design (``max_attempts`` 1).

What is done with each:

* its file is there now: the stage is queued again at :data:`HEAL_PRIORITY`, behind every
  other work (the claim's fair share gives a class at 950 about one claim for every ten at
  100, so it neither holds normal work up nor starves), and what follows it keeps that
  priority (:mod:`.propagate`);
* its file is still gone: left failed, and not looked at again before as long as it has been
  failed (at least an hour, at most :data:`GONE_MAX`);
* its root does not answer, or the look ran out of time: nothing is said; again in an hour.

A stage is healed at most ``len(WAITS)`` times for an item since it last succeeded, each
:data:`WAITS` after the failure, so an item that is there and keeps failing stops coming
back. A stage with :data:`BATCH` such jobs still to run gets no more, so a large backlog
is fed in as it drains.

When a failed row is looked at again is its ``not_before``: a terminal failure leaves it
NULL, and nothing claims a failed row. So a round skips what is not due in SQL, whichever
process leads and however often it restarts. Only files are asked about (one ``stat`` each),
in one thread, for at most :data:`LOOK_S`.
"""

from __future__ import annotations

import asyncio
import threading
import time
from collections.abc import Iterable, Mapping
from datetime import datetime, timedelta
from typing import Any
from uuid import UUID

from ..log import get_logger
from .failure_classes import SHARE
from .failure_policy import _roots, root_of
from .missing_files import _exists, file_found
from .requeue import HEAL_PRIORITY, requeue_stage
from .sql import now, parse_ts, to_ts

__all__ = ["HEAL_WHY", "heal_due", "heal_once"]

_log = get_logger()

#: how often a process looks (s), how many failures at once, and how long the share is asked
EVERY_S = 600.0
BATCH = 250
LOOK_S = 20.0
#: after the failure: the first, second and third time an item's stage is queued again
WAITS = (timedelta(hours=1), timedelta(days=1), timedelta(days=7))
#: a root that does not answer, a look that ran out of time
AGAIN = timedelta(hours=1)
#: the longest wait before a file that is still gone is looked for again
GONE_MAX = timedelta(days=7)
#: healed :data:`WAITS` times, or not queued after all: left alone for this long
LEFT = timedelta(days=30)

#: the reason of the jobs this queues; what counts an item's healed retries
HEAL_WHY = "what made it fail has passed"

_PASSES = """(j.error_cause LIKE 'file_missing%'
       OR (j.failure_class = 'transient'
           AND j.error_cause IN ('nas_timeout', 'nas_partial_read', 'nas_invalid_argument'))
       OR j.error_cause LIKE 'DatabaseUnavailable:%'
       OR j.error_cause LIKE 'plugin_tx:%'
       OR (j.error_cause LIKE 'plugin_database:%'
           AND (j.error_cause LIKE '%bad connection%'
                OR j.error_cause LIKE '%assign requested address%'
                OR j.error_cause LIKE '%connection refused%'
                OR j.error_cause LIKE '%connection reset%'
                OR j.error_cause LIKE '%terminating connection%'
                OR j.error_cause LIKE '%violates foreign key constraint%')))"""

#: no ORDER BY: any of them will do, and ordering would read every candidate
_OPEN = f"""
SELECT j.id, j.stage, j.subject_id, j.force, j.finished_at, v.path, v.missing_confirmed_at,
       l.roots,
       (SELECT count(*) FROM processing_jobs h
         WHERE h.subject_kind = 'video' AND h.subject_id = j.subject_id AND h.stage = j.stage
           AND CAST(h.reasons AS TEXT) LIKE $1
           AND NOT EXISTS (SELECT 1 FROM processing_jobs d
                            WHERE d.subject_kind = 'video' AND d.subject_id = j.subject_id
                              AND d.stage = j.stage AND d.state = 'done' AND d.id >= h.id)
       ) AS healed
  FROM processing_jobs j
  JOIN job_stages s ON s.stage = j.stage
  JOIN videos v ON v.id = j.subject_id
  JOIN libraries l ON l.id = v.library_id
 WHERE j.subject_kind = 'video' AND j.state = 'failed' AND j.max_attempts > 1
   AND j.stage <> 'access_check'
   AND {_PASSES}
   AND (j.not_before IS NULL OR j.not_before <= $2)
   AND s.enabled AND v.deleted_at IS NULL AND v.state <> 'superseded'
   AND l.deleted_at IS NULL
   AND NOT EXISTS (SELECT 1 FROM processing_jobs n
                    WHERE n.subject_kind = 'video' AND n.subject_id = j.subject_id
                      AND n.stage = j.stage AND n.id > j.id)
   AND NOT EXISTS (SELECT 1 FROM failure_ignores i
                    WHERE i.subject_kind = 'video' AND i.subject_id = j.subject_id
                      AND i.stage = j.stage)
"""

_FULL = """
SELECT stage FROM processing_jobs
 WHERE priority = $1 AND state IN ('pending','claimed','running','resuming','paused')
 GROUP BY stage HAVING count(*) >= $2
"""

#: when this process last looked (monotonic), and whether its last look is still hanging
_last: list[float] = []
_looking = threading.Event()


async def heal_due(db: Any, health: Mapping[str, tuple[bool, str]], t: datetime) -> int:
    """:func:`heal_once` when :data:`EVERY_S` passed since the last; how many it queued.
    Never raises: the health round goes on."""
    at = time.monotonic()
    if _last and at - _last[0] < EVERY_S:
        return 0
    _last[:] = [at]
    try:
        return await heal_once(db, health, t)
    except Exception:  # noqa: BLE001 - a repair must never stop the health round
        _log.exception("failures_heal_failed")
        return 0


async def heal_once(
    db: Any, health: Mapping[str, tuple[bool, str]], at: datetime | None = None
) -> int:
    """One batch; how many stages were queued again. ``health``: the round's probes."""
    if _looking.is_set():  # the last look hangs on the share still: not one more thread
        return 0
    t = at or now()
    full = [str(r["stage"]) for r in await db.fetch(_FULL, HEAL_PRIORITY, BATCH)]
    skip = ",".join(f"${i + 3}" for i in range(len(full)))
    rows = await db.fetch(
        _OPEN + (f"   AND j.stage NOT IN ({skip})\n" if full else "") + f" LIMIT {BATCH}",
        f"%{HEAL_WHY}%",
        to_ts(db, t),
        *full,
    )
    look = []
    for r in rows:
        failed = parse_ts(r["finished_at"]) or t - LEFT
        n = int(r["healed"])
        if n >= len(WAITS):
            await _leave(db, r, t + LEFT)
        elif failed + WAITS[n] > t:
            await _leave(db, r, failed + WAITS[n])
        elif _root_down(r, health):
            await _leave(db, r, t + AGAIN)
        else:
            look.append((r, failed))
    there = await _look({str(r["path"]) for r, _ in look})
    healed: dict[str, int] = {}
    for r, failed in look:
        found = there.get(str(r["path"]))
        if found is None:
            await _leave(db, r, t + AGAIN)
        elif not found:
            await _leave(db, r, t + min(max(t - failed, AGAIN), GONE_MAX))
        elif await _requeue(db, r):
            healed[str(r["stage"])] = healed.get(str(r["stage"]), 0) + 1
        else:
            await _leave(db, r, t + LEFT)
    if healed:
        _log.info("failures_healed", stages=healed)
    return sum(healed.values())


def _root_down(r: Any, health: Mapping[str, tuple[bool, str]]) -> bool:
    """The root that holds the file did not answer its probe in this round."""
    root = root_of(str(r["path"]), _roots(r["roots"]))
    probe = health.get(f"{SHARE}:{root}")
    return probe is not None and not probe[0]


async def _look(paths: Iterable[str]) -> dict[str, bool]:
    """Which of these files are there, as far as :data:`LOOK_S` reached (a file not in the
    answer was not asked about). One thread; a ``stat`` that hangs keeps it, and no further
    look starts until it returns."""
    todo = sorted(paths)
    if not todo:
        return {}
    found: dict[str, bool] = {}
    started, stop = threading.Event(), threading.Event()

    def run() -> None:
        started.set()
        try:
            for p in todo:
                if stop.is_set():
                    return
                found[p] = _exists(p)
        finally:
            _looking.clear()

    _looking.set()
    try:
        await asyncio.wait_for(asyncio.to_thread(run), LOOK_S)
    except TimeoutError:
        _log.warning("failures_heal_look_slow", asked=len(found), of=len(todo))
    finally:
        stop.set()
        if not started.is_set():  # no thread was free in time: the look was cancelled
            _looking.clear()
    return dict(found)


async def _requeue(db: Any, r: Any) -> bool:
    vid = UUID(str(r["subject_id"]))
    if r["missing_confirmed_at"] is not None:
        await file_found(db, vid)
    # as it was asked for (``force``): a stage that succeeded before with the same input is
    # answered from that success, not run once more
    res = await requeue_stage(
        db, vid, str(r["stage"]), HEAL_WHY, force=bool(r["force"]), priority=HEAL_PRIORITY
    )
    return res.job_id is not None


async def _leave(db: Any, r: Any, until: datetime) -> None:
    """Not looked at again before ``until``."""
    await db.execute(
        "UPDATE processing_jobs SET not_before = $2 WHERE id = $1 AND state = 'failed'",
        int(r["id"]),
        to_ts(db, until),
    )
