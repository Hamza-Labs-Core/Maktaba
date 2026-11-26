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

``ffmpeg_rc``, ``db_error`` and the other plugin causes are left out: they are as often a bug
or a bad file. So are the stages that run once by design (``max_attempts`` 1), those marked
``external_write`` (a conversion does not start again by itself days later) and the retired.

What is done with each:

* its file is there now, or its cause says nothing of the file (the database's): the stage is
  queued again at :data:`HEAL_PRIORITY`, asked what the failed job was asked (its payload and
  force), behind every other work (the claim's fair share gives a class at 950 about one claim
  for every ten at 100, so it neither holds normal work up nor starves), and what follows it
  keeps that priority (:mod:`.propagate`);
* its file is still gone: left failed, and not looked at again before as long as it has been
  failed (at least an hour, at most :data:`GONE_MAX`; :data:`UNSAID_MAX` when none of the
  library's roots holds the path, where an unmounted share cannot be told from a missing file);
* its root does not answer: not taken at all until it does;
* the look ran out of time at a file: it and the others of its folder are put off an hour, the
  ones not asked about stay due.

A stage is healed at most ``len(WAITS)`` times for an item since it last succeeded, each
:data:`WAITS` after the failure, so an item that is there and keeps failing stops coming
back. A stage with :data:`BATCH` such jobs still to run gets no more, so a large backlog
is fed in as it drains.

When a failed row is looked at again is its ``not_before``: a terminal failure leaves it
NULL, and nothing claims a failed row. So a round skips what is not due in SQL, whichever
process leads and however often it restarts.

A look is one ``stat`` a file; a path that is not ASCII and not there is also looked for in its
folder's listing (:func:`~.paths.find_existing`: up to 20 s in a folder that hangs, the listing
kept 5 minutes). It is one thread for at most :data:`LOOK_S`. A ``stat`` that never returns
keeps that thread, and no file is looked at again in this process until it does (a restart
ends it); the database's causes go on.
"""

from __future__ import annotations

import asyncio
import bisect
import threading
import time
from collections.abc import Iterable, Mapping
from datetime import datetime, timedelta
from typing import Any
from uuid import UUID

from ..log import get_logger
from .failure_classes import SHARE
from .failure_policy import _norm, _roots, root_of
from .missing_files import _exists, file_found
from .paths import track_unavailable
from .requeue import HEAL_PRIORITY, requeue_stage
from .retired import RETIRED_STAGES
from .sql import is_pg, loads, now, parse_ts, to_ts, uid

__all__ = ["HEAL_WHY", "heal_due", "heal_once"]

_log = get_logger()

#: how often a process looks (s), how many failures at once, and how long the share is asked
EVERY_S = 600.0
BATCH = 250
LOOK_S = 20.0
#: after the failure: the first, second and third time an item's stage is queued again
WAITS = (timedelta(hours=1), timedelta(days=1), timedelta(days=7))
#: the shortest wait before a file is looked for again
AGAIN = timedelta(hours=1)
#: the longest one for a file that is still gone, and when that cannot be said
GONE_MAX = timedelta(days=7)
UNSAID_MAX = timedelta(days=1)
#: healed :data:`WAITS` times, or not queued after all: left alone for this long
LEFT = timedelta(days=30)

#: the reason of the jobs this queues; what counts an item's healed retries
HEAL_WHY = "what made it fail has passed"

#: the causes a look at the file answers
_FILE = """(j.error_cause LIKE 'file_missing%'
       OR (j.failure_class = 'transient'
           AND j.error_cause IN ('nas_timeout', 'nas_partial_read', 'nas_invalid_argument')))"""
#: the causes that say nothing of the file
_DATABASE = """(j.error_cause LIKE 'DatabaseUnavailable:%'
       OR j.error_cause LIKE 'plugin_tx:%'
       OR (j.error_cause LIKE 'plugin_database:%'
           AND (j.error_cause LIKE '%bad connection%'
                OR j.error_cause LIKE '%assign requested address%'
                OR j.error_cause LIKE '%connection refused%'
                OR j.error_cause LIKE '%connection reset%'
                OR j.error_cause LIKE '%terminating connection%'
                OR j.error_cause LIKE '%violates foreign key constraint%')))"""

#: an open failure of a video; no ORDER BY: any of them will do, and ordering would read
#: every candidate
_OPEN = f"""
SELECT j.id, j.stage, j.subject_id, j.force, j.force_cascade, j.payload, j.finished_at,
       v.path, v.missing_confirmed_at, l.roots,
       CASE WHEN {_DATABASE} THEN 1 ELSE 0 END AS by_database,
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
 WHERE j.state = 'failed' AND j.max_attempts > 1 AND j.stage <> 'access_check'
   AND (j.not_before IS NULL OR j.not_before <= $2)
   AND s.enabled AND NOT s.external_write
   AND v.deleted_at IS NULL AND v.state <> 'superseded' AND l.deleted_at IS NULL
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

#: when this process last looked (monotonic), whether its last look is still hanging,
#: and where the next look starts (rotates past slow folders so other shares are not starved)
_last: list[float] = []
_looking = threading.Event()
_look_after: list[str] = []

_ASCII_LOWER = str.maketrans("ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz")


def _ascii_lower(text: str) -> str:
    """Fold ASCII capitals only, matching SQLite and PostgreSQL ``--locale=C`` ``lower()``."""
    return text.translate(_ASCII_LOWER)


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
    t = at or now()
    look: list[tuple[Any, datetime]] = []
    healed: dict[str, int] = {}
    for r in await _candidates(db, health, t):
        failed = parse_ts(r["finished_at"]) or t - LEFT
        n = int(r["healed"])
        if n >= len(WAITS):
            await _leave(db, r, t + LEFT)
        elif failed + WAITS[n] > t:
            await _leave(db, r, failed + WAITS[n])
        elif r["by_database"]:
            await _requeue(db, r, t, healed)
        elif (root := root_of(str(r["path"]), _roots(r["roots"]))) is not None and (
            health.get(f"{SHARE}:{root}", (True, ""))[0] is False
        ):  # SQLite and bundled PostgreSQL (--locale=C) lower() fold ASCII only
            await _leave(db, r, t + AGAIN)
        else:
            look.append((r, failed))
    there, slow = await _look({str(r["path"]) for r, _ in look})
    for r, failed in look:
        path = str(r["path"])
        if path in there:
            found = there[path]
            if found:
                await _requeue(db, r, t, healed)
            elif found is False:
                # with no root that holds it (a root stored in another form than the path), an
                # unmounted share looks like a missing file: nothing is said, so not that long
                gone = GONE_MAX if root_of(path, _roots(r["roots"])) else UNSAID_MAX
                await _leave(db, r, t + min(max(t - failed, AGAIN), gone))
            else:
                # listing unavailable (hung share) or stat error: not confirmed gone
                await _leave(db, r, t + AGAIN)
        elif slow is not None and _folder(path) == _folder(slow):
            await _leave(db, r, t + AGAIN)
        # else: not asked about; it is due still
    if healed:
        _log.info("failures_healed", stages=healed)
    return sum(healed.values())


async def _candidates(db: Any, health: Mapping[str, tuple[bool, str]], t: datetime) -> list[Any]:
    """At most :data:`BATCH` failures that may be due: not of a stage that has enough to do,
    and none a look could not answer now (its root is down, or the last look hangs on the
    share still), which would take the places of the others."""
    full = [str(r["stage"]) for r in await db.fetch(_FULL, HEAL_PRIORITY, BATCH)]
    args: list[Any] = [f"%{HEAL_WHY}%", to_ts(db, t)]

    def arg(value: Any) -> str:
        args.append(value)
        return f"${len(args)}"

    stages = ", ".join(arg(s) for s in (*full, *sorted(RETIRED_STAGES)))
    if _looking.is_set():
        cause = _DATABASE
    else:
        down: list[str] = []
        for name, probe in sorted(health.items()):
            if not (name.startswith(SHARE + ":") and not probe[0]):
                continue
            raw = name[len(SHARE) + 1 :].replace("\\", "/").rstrip("/")
            if not raw:
                continue
            # both full-folded (UTF-8 Postgres) and ASCII-folded (SQLite, bundled Postgres
            # --locale=C) so a down root with non-ASCII capitals is excluded in SQL too
            for prefix in dict.fromkeys((_norm(raw) + "/", _ascii_lower(raw) + "/")):
                down.append(prefix)
        under = "".join(
            f" AND substr(lower(replace(v.path, '\\', '/')), 1, {len(d)}) <> {arg(d)}" for d in down
        )
        cause = f"({_DATABASE} OR ({_FILE}{under}))"
    # SQLite takes subject_kind's index (the whole table) unless told not to
    kind = "j.subject_kind" if is_pg(db) else "+j.subject_kind"
    rows: list[Any] = await db.fetch(
        f"{_OPEN}   AND {kind} = 'video' AND j.stage NOT IN ({stages})\n"
        f"   AND {cause}\n LIMIT {BATCH}",
        *args,
    )
    return rows


def _folder(path: str) -> str:
    return path.replace("\\", "/").rpartition("/")[0]


async def _look(paths: Iterable[str]) -> tuple[dict[str, bool | None], str | None]:
    """Which of these files are there, as far as :data:`LOOK_S` reached (a file not in the
    answer was not asked about; None means an unavailable listing or stat error, not gone),
    and the one it was asking about when the time was up. One thread; a ``stat`` that hangs
    keeps it, and no further look starts until it returns."""
    todo = sorted(paths)
    if not todo:
        return {}, None
    if _look_after:
        idx = bisect.bisect_right(todo, _look_after[0])
        if idx < len(todo):
            todo = todo[idx:] + todo[:idx]
    found: dict[str, bool | None] = {}
    asking: list[str] = []
    stop = threading.Event()

    def run() -> None:
        try:
            for p in todo:
                asking[:] = [p]
                if stop.is_set():
                    return
                try:
                    with track_unavailable() as unavailable:
                        ok = _exists(p)
                except Exception:  # noqa: BLE001 - one path's error must not stop the round
                    _log.warning("failures_heal_stat_failed", path=p, exc_info=True)
                    found[p] = None
                else:
                    found[p] = True if ok else (None if unavailable[0] else False)
        finally:
            _looking.clear()

    _looking.set()
    slow: str | None = None
    try:
        await asyncio.wait_for(asyncio.to_thread(run), LOOK_S)
    except TimeoutError:
        slow = next(iter(asking), None)
        _log.warning("failures_heal_look_slow", asked=len(found), of=len(todo), at=slow)
    finally:
        stop.set()
        if not asking:  # no thread was free in time: the look was cancelled
            _looking.clear()
    if slow is not None:
        _look_after[:] = [slow]
    elif found:
        _look_after[:] = [todo[-1]]
    return dict(found), slow


async def _requeue(db: Any, r: Any, t: datetime, healed: dict[str, int]) -> None:
    """Queue the failed row's stage again; one that cannot be is left, the others go on."""
    stage, vid = str(r["stage"]), UUID(str(r["subject_id"]))
    try:
        if not r["by_database"] and r["missing_confirmed_at"] is not None:
            await file_found(db, vid)
        # as it was asked for (``force`` too): a stage that succeeded before with the same
        # input is answered from that success, not run once more
        res = await requeue_stage(
            db, vid, stage, HEAL_WHY, failed=r, force=bool(r["force"]), priority=HEAL_PRIORITY
        )
    except Exception:  # noqa: BLE001 - one row must not stop the batch
        _log.exception("failure_heal_failed", job_id=int(r["id"]), stage=stage)
        await _leave(db, r, t + UNSAID_MAX)
        return
    if res.job_id is None:
        await _leave(db, r, t + LEFT)
    else:
        payload = loads(r["payload"])
        if (
            res.outcome == "inserted"
            and stage in ("transcribe", "second_listen")
            and isinstance(payload, dict)
            and payload.get("relisten")
        ):
            await _count_relisten(db, vid, res.job_id, t)
        healed[stage] = healed.get(stage, 0) + 1


async def _count_relisten(db: Any, video_id: UUID, job_id: int, t: datetime) -> None:
    """Count a healed relisten job in ``relisten_attempts`` so its try and wait are tracked."""
    ts = to_ts(db, t)
    nxt = to_ts(db, t + AGAIN)
    await db.execute(
        "INSERT INTO relisten_attempts"
        " (video_id, attempts, last_job_id, last_at, next_at, ended_at, wait_s, stopped_at)"
        " VALUES ($1, 1, $2, $3, $4, NULL, NULL, NULL)"
        " ON CONFLICT (video_id) DO UPDATE SET"
        " attempts = relisten_attempts.attempts + 1,"
        " last_job_id = EXCLUDED.last_job_id,"
        " last_at = EXCLUDED.last_at,"
        " next_at = EXCLUDED.next_at,"
        " ended_at = NULL,"
        " wait_s = NULL,"
        " stopped_at = NULL",
        uid(video_id),
        job_id,
        ts,
        nxt,
    )


async def _leave(db: Any, r: Any, until: datetime) -> None:
    """Not looked at again before ``until``."""
    await db.execute(
        "UPDATE processing_jobs SET not_before = $2 WHERE id = $1 AND state = 'failed'",
        int(r["id"]),
        to_ts(db, until),
    )
