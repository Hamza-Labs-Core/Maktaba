"""The home converts its stored paths to canonical share paths by itself (story 31.3).

Nobody runs ``storage canonicalize`` by hand any more. The home's agent
(:mod:`..cluster.node`) checks at start and with every storage check whether there
may be work (:func:`due`) and enqueues the home-only stage ``storage_canonicalize``
(a visible run in the queue); the stage calls :func:`run_once`, which:

1. **registers the storage servers** the home mounts and no record names
   (:mod:`.autoreg`; an admin's record is never changed);
2. **checks** that it can convert: every network mount resolves to a server, every
   library root still in local form is on a mounted share or exists here (a root
   that is gone is an unmounted share), the dry run's sampled paths open here
   (rules ``storage.allowed_missing``, 0 by default); otherwise it reports why
   (``blocked``) and tries again later;
3. adds the canonical entry for every legacy entry of the **Plex path map**
   (``/Volumes/Originals/=/volume1/Originals/`` -> also
   ``smb://home-nas/Originals/=/volume1/Originals/``);
4. **backs up** every path column inside the database (:mod:`.backup`; the last
   ``storage.backups_kept``);
5. **pauses only the stages that read or write the shares** (their pools,
   ``storage.pause_pools``: ``job_stages.paused`` with ``paused_by =
   'storage_canonicalize'``, so a stage the admin paused stays paused and only these are
   resumed) and waits for their running jobs (``storage.drain_wait_minutes``);
6. **applies** in chunks (one transaction each, ``storage.batch_size``) under one batch
   id, reporting progress;
7. **verifies**: a second dry run has nothing left, sampled converted paths open;
8. **resumes** the stages.

A run cut short (the worker stopped mid-way) keeps its phase, batch and backup in
``storage_canonicalize.state``: the agent enqueues the stage again and it carries on
with the same batch, so ``storage revert`` still undoes all of it; a revert cut short
is finished the same way (never taken for a conversion to carry on). Nothing runs while
a share is unmounted or a check fails. ``storage.auto_canonicalize`` (on by default)
switches all of it off.

Before a new conversion writes, the home's reported share map
(``instance_identity.storage.path_map``) must have named every share it converts to
for ``storage.map_settle_minutes``: the API and the streaming service read that map
every 5 minutes, and a canonical path written before they have it does not open there
(playback and live channels would fail until their next read). Until then the run
``waits`` and the agent looks again with its next storage check.

One run at a time: a Postgres advisory lock (a process-local lock on SQLite) is held
for the whole run, so the stage, ``storage auto`` by hand and a reclaimed job never
write two batches at once.

The admin's buttons on the Machines page set ``storage_canonicalize.request``:
``run`` (check now), ``revert`` (undo the last conversion; automatic conversion then
holds until ``resume``) and ``resume``.
"""

from __future__ import annotations

import asyncio
import json
import os
import platform
import random
import time
import uuid
from collections.abc import AsyncIterator, Awaitable, Callable, Iterable, Mapping, Sequence
from contextlib import asynccontextmanager
from dataclasses import dataclass, field
from datetime import datetime, timedelta
from typing import Any

from .. import settings
from ..engine.paths import PathMap, _norm
from ..engine.sql import dumps, is_pg, loads, now, to_ts
from ..log import get_logger
from . import backup as bk
from . import migrate as mig
from .autoreg import Lookups, register_from_mounts
from .canonical import is_canonical
from .mounts import Mount, discover
from .resolver import Resolver, build_resolver
from .servers import load_servers

__all__ = [
    "MARK",
    "STAGE",
    "AutoEnv",
    "StorageConfig",
    "due",
    "enqueue",
    "ensure_plex_map",
    "load_state",
    "release_stages",
    "request",
    "restore",
    "run_once",
]

_log = get_logger()

STAGE = "storage_canonicalize"
#: ``job_stages.paused_by`` of the stages this paused (and only those are resumed)
MARK = "storage_canonicalize"
#: phases a run is in the middle of: a worker that stopped there is resumed at once
ACTIVE = frozenset({"backing_up", "pausing", "applying", "verifying", "reverting"})
#: the advisory lock one run holds (``hashtext`` of it)
LOCK_KEY = "maktaba.storage_canonicalize"


@dataclass(frozen=True)
class StorageConfig:
    """Canonical share paths: the home converts the stored paths by itself."""

    #: off: nothing is converted or registered automatically (``storage canonicalize``
    #: by hand still works)
    auto_canonicalize: bool = True
    #: every server the home mounts that no record names gets one
    auto_register_servers: bool = True
    #: how often the home looks again (a blocked or failed run is retried as often)
    check_minutes: int = 60
    #: converted paths checked against the files before and after
    verify_samples: int = 20
    #: sampled paths that may be missing (a file deleted since the scan); above it the
    #: conversion waits, a share is probably not mounted
    allowed_missing: int = 0
    #: the worker pools whose stages read or write the shares, paused while it writes
    pause_pools: tuple[str, ...] = ("nas_read", "nas_write", "repairs")
    #: how long it waits for their running jobs before it writes anyway (every write
    #: is conditional on the old value, so a late writer's row is left for the next run)
    drain_wait_minutes: int = 10
    #: values written per transaction
    batch_size: int = 2000
    #: backups of the path columns kept in the database
    backups_kept: int = 3
    #: add the canonical prefix to the Plex extension's path map
    plex_path_map: bool = True
    #: how long the home's reported share map must name a share before paths are
    #: converted to it (the API and the streaming service re-read it every 5 minutes);
    #: 0 converts at once
    map_settle_minutes: int = 6


_S = "Canonical share paths"
settings.register(StorageConfig, {
    "auto_canonicalize": settings.Help(_S, "Convert paths automatically",
                                       "The home converts the stored media paths to "
                                       "smb://server/share/... by itself, once every share "
                                       "they are on is mounted and the check passes. Off: "
                                       "only maktaba-pipeline storage canonicalize does."),
    "auto_register_servers": settings.Help(_S, "Register storage servers",
                                           "Every NAS the home mounts gets a storage server "
                                           "record (named from its DNS or NetBIOS name). A "
                                           "record you edited is never changed."),
    "check_minutes": settings.Help(_S, "Look again every (min)", minimum=5, maximum=10_080),
    "verify_samples": settings.Help(_S, "Paths checked", "Converted paths opened before and "
                                    "after the conversion.", minimum=1, maximum=1000),
    "allowed_missing": settings.Help(_S, "Missing files allowed", "Checked paths that may be "
                                     "missing (deleted since the scan) before the conversion "
                                     "waits.", minimum=0, maximum=1000),
    "pause_pools": settings.Help(_S, "Pause these pools", "The stages of these worker pools "
                                 "read or write the shares: they pause while the paths are "
                                 "written, the rest of the pipeline keeps running."),
    "drain_wait_minutes": settings.Help(_S, "Wait for running jobs (min)", minimum=0,
                                        maximum=240),
    "batch_size": settings.Help(_S, "Values per transaction", minimum=100, maximum=100_000),
    "backups_kept": settings.Help(_S, "Backups kept", minimum=1, maximum=20),
    "plex_path_map": settings.Help(_S, "Add to the Plex path map",
                                   "The Plex extension's path map gets the canonical prefix "
                                   "of every local prefix it maps."),
    "map_settle_minutes": settings.Help(_S, "Wait for the share map (min)",
                                        "The API and the streaming service read the home's "
                                        "share map every 5 minutes: paths are converted to a "
                                        "share only once the map has named it this long, so "
                                        "playback never meets a path it cannot open yet.",
                                        minimum=0, maximum=240),
})  # fmt: skip


# -- the environment (tests pass their own) ---------------------------------------------


Sleep = Callable[[float], Awaitable[None]]


@dataclass
class AutoEnv:
    system: str = field(default_factory=platform.system)
    machine: str = field(default_factory=platform.node)
    #: this machine's network mounts now (``None``: discovered live)
    mounts: Sequence[Mount] | None = None
    lookups: Lookups = field(default_factory=Lookups)
    exists: Callable[[str], bool] = os.path.exists
    isdir: Callable[[str], bool] = os.path.isdir
    sleep: Sleep = asyncio.sleep
    poll_sec: float = 5.0
    #: called after the home's servers or stored paths changed (the agent rebuilds its
    #: resolver at once)
    on_change: Callable[[], None] | None = None
    #: tests: called after every chunk written (to stop a run mid-way)
    after_chunk: Callable[[int, int], Awaitable[None]] | None = None

    def live_mounts(self) -> list[Mount]:
        return list(discover(self.system) if self.mounts is None else self.mounts)


# -- one run at a time --------------------------------------------------------------------

#: SQLite (one process): the runs of this process
_LOCAL_LOCK = asyncio.Lock()


@asynccontextmanager
async def _exclusive(db: Any) -> AsyncIterator[bool]:
    """Hold the run's lock; yields ``False`` when another run holds it. On Postgres a
    session advisory lock on a connection of its own, held for the whole run (a worker
    that dies releases it with its connection)."""
    pool = getattr(db, "_pool", None) if is_pg(db) else None
    if pool is not None:
        async with pool.acquire() as conn:
            got = bool(await conn.fetchval("SELECT pg_try_advisory_lock(hashtext($1))", LOCK_KEY))
            try:
                yield got
            finally:
                if got:
                    await conn.execute("SELECT pg_advisory_unlock(hashtext($1))", LOCK_KEY)
        return
    if _LOCAL_LOCK.locked():
        yield False
        return
    await _LOCAL_LOCK.acquire()
    try:
        yield True
    finally:
        _LOCAL_LOCK.release()


# -- state --------------------------------------------------------------------------------


async def load_state(db: Any) -> tuple[dict[str, Any], str | None]:
    """``(state, pending request)``; an empty state on an older schema."""
    try:
        row = await db.fetchrow("SELECT state, request FROM storage_canonicalize WHERE id = 1")
    except Exception as exc:  # noqa: BLE001 - no table: nothing to do here
        _log.debug("storage_canonicalize_unavailable", error=str(exc))
        return {}, None
    if row is None:
        return {}, None
    st = loads(row["state"]) if isinstance(row["state"], str) else row["state"]
    return (dict(st) if isinstance(st, dict) else {}), row["request"]


#: the notification centre (slot 0130): a notice when a conversion is done, waits,
#: failed or was reverted; once per outcome (``state.notified``)
_SEVERITY = {"done": "success", "blocked": "warn", "failed": "error", "held": "info"}


async def _notify(db: Any, state: dict[str, Any]) -> None:
    phase = state.get("phase")
    if phase not in _SEVERITY:
        return
    problems = state.get("problems") or []
    params: dict[str, Any]
    if phase == "done":
        last = state.get("last") or {}
        if not last.get("batch"):
            return
        sig, params = f"done:{last['batch']}", {"n": int(last.get("changes") or 0)}
    elif phase == "held":
        rev = state.get("reverted") or {}
        if not rev:
            return
        sig, params = f"held:{rev.get('batch')}", {"n": int(rev.get("rows") or 0)}
    else:
        codes = sorted({str(p.get("code")) for p in problems})
        first = problems[0] if problems else {}
        detail = str(first.get("detail") or first.get("code") or "")
        sig, params = f"{phase}:{','.join(codes)}", {"detail": detail}
    if state.get("notified") == sig:
        return
    try:
        await db.execute(
            "INSERT INTO notifications (id, kind, severity, title_key, params, link, audience,"
            " subject) VALUES ($1, $2, $3, $4, $5, $6, 'admins', $7)",
            str(uuid.uuid4()),
            f"storage.canonicalize.{phase}",
            _SEVERITY[phase],
            f"notify.storage.canonicalize.{phase}",
            dumps(params),
            "/admin/machines",
            "storage_canonicalize",
        )
    except Exception as exc:  # noqa: BLE001 - no notification centre yet: the page shows it
        _log.debug("storage_canonicalize_notify_skipped", error=str(exc))
        return
    state["notified"] = sig


async def _save(db: Any, state: Mapping[str, Any]) -> None:
    cast = "::jsonb" if is_pg(db) else ""
    data = dict(state)
    await _notify(db, data)
    if isinstance(state, dict) and "notified" in data:
        state["notified"] = data["notified"]
    data["updated_at"] = now().isoformat()
    await db.execute(
        "INSERT INTO storage_canonicalize (id, state, updated_at)"
        f" VALUES (1, $1{cast}, $2)"
        " ON CONFLICT (id) DO UPDATE SET state = EXCLUDED.state, updated_at = EXCLUDED.updated_at",
        dumps(data),
        to_ts(db, now()),
    )


async def _take_request(db: Any, action: str) -> None:
    """Clear the request this run takes, only while it is still that one: an admin's
    newer request stays for the next run."""
    await db.execute(
        "UPDATE storage_canonicalize SET request = NULL, requested_at = NULL,"
        " requested_by = NULL WHERE id = 1 AND request = $1",
        action,
    )


async def request(db: Any, action: str | None, by: str = "cli") -> None:
    """Ask the home for ``run`` / ``revert`` / ``resume`` (``None`` clears it)."""
    if action not in (None, "run", "revert", "resume"):
        raise ValueError(f"unknown request {action!r}")
    await db.execute(
        "INSERT INTO storage_canonicalize (id, request, requested_at, requested_by)"
        " VALUES (1, $1, $2, $3) ON CONFLICT (id) DO UPDATE SET request = EXCLUDED.request,"
        " requested_at = EXCLUDED.requested_at, requested_by = EXCLUDED.requested_by",
        action,
        to_ts(db, now()) if action else None,
        by if action else None,
    )


async def _config(db: Any) -> StorageConfig:
    from ..rules import read

    cfg, _, bad = await read(db, "storage")
    if bad:
        _log.warning("storage_rules_skipped", skipped=bad)
    return cfg  # type: ignore[no-any-return]


# -- the stages that touch the shares -----------------------------------------------------


async def pause_path_stages(db: Any, pools: Iterable[str]) -> list[str]:
    """Pause every stage of ``pools`` not paused already; every stage this holds."""
    pools = set(pools)
    rows = await db.fetch("SELECT stage, pool, paused, paused_by FROM job_stages")
    at = to_ts(db, now())
    for r in rows:
        if r["pool"] in pools and not r["paused"] and r["stage"] != STAGE:
            await db.execute(
                "UPDATE job_stages SET paused = $2, paused_at = $3, paused_by = $4,"
                " updated_at = $3 WHERE stage = $1 AND paused = $5",
                r["stage"],
                True,
                at,
                MARK,
                False,
            )
    return await held_stages(db)


async def held_stages(db: Any) -> list[str]:
    rows = await db.fetch(
        "SELECT stage FROM job_stages WHERE paused = $1 AND paused_by = $2 ORDER BY stage",
        True,
        MARK,
    )
    return [str(r["stage"]) for r in rows]


async def release_stages(db: Any) -> list[str]:
    """Resume the stages this paused (a stage the admin paused, or paused again since,
    has another ``paused_by`` and stays as it is)."""
    try:
        held = await held_stages(db)
    except Exception:  # noqa: BLE001 - an older schema: nothing held
        return []
    if held:
        await db.execute(
            "UPDATE job_stages SET paused = $1, paused_at = NULL, paused_by = NULL,"
            " updated_at = $2 WHERE paused = $3 AND paused_by = $4",
            False,
            to_ts(db, now()),
            True,
            MARK,
        )
        _log.info("storage_canonicalize_stages_resumed", stages=held)
    return held


async def _running(db: Any, stages: Sequence[str]) -> int:
    if not stages:
        return 0
    marks = ", ".join(f"${i + 1}" for i in range(len(stages)))
    return int(
        await db.fetchval(
            f"SELECT COUNT(*) FROM processing_jobs WHERE stage IN ({marks})"
            " AND state IN ('claimed', 'running', 'resuming')",
            *stages,
        )
        or 0
    )


async def wait_drained(db: Any, stages: Sequence[str], timeout_sec: float, env: AutoEnv) -> int:
    """Wait until no job of ``stages`` runs, at most ``timeout_sec``; the jobs still
    running then."""
    deadline = time.monotonic() + max(0.0, timeout_sec)
    while True:
        n = await _running(db, stages)
        if n == 0 or time.monotonic() >= deadline:
            return n
        await env.sleep(env.poll_sec)


# -- the Plex path map --------------------------------------------------------------------


async def ensure_plex_map(db: Any, resolver: Resolver) -> list[str]:
    """Add ``<canonical prefix>=<Plex path>`` for every local prefix of the Plex
    extension's path map that is on a share; the entries added."""
    try:
        row = await db.fetchrow("SELECT settings FROM extensions WHERE name = 'plex'")
    except Exception:  # noqa: BLE001 - no extensions table
        return []
    if row is None:
        return []
    stored = loads(row["settings"]) if row["settings"] is not None else {}
    stored = dict(stored) if isinstance(stored, dict) else {}
    text = str(stored.get("path_map") or os.environ.get("MAKTABA_PATH_MAP") or "").strip()
    try:
        pm = PathMap.parse(text)
    except (ValueError, json.JSONDecodeError):
        _log.warning("plex_path_map_unreadable")
        return []
    have = {src.lower() for src, _ in pm.entries}
    add: list[tuple[str, str]] = []
    for src, dst in pm.entries:
        if is_canonical(src.rstrip("/")):
            continue
        c = resolver.canonical(src)
        if c is None:
            continue
        key = _norm(c)
        if key.lower() in have:
            continue
        have.add(key.lower())
        add.append((key, dst))
    if not add:
        return []
    if text.startswith("{"):
        obj = json.loads(text)
        obj.update(dict(add))
        new_text = json.dumps(obj, ensure_ascii=False)
    else:
        new_text = ";".join([text.rstrip(";"), *(f"{k}={v}" for k, v in add)])
    stored["path_map"] = new_text
    await db.execute(
        "UPDATE extensions SET settings = $2, updated_at = $3 WHERE name = $1",
        "plex",
        dumps(stored),
        to_ts(db, now()),
    )
    if is_pg(db):
        await db.execute("SELECT pg_notify('jobs.control', $1)", dumps({"table": "extensions"}))
    added = [f"{k}={v}" for k, v in add]
    _log.info("plex_path_map_canonical_added", entries=added)
    return added


# -- the checks ---------------------------------------------------------------------------


async def _home_admin_map(db: Any) -> dict[str, str] | None:
    try:
        row = await db.fetchrow("SELECT path_map FROM instance_identity WHERE id = 1")
    except Exception:  # noqa: BLE001 - an older schema
        return None
    if row is None or row["path_map"] is None:
        return None
    v = loads(row["path_map"]) if isinstance(row["path_map"], str) else row["path_map"]
    return {str(k): str(x) for k, x in v.items()} if isinstance(v, dict) and v else None


async def _legacy_roots(db: Any) -> list[str]:
    out: list[str] = []
    for table, column, array in (
        ("libraries", "roots", True),
        ("library_roots", "path", False),
    ):
        try:
            rows = await db.fetch(f"SELECT {column} AS v FROM {table} WHERE {column} IS NOT NULL")
        except Exception:  # noqa: BLE001
            continue
        for r in rows:
            values = mig._array(r["v"]) if array else [str(r["v"])]  # noqa: SLF001
            for v in values:
                if v and not is_canonical(v) and v not in out:
                    out.append(v)
    return out


def _problem(code: str, detail: str = "") -> dict[str, str]:
    return {"code": code, "detail": detail}


async def precheck(
    db: Any, resolver: Resolver, mounts: Sequence[Mount], env: AutoEnv
) -> list[dict[str, str]]:
    """Why the conversion cannot run now (empty: it can)."""
    problems: list[dict[str, str]] = []
    locals_ = {s["local"].lower() for s in resolver.share_list()}
    roots = await _legacy_roots(db)
    for m in mounts:
        local = _norm(m.local).lower()
        if local in locals_:
            continue
        # a mount no server names matters only when stored paths are on it
        if any(_norm(r).lower().startswith(local) for r in roots):
            problems.append(_problem("unresolved_mount", f"//{m.host}/{m.share} -> {m.local}"))
    for root in roots:
        if resolver.canonical(root) is None and not env.isdir(root):
            problems.append(_problem("unmounted", root))
    return problems


def _missing(p: mig.MigrationPlan) -> list[dict[str, str]]:
    return [
        _problem("missing", f"{a} -> {b}") for c in p.columns for a, b, ok in c.verified if not ok
    ]


def _plan_summary(p: mig.MigrationPlan) -> dict[str, Any]:
    return {
        "changes": p.total,
        "collisions": sum(c.collisions for c in p.columns),
        "unmatched": sum(c.unmatched for c in p.columns),
        "checked": sum(len(c.verified) for c in p.columns),
        "missing": sum(1 for c in p.columns for v in c.verified if not v[2]),
        "columns": [
            {
                "column": f"{c.table}.{c.column}",
                "rows": c.rows,
                "to_convert": c.converted,
                "already": c.already,
                "unmatched": c.unmatched,
                "collisions": c.collisions,
            }
            for c in p.columns
            if c.rows
        ],
        "shares": [s["share"] for s in p.shares],
    }


async def _legacy_count(db: Any) -> int:
    try:
        return int(
            await db.fetchval(
                "SELECT COUNT(*) FROM videos WHERE path IS NOT NULL AND path NOT LIKE 'smb://%'"
                " AND path NOT LIKE 'nfs://%' AND path NOT LIKE 'file://%'"
            )
            or 0
        )
    except Exception:  # noqa: BLE001
        return 0


async def fingerprint(db: Any, mounts: Sequence[Mount]) -> dict[str, Any]:
    """What a new look could change: the paths still in local form, the mounts, the
    servers."""
    servers = await load_servers(db)
    return {
        "legacy": await _legacy_count(db),
        "mounts": sorted(f"{m.host}/{m.share}={m.local}" for m in mounts),
        "servers": sorted(f"{s.name}:{','.join(s.addresses)}" for s in servers),
    }


# -- when to run --------------------------------------------------------------------------


def _ts(v: Any) -> datetime | None:
    if not v:
        return None
    try:
        return datetime.fromisoformat(str(v))
    except ValueError:
        return None


async def due(db: Any, env: AutoEnv | None = None) -> str | None:
    """Why the stage should run now, or ``None``. Cheap: a row, a count, the mounts."""
    state, req = await load_state(db)
    if req:
        return f"request:{req}"
    phase = state.get("phase")
    if phase in ACTIVE:
        return "resume"
    cfg = await _config(db)
    if not cfg.auto_canonicalize:
        return "release" if await held_stages(db) else None
    if state.get("held"):
        return None
    checked = _ts(state.get("checked_at"))
    if checked is None or phase not in ("idle", "done", "blocked", "failed"):
        return "first"  # never looked, or switched on again / resumed since
    if phase in ("blocked", "failed"):
        if now() - checked >= timedelta(minutes=cfg.check_minutes):
            return "retry"
        return None
    env = env or AutoEnv()
    fp = await fingerprint(db, env.live_mounts())
    if fp != state.get("fingerprint"):
        return "changed"
    return None


async def enqueue(db: Any, why: str) -> bool:
    from ..engine.enqueue import enqueue_job
    from ..engine.graph import CATALOG_ID

    res = await enqueue_job(
        db,
        subject_kind="catalog",
        subject_id=CATALOG_ID,
        stage=STAGE,
        reason={"why": f"storage_{why}", "by": "system"},
    )
    return res.enqueued


# -- the run ------------------------------------------------------------------------------


def _message(phase: str, reason: Sequence[Mapping[str, str]] | None = None) -> str:
    if reason:
        first = reason[0]
        return f"{phase}: {first['code']} {first.get('detail', '')}".strip()
    return phase


async def run_once(db: Any, env: AutoEnv | None = None) -> dict[str, Any]:
    """One automatic look (and conversion, when it can). The state it leaves; when
    another run holds the lock, the state as it is (``busy``)."""
    env = env or AutoEnv()
    async with _exclusive(db) as got:
        if not got:
            state, _ = await load_state(db)
            _log.info("storage_canonicalize_busy", phase=state.get("phase"))
            return {**state, "busy": True}
        return await _run_once(db, env)


async def _run_once(db: Any, env: AutoEnv) -> dict[str, Any]:
    cfg = await _config(db)
    state, req = await load_state(db)
    if state.get("phase") == "reverting" and req != "revert":
        # a revert cut short is finished, never taken for a conversion to carry on (a
        # pending request waits for the next run)
        state["checked_at"] = now().isoformat()
        state.pop("problems", None)
        return await _revert(db, state, cfg, env)
    if req:
        await _take_request(db, req)
    state["checked_at"] = now().isoformat()
    state.pop("problems", None)
    if req == "revert":
        return await _revert(db, state, cfg, env)
    if req == "resume":
        state["held"] = False
        state.pop("reverted", None)
    if not cfg.auto_canonicalize:
        await release_stages(db)
        state["phase"] = "off"
        await _save(db, state)
        return state
    if state.get("held"):
        await release_stages(db)
        state["phase"] = "held"
        await _save(db, state)
        return state

    # the mount table and the DNS / NetBIOS lookups block: off the event loop, so the
    # worker's heartbeats and its other jobs keep running
    mounts = await asyncio.to_thread(env.live_mounts)
    servers = await load_servers(db)
    if cfg.auto_register_servers and mounts:
        registered = dict(state.get("registered") or {})
        added, skipped = await register_from_mounts(
            db, mounts, servers, env.lookups, registered=registered
        )
        state["registered"] = registered
        if added:
            state["servers_added"] = [
                *(state.get("servers_added") or []),
                *({"name": s.name, "addresses": list(s.addresses)} for s in added),
            ]
            servers = await load_servers(db)
            if env.on_change:
                env.on_change()
        state["servers_skipped"] = skipped
    admin = await _home_admin_map(db)
    resolver = await asyncio.to_thread(
        lambda: build_resolver(
            servers=servers,
            mounts=mounts,
            system=env.system,
            admin=admin,
            resolve=env.lookups.resolve if servers else None,
            exists=None if env.exists is os.path.exists else env.exists,
        )
    )
    state["fingerprint"] = await fingerprint(db, mounts)
    problems = await precheck(db, resolver, mounts, env)
    if problems:
        return await _blocked(db, state, problems)
    if not resolver.share_list():
        # no network share here and no stored path on a missing one: nothing to do
        await release_stages(db)
        state["phase"] = "idle" if not state.get("run") else "blocked"
        state["problems"] = [_problem("no_shares")]
        await _save(db, state)
        return state

    p = await mig.plan(
        db, resolver, verify=cfg.verify_samples, exists=env.exists, machine=env.machine
    )
    state["plan"] = _plan_summary(p)
    missing = _missing(p)
    if len(missing) > cfg.allowed_missing:
        return await _blocked(db, state, missing)
    if cfg.plex_path_map:
        plex = await ensure_plex_map(db, resolver)
        if plex:
            state["plex_added"] = [*(state.get("plex_added") or []), *plex]
    if p.total == 0 and not state.get("run"):
        await release_stages(db)
        state["phase"] = "done" if state.get("last") else "idle"
        await _save(db, state)
        return state
    if not state.get("run"):
        waits = await _map_ready(db, state, cfg, p)
        if waits:
            await release_stages(db)
            state["phase"] = "waiting"
            state["problems"] = waits
            _log.info("storage_canonicalize_waits_for_map", problems=waits)
            await _save(db, state)
            return state
    try:
        return await _convert(db, state, cfg, env, resolver, p)
    except Exception as exc:  # noqa: BLE001 - reported; the run stays open for the retry
        _log.exception("storage_canonicalize_failed")
        await release_stages(db)
        state["phase"] = "failed"
        state["problems"] = [_problem("error", f"{type(exc).__name__}: {exc}")]
        await _save(db, state)
        return state


async def _reported_shares(db: Any) -> set[str] | None:
    """The prefixes of the map the home reports (``instance_identity.storage.
    path_map``: what the API and the streaming service resolve with); ``None`` on an
    older schema (nothing reads one there)."""
    try:
        row = await db.fetchrow("SELECT storage FROM instance_identity WHERE id = 1")
    except Exception as exc:  # noqa: BLE001 - an older schema
        _log.debug("home_storage_unavailable", error=str(exc))
        return None
    if row is None or row["storage"] is None:
        return set()
    st = loads(row["storage"]) if isinstance(row["storage"], str) else row["storage"]
    pm = st.get("path_map") if isinstance(st, dict) else None
    return {_norm(str(k)).lower() for k in pm} if isinstance(pm, dict) else set()


async def _map_ready(
    db: Any, state: dict[str, Any], cfg: StorageConfig, p: mig.MigrationPlan
) -> list[dict[str, str]]:
    """Why a new conversion waits for the reported share map (empty: it need not)."""
    if cfg.map_settle_minutes <= 0:
        return []
    needed = sorted(
        {_norm(k).lower() for c in p.columns for k in c.by_share if is_canonical(k.rstrip("/"))}
    )
    if not needed:
        return []
    reported = await _reported_shares(db)
    if reported is None:
        return []
    at = now()
    # share -> when the reported map was first seen naming it (forgotten once it does not)
    seen = {k: v for k, v in dict(state.get("map_seen") or {}).items() if k in reported}
    for n in needed:
        if n in reported and n not in seen:
            seen[n] = at.isoformat()
    state["map_seen"] = seen
    missing = [n for n in needed if n not in reported]
    if missing:
        return [_problem("map_not_reported", ", ".join(missing))]
    settle = timedelta(minutes=cfg.map_settle_minutes)
    left = max(settle - (at - (_ts(seen[n]) or at)) for n in needed)
    if left > timedelta(0):
        return [_problem("map_settling", str(int(left.total_seconds() // 60) + 1))]
    return []


async def _blocked(
    db: Any, state: dict[str, Any], problems: list[dict[str, str]]
) -> dict[str, Any]:
    await release_stages(db)
    state["phase"] = "blocked"
    state["problems"] = problems[:20]
    _log.warning("storage_canonicalize_blocked", problems=problems[:5])
    await _save(db, state)
    return state


async def _convert(
    db: Any,
    state: dict[str, Any],
    cfg: StorageConfig,
    env: AutoEnv,
    resolver: Resolver,
    p: mig.MigrationPlan,
) -> dict[str, Any]:
    run = dict(state.get("run") or {})
    if not run:
        run = {
            "id": str(uuid.uuid4()),
            "batch": str(uuid.uuid4()),
            "started_at": now().isoformat(),
            "done": 0,
        }
    run["total"] = max(int(run.get("done") or 0) + p.total, int(run.get("total") or 0))
    state["run"] = run
    if not run.get("backup"):
        state["phase"] = "backing_up"
        await _save(db, state)
        bid, n = await bk.take_backup(db, machine=env.machine)
        await bk.set_backup_batch(db, bid, run["batch"])
        await bk.prune_backups(db, cfg.backups_kept)
        run["backup"], run["backup_values"] = bid, n
    state["phase"] = "pausing"
    await _save(db, state)
    held = await pause_path_stages(db, cfg.pause_pools)
    run["paused"] = held
    still = await wait_drained(db, held, cfg.drain_wait_minutes * 60.0, env)
    if still:
        _log.warning("storage_canonicalize_writers_still_running", jobs=still)
    # the paths as they are now the writers are paused
    p = await mig.plan(db, resolver, verify=0, machine=env.machine)
    state["phase"] = "applying"
    await _save(db, state)
    base = int(run.get("done") or 0)

    async def progress(done: int, total: int) -> None:
        run["done"] = base + done
        run["total"] = max(int(run.get("total") or 0), base + total)
        await _save(db, state)
        if env.after_chunk is not None:
            await env.after_chunk(done, total)

    await mig.apply(db, p, batch=run["batch"], chunk_size=cfg.batch_size, progress=progress)
    state["phase"] = "verifying"
    await _save(db, state)
    left = await mig.plan(db, resolver, verify=0, machine=env.machine)
    rounds = 0
    while left.total and rounds < 2:
        # a late writer stored a local path meanwhile: the same batch takes it too
        rounds += 1
        await mig.apply(db, left, batch=run["batch"], chunk_size=cfg.batch_size)
        run["done"] = int(run.get("done") or 0) + left.total
        left = await mig.plan(db, resolver, verify=0, machine=env.machine)
    problems: list[dict[str, str]] = []
    if left.total:
        problems.append(_problem("verify_changes", str(left.total)))
    problems += await _verify_sample(db, resolver, run["batch"], cfg.verify_samples, env)
    await release_stages(db)
    n = await _batch_size(db, run["batch"])
    state["last"] = {
        "batch": run["batch"],
        "backup": run.get("backup"),
        "changes": n,
        "started_at": run.get("started_at"),
        "finished_at": now().isoformat(),
        "machine": env.machine,
    }
    state["run"] = None
    state["fingerprint"] = await fingerprint(db, env.live_mounts())
    if len([x for x in problems if x["code"] == "missing"]) > cfg.allowed_missing or any(
        x["code"] == "verify_changes" for x in problems
    ):
        state["phase"] = "failed"
        state["problems"] = problems[:20]
    else:
        state["phase"] = "done"
    await _save(db, state)
    if env.on_change:
        env.on_change()
    _log.info("storage_canonicalize_done", batch=run["batch"], changes=n, phase=state["phase"])
    return state


async def _batch_size(db: Any, batch: str) -> int:
    cast = "::uuid" if is_pg(db) else ""
    return int(
        await db.fetchval(
            f"SELECT COUNT(*) FROM storage_path_migrations WHERE batch_id = $1{cast}", batch
        )
        or 0
    )


async def _verify_sample(
    db: Any, resolver: Resolver, batch: str, k: int, env: AutoEnv
) -> list[dict[str, str]]:
    cast = "::uuid" if is_pg(db) else ""
    key = "v.id::text" if is_pg(db) else "CAST(v.id AS TEXT)"
    # only the videos whose file is there (a soft-deleted one is gone on purpose)
    rows = await db.fetch(
        "SELECT m.new_value FROM storage_path_migrations m"
        f" JOIN videos v ON {key} = m.row_key AND v.path = m.new_value"
        f" WHERE m.batch_id = $1{cast} AND m.table_name = 'videos' AND m.column_name = 'path'"
        " AND m.reverted_at IS NULL AND v.deleted_at IS NULL",
        batch,
    )
    values = [str(r["new_value"]) for r in rows if is_canonical(str(r["new_value"]))]
    out: list[dict[str, str]] = []
    for v in random.sample(values, min(k, len(values))):
        local = resolver.to_local(v)
        if not env.exists(local):
            out.append(_problem("missing", f"{v} -> {local}"))
    return out


async def _revert(
    db: Any, state: dict[str, Any], cfg: StorageConfig, env: AutoEnv
) -> dict[str, Any]:
    run = state.get("run") or {}
    # the revert cut short, else the run in progress (cut short or failed: its rows are
    # the newest), else the last finished conversion
    batch = state.get("reverting") or run.get("batch") or (state.get("last") or {}).get("batch")
    if batch is None:
        listed = [b for b in await mig.batches(db) if b["reverted"] < b["changes"]]
        batch = listed[0]["batch"] if listed else None
    if batch is None:
        state["problems"] = [_problem("nothing_to_revert")]
        state["held"] = True
        state["phase"] = "held"
        await _save(db, state)
        return state
    state["phase"] = "reverting"
    state["reverting"] = batch
    await _save(db, state)
    held = await pause_path_stages(db, cfg.pause_pools)
    await wait_drained(db, held, cfg.drain_wait_minutes * 60.0, env)
    try:
        got, n = await mig.revert(db, batch)
    finally:
        await release_stages(db)
    state.pop("reverting", None)
    state["reverted"] = {"batch": got or batch, "rows": n, "at": now().isoformat()}
    if (state.get("last") or {}).get("batch") in (batch, got):
        state["last"] = None  # a finished conversion reverted (not only a run's rows)
    state["run"] = None
    state["held"] = True
    state["phase"] = "held"
    await _save(db, state)
    if env.on_change:
        env.on_change()
    _log.info("storage_canonicalize_reverted", batch=batch, rows=n)
    return state


async def restore(
    db: Any, backup_id: str | None = None, env: AutoEnv | None = None
) -> tuple[str | None, int]:
    """``storage restore-backup``: write a backup's values back with the share stages
    paused, and hold the automatic conversion (as after a revert), so its next look does
    not convert the restored paths again; ``resume`` lets it go on."""
    env = env or AutoEnv()
    async with _exclusive(db) as got:
        if not got:
            raise RuntimeError("a path conversion is running: restore when it is done")
        cfg = await _config(db)
        state, _ = await load_state(db)
        held = await pause_path_stages(db, cfg.pause_pools)
        await wait_drained(db, held, cfg.drain_wait_minutes * 60.0, env)
        try:
            bid, n = await bk.restore_backup(db, backup_id)
        finally:
            await release_stages(db)
        if bid is None:
            return None, 0
        state["reverted"] = {
            "batch": f"backup:{bid}",
            "backup": bid,
            "rows": n,
            "at": now().isoformat(),
        }
        state["run"] = None
        state["last"] = None
        state["held"] = True
        state["phase"] = "held"
        state.pop("problems", None)
        await _save(db, state)
        if env.on_change:
            env.on_change()
        _log.info("storage_canonicalize_backup_restored", backup=bid, rows=n)
        return bid, n
