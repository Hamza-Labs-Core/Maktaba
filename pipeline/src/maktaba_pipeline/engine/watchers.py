"""Dependency watchers (Epic 40, story 40.2): small health probes that release parked jobs.

One loop on the home's leader worker (``pg_try_advisory_xact_lock('maktaba.queue_health')``;
on SQLite there is one process), every ``rules.queue_health.probe_interval_seconds``:

* ``dependency:model:<section>`` - every rules section a stage names in ``model_rules``:
  ``GET <url>/api/tags`` (Ollama) answers, and lists the model when it lists any. A section
  that is switched off is healthy: its stages do not ask the model;
* ``dependency:share:<root>`` - every library root lists on this machine;
* ``resource:disk`` - the temporary and data folders have ``min_free_disk_gb`` free;
* ``resource:shm`` - Postgres' shared memory cannot be seen from here: healthy again
  ``resource_cooldown_minutes`` after the last job ran out of it.

Each result is upserted into ``dependency_health``; for every healthy one the jobs parked
on it are released (``parked_on = NULL, not_before = now``), in batches. A job parked on a
name nobody probes any more (a deleted library's root) waits for the ``park_max_hours``
backstop.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import shutil
import tempfile
import urllib.error
import urllib.request
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass, field
from datetime import datetime, timedelta
from typing import Any

from ..log import get_logger
from .failure_classes import MODEL, SHARE, QueueHealthConfig
from .failure_policy import _roots, read_config, root_answers
from .graph import StageDef
from .sql import is_pg, now, parse_ts, to_ts

__all__ = [
    "Probe",
    "WatchReport",
    "model_probe",
    "release",
    "run_watchers",
    "watch_once",
]

_log = get_logger()

RELEASE_BATCH = 1000

#: (healthy, message)
Probe = tuple[bool, str]
ModelFetch = Callable[[str, float], bytes]


@dataclass(frozen=True, slots=True)
class WatchReport:
    leader: bool
    health: dict[str, Probe] = field(default_factory=dict)
    released: dict[str, int] = field(default_factory=dict)


def _fetch(url: str, timeout: float) -> bytes:
    with urllib.request.urlopen(url, timeout=timeout) as r:  # noqa: S310 - the admin's model
        data: bytes = r.read()
        return data


_fetcher: ModelFetch = _fetch


def set_model_fetcher(fn: ModelFetch | None) -> ModelFetch:
    """Replace the HTTP GET of the model probe (tests); None restores it."""
    global _fetcher
    previous = _fetcher
    _fetcher = fn or _fetch
    return previous


async def model_probe(url: str, model: str, timeout: float) -> Probe:
    """Does the model server at ``url`` answer, with ``model`` among its models?"""
    tags = url.rstrip("/") + "/api/tags"
    try:
        body = await asyncio.wait_for(asyncio.to_thread(_fetcher, tags, timeout), timeout + 1)
    except (TimeoutError, OSError, urllib.error.URLError, ValueError) as e:
        return False, f"{url} is not answering ({type(e).__name__}: {e})"[:300]
    try:
        doc = json.loads(body.decode("utf-8"))
    except (ValueError, UnicodeDecodeError):
        return True, f"{url} answers"
    names = [
        str(m.get("name") or m.get("model") or "")
        for m in (doc.get("models") or [] if isinstance(doc, dict) else [])
        if isinstance(m, dict)
    ]
    if names and model and not any(n == model or n.split(":")[0] == model for n in names):
        return False, f"{url} answers but does not have the model {model}"[:300]
    return True, f"{url} answers"


async def _model_probes(
    db: Any, graph: Mapping[str, StageDef], cfg: QueueHealthConfig
) -> dict[str, Probe]:
    from .. import rules

    out: dict[str, Probe] = {}
    for section in sorted({s.model_rules for s in graph.values() if s.model_rules}):
        try:
            mcfg, _, _ = await rules.read(db, section)
        except KeyError:  # a section this code does not know
            continue
        name = f"{MODEL}:{section}"
        url = str(getattr(mcfg, "url", "") or "")
        if not getattr(mcfg, "enabled", True) or not url:
            out[name] = (True, "switched off: its stages do not ask the model")
            continue
        out[name] = await model_probe(
            url, str(getattr(mcfg, "model", "")), cfg.probe_timeout_seconds
        )
    return out


async def _share_probes(db: Any, cfg: QueueHealthConfig) -> dict[str, Probe]:
    roots: set[str] = set()
    for r in await db.fetch("SELECT roots FROM libraries WHERE deleted_at IS NULL"):
        roots.update(_roots(r["roots"]))
    out: dict[str, Probe] = {}
    for root in sorted(roots):
        ok = await root_answers(root, cfg.probe_timeout_seconds)
        out[f"{SHARE}:{root}"] = (ok, "answers" if ok else f"{root} is offline or not mounted")
    return out


def _data_dirs() -> list[str]:
    dirs = [tempfile.gettempdir()]
    for env in ("MAKTABA_DATA_DIR", "MAKTABA_HOME"):
        if (d := os.environ.get(env)) and os.path.isdir(d):
            dirs.append(d)
    return dirs


def _disk_probe(cfg: QueueHealthConfig) -> Probe:
    need = cfg.min_free_disk_gb * 1024**3
    for d in _data_dirs():
        try:
            free = shutil.disk_usage(d).free
        except OSError as e:
            return False, f"{d}: {e}"[:300]
        if free < need:
            return False, f"{d} has {free / 1024**3:.1f} GB free, {cfg.min_free_disk_gb:g} needed"
    return True, "enough free space"


async def _shm_probe(db: Any, cfg: QueueHealthConfig, at: datetime) -> Probe | None:
    """Healthy once the cooldown passed since the last job ran out of shared memory; None
    while nothing ever did (no row to write)."""
    row = await db.fetchrow(
        "SELECT healthy, checked_at FROM dependency_health WHERE name = 'resource:shm'"
    )
    if row is None:
        return None
    if bool(row["healthy"]):
        return True, "no job ran out of shared memory recently"
    last = parse_ts(row["checked_at"]) or at
    if at - last >= timedelta(minutes=cfg.resource_cooldown_minutes):
        return True, "cooled down"
    return None  # still cooling down: keep the row as the failure wrote it


async def probe_all(
    db: Any, graph: Mapping[str, StageDef], cfg: QueueHealthConfig, at: datetime
) -> dict[str, Probe]:
    out = await _model_probes(db, graph, cfg)
    out.update(await _share_probes(db, cfg))
    out["resource:disk"] = await asyncio.to_thread(_disk_probe, cfg)
    shm = await _shm_probe(db, cfg, at)
    if shm is not None:
        out["resource:shm"] = shm
    return out


async def _write(db: Any, name: str, probe: Probe, at: datetime) -> None:
    healthy, message = probe
    t = to_ts(db, at)
    await db.execute(
        "INSERT INTO dependency_health (name, healthy, message, checked_at, since)"
        " VALUES ($1, $2, $3, $4, $4) ON CONFLICT (name) DO UPDATE SET"
        " message = EXCLUDED.message, checked_at = EXCLUDED.checked_at,"
        " since = CASE WHEN dependency_health.healthy = EXCLUDED.healthy"
        " THEN dependency_health.since ELSE EXCLUDED.since END,"
        " healthy = EXCLUDED.healthy",
        name,
        healthy if is_pg(db) else int(healthy),
        message[:300],
        t,
    )


async def release(db: Any, name: str, at: datetime | None = None) -> int:
    """Release the jobs parked on ``name`` (all of them, in batches); how many."""
    t = to_ts(db, at or now())
    total = 0
    while True:
        rows = await db.fetch(
            "UPDATE processing_jobs SET parked_on = NULL, not_before = $2"
            " WHERE id IN (SELECT id FROM processing_jobs WHERE state = 'pending'"
            f" AND parked_on = $1 ORDER BY id LIMIT {RELEASE_BATCH}) RETURNING id",
            name,
            t,
        )
        total += len(rows)
        if len(rows) < RELEASE_BATCH:
            return total


async def watch_once(
    db: Any,
    graph: Mapping[str, StageDef],
    *,
    cfg: QueueHealthConfig | None = None,
    at: datetime | None = None,
    probes: Callable[..., Awaitable[dict[str, Probe]]] | None = None,
) -> WatchReport:
    """One round: probe, record, release. ``leader=False``: another worker holds the
    round. On Postgres the whole round runs in one transaction holding the advisory lock
    (the probes are bounded by their timeout); SQLite has one process, so no lock and no
    long transaction there."""
    if not is_pg(db):
        return await _round(db, graph, cfg, at, probes)
    async with db.transaction():
        got = await db.fetchval(
            "SELECT pg_try_advisory_xact_lock(hashtext('maktaba.queue_health'))"
        )
        if not got:
            return WatchReport(leader=False)
        return await _round(db, graph, cfg, at, probes)


async def _round(
    db: Any,
    graph: Mapping[str, StageDef],
    cfg: QueueHealthConfig | None,
    at: datetime | None,
    probes: Callable[..., Awaitable[dict[str, Probe]]] | None,
) -> WatchReport:
    cfg = cfg or await read_config(db)
    t = at or now()
    health = await (probes or probe_all)(db, graph, cfg, t)
    released: dict[str, int] = {}
    for name, probe in sorted(health.items()):
        await _write(db, name, probe, t)
        if probe[0]:
            n = await release(db, name, t)
            if n:
                released[name] = n
    if released:
        _log.info("parked_jobs_released", released=released)
    return WatchReport(True, health, released)


async def run_watchers(db: Any, graph: Mapping[str, StageDef], stop: asyncio.Event) -> None:
    """Probe and release until ``stop``; never takes the worker down."""
    while not stop.is_set():
        interval = 60.0
        try:
            cfg = await read_config(db)
            interval = float(cfg.probe_interval_seconds)
            await watch_once(db, graph, cfg=cfg)
        except Exception:  # noqa: BLE001 - a probe round must never take the worker down
            _log.exception("queue_health_watch_failed")
        with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
            await asyncio.wait_for(stop.wait(), timeout=interval)
