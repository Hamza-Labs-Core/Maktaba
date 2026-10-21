"""What a failure does to its job (Epic 40): park, retry within the class budget, or fail.

:func:`decide` is called by ``mark_failed_or_retry`` on an engine connection, inside the
transaction that writes the failure. It reads the ``queue_health`` rules, classifies the
failure (:mod:`.failure_classes`), refines the class with what it can see from here (the
stage's model, the item's library root) and the dependency's last probe
(``dependency_health``), and answers:

* **park** - a dependency or a resource is down: the row goes back to ``pending`` with
  ``parked_on`` set and ``not_before`` at the ``park_max_hours`` backstop; the attempt the
  claim took is refunded. A dependency whose last probe said healthy is not the cause: the
  failure is then ``unknown`` (no endless park and release);
* **retry** - with the usual backoff, while ``attempts`` is under the class budget;
* **fail** - ``item_permanent``, a stage that said ``retryable=False``, or a spent budget.
"""

from __future__ import annotations

import asyncio
import os
import random
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import Any, Literal

from .causes import classify
from .failure_classes import (
    ITEM_PERMANENT,
    MODEL,
    SHARE,
    TRANSIENT,
    UNKNOWN,
    QueueHealthConfig,
    class_for,
    is_parking,
)
from .paths import to_local
from .sql import is_pg, loads, now, parse_ts, to_ts, uid

__all__ = [
    "Decision",
    "budget",
    "decide",
    "dependency_state",
    "library_root",
    "mark_unhealthy",
    "not_before",
    "read_config",
    "root_answers",
]


@dataclass(frozen=True, slots=True)
class Decision:
    failure_class: str
    action: Literal["park", "retry", "fail"]
    cause: str
    #: seconds until ``not_before`` (park: the backstop; retry: the backoff)
    delay_sec: float | None = None
    #: the row's ``max_attempts`` from now on: a class budget above the stage's own
    #: raises it (the table checks ``attempts <= max_attempts + 1``)
    max_attempts: int | None = None

    @property
    def parked_on(self) -> str | None:
        return self.failure_class if self.action == "park" else None


async def read_config(db: Any) -> QueueHealthConfig:
    from .. import rules

    cfg, _, _ = await rules.read(db, "queue_health")
    assert isinstance(cfg, QueueHealthConfig)
    return cfg


def budget(cls: str, max_attempts: int, cfg: QueueHealthConfig) -> int:
    """How many tries a failure of ``cls`` gets. A stage that runs once by design
    (``max_attempts`` 1) keeps its one try whatever the class."""
    if cls == ITEM_PERMANENT:
        return 0
    if cls == TRANSIENT and max_attempts > 1:
        return max(max_attempts, cfg.transient_attempts)
    return max_attempts


async def dependency_state(db: Any, name: str, fresh_sec: float) -> bool | None:
    """The dependency's last probe: True/False when it is younger than ``fresh_sec``,
    None when there is none."""
    row = await db.fetchrow(
        "SELECT healthy, checked_at FROM dependency_health WHERE name = $1", name
    )
    if row is None:
        return None
    at = parse_ts(row["checked_at"])
    if at is None or (now() - at).total_seconds() > fresh_sec:
        return None
    return bool(row["healthy"])


async def mark_unhealthy(db: Any, name: str, message: str) -> None:
    """Record that a job just ran into ``name`` (a resource's cooldown starts here)."""
    t = to_ts(db, now())
    await db.execute(
        "INSERT INTO dependency_health (name, healthy, message, checked_at, since)"
        " VALUES ($1, $2, $3, $4, $4) ON CONFLICT (name) DO UPDATE SET"
        " healthy = EXCLUDED.healthy, message = EXCLUDED.message,"
        " checked_at = EXCLUDED.checked_at,"
        " since = CASE WHEN dependency_health.healthy THEN EXCLUDED.since"
        " ELSE dependency_health.since END",
        name,
        False if is_pg(db) else 0,
        message[:300],
        t,
    )


def _roots(value: Any) -> tuple[str, ...]:
    if value is None:
        return ()
    if isinstance(value, list | tuple):
        return tuple(str(v) for v in value)
    parsed = loads(value)
    return tuple(str(v) for v in parsed) if isinstance(parsed, list) else ()


def _norm(p: str) -> str:
    return p.replace("\\", "/").rstrip("/").lower()


async def library_root(db: Any, video_id: Any) -> str | None:
    """The library root (as stored) that holds the video's file, or None."""
    if video_id is None:
        return None
    row = await db.fetchrow(
        "SELECT v.path AS path, l.roots AS roots FROM videos v"
        " JOIN libraries l ON l.id = v.library_id WHERE v.id = $1",
        uid(video_id),
    )
    if row is None or row["path"] is None:
        return None
    path = _norm(str(row["path"]))
    best: str | None = None
    for root in _roots(row["roots"]):
        r = _norm(root)
        if r and (path == r or path.startswith(r + "/")) and (best is None or len(r) > len(best)):
            best = root
    return best


async def root_answers(root: str, timeout_s: float) -> bool:
    """The root's folder exists and lists on this machine within ``timeout_s`` (a dead SMB
    mount can hang a stat for minutes: a check that does not answer is a no)."""

    def look() -> bool:
        local = to_local(root)
        if not os.path.isdir(local):
            return False
        with os.scandir(local) as it:
            next(it, None)
        return True

    try:
        return await asyncio.wait_for(asyncio.to_thread(look), timeout=timeout_s)
    except (TimeoutError, OSError):
        return False


def _model_section(stage: str) -> str:
    from .graph import get_stage

    s = get_stage(stage)
    return (s.model_rules if s is not None else None) or "model"


async def decide(
    db: Any,
    *,
    stage: str,
    video_id: Any,
    kind: str,
    message: str,
    retryable: bool,
    attempts: int,
    max_attempts: int,
    rng: random.Random | None = None,
    cfg: QueueHealthConfig | None = None,
) -> Decision:
    cfg = cfg or await read_config(db)
    cause = classify(kind, message)
    cls = class_for(cause, cfg.class_overrides)
    if cls == MODEL:
        cls = f"{MODEL}:{_model_section(stage)}"
    elif cause.startswith("file_missing") and cls == ITEM_PERMANENT:
        root = await library_root(db, video_id)
        if root is not None and not await root_answers(root, cfg.probe_timeout_seconds):
            cls = f"{SHARE}:{root}"

    if is_parking(cls):
        park = timedelta(hours=cfg.park_max_hours).total_seconds()
        if cls.startswith("resource:"):
            await mark_unhealthy(db, cls, message)
            return Decision(cls, "park", cause, park)
        fresh = max(3.0 * cfg.probe_interval_seconds, 180.0)
        if await dependency_state(db, cls, fresh) is not True:
            return Decision(cls, "park", cause, park)
        # the dependency answers its probe: it is not why this job failed
        cls = UNKNOWN

    if cls == ITEM_PERMANENT or not retryable:
        return Decision(cls, "fail", cause)
    tries = budget(cls, max_attempts, cfg)
    if attempts < tries:
        from ..pipeline.backoff import compute_backoff

        delay = compute_backoff(max(1, attempts), rng=rng)
        return Decision(cls, "retry", cause, delay, max(tries, max_attempts))
    return Decision(cls, "fail", cause)


def not_before(decision: Decision, at: datetime) -> datetime | None:
    if decision.delay_sec is None:
        return None
    return at + timedelta(seconds=decision.delay_sec)
