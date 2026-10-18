"""Worker registry (multi-host workers, plan 06-11 notes N2).

Each worker process registers its host, the stages it serves, its pools and
path map, then heartbeats. The UI reads this table to show which hosts are
online and which stages have no live worker ("waiting for a worker that
serves transcribe"). Claiming already offers a job only to a worker whose
``stages`` list contains the job's stage (see :mod:`.claim`).
"""

from __future__ import annotations

import json
import platform
import socket
import subprocess
from collections.abc import Sequence
from datetime import timedelta
from typing import Any

from .sql import dumps, is_pg, now, parse_ts, to_ts

__all__ = [
    "LIVE_WINDOW_SEC",
    "deregister_worker",
    "heartbeat_worker",
    "live_workers",
    "register_worker",
    "stable_hostname",
    "stages_without_worker",
    "update_worker_stages",
]

#: A worker whose last heartbeat is older than this is considered offline.
LIVE_WINDOW_SEC = 45.0

#: A worker of this host silent this long is a dead predecessor (cleared by the next
#: worker on the host); much longer than LIVE_WINDOW_SEC, so a live worker whose loop
#: is busy for a minute is not mistaken for one (and re-registers if it ever is).
DEAD_AFTER_SEC = 300.0


def stable_hostname() -> str:
    """This machine's name as its owner set it. On macOS ``gethostname()`` follows the
    network (``Mahmouds-Mac-Studio.local`` one hour, a DHCP name the next), which split
    one Mac into several "hosts"; its LocalHostName does not change by itself."""
    if platform.system() == "Darwin":
        try:
            out = subprocess.run(
                ["scutil", "--get", "LocalHostName"],
                capture_output=True, text=True, timeout=5, check=True,
            ).stdout.strip()  # fmt: skip
            if out:
                return out
        except (OSError, subprocess.SubprocessError):
            pass
    return socket.gethostname()


async def _clear_dead(db: Any, host: str, worker_id: str) -> None:
    """Rows of this host that stopped heartbeating: earlier runs, killed before they
    could deregister (a stop that outlasted its drain). This worker replaces them."""
    await db.execute(
        "DELETE FROM workers WHERE host = $1 AND id <> $2 AND last_seen_at < $3",
        host,
        worker_id,
        to_ts(db, now() - timedelta(seconds=DEAD_AFTER_SEC)),
    )


def _arr(db: Any, items: Sequence[str]) -> Any:
    return list(items) if is_pg(db) else json.dumps(list(items))


async def register_worker(
    db: Any,
    *,
    worker_id: str,
    host: str,
    stages: Sequence[str],
    pools: Sequence[str],
    path_map: dict[str, str] | None = None,
    version: str | None = None,
    node_id: str | None = None,
    role: str = "home",
    os_name: str | None = None,
    capabilities: dict[str, Any] | None = None,
    stages_off: dict[str, str] | None = None,
) -> None:
    """Insert or refresh this worker's row. ``node_id`` / ``role`` say whether it is one
    of the home's workers or a joined machine's (Epic 31); ``stages_off`` says why each
    graph stage it does not claim is off."""
    ts = to_ts(db, now())
    # earlier runs on this host are replaced, not "offline" (again at each heartbeat:
    # a restart starts this worker before its predecessor's last beat is stale); a
    # dead worker on another host still shows, and the pruner drops it after a day
    await _clear_dead(db, host, worker_id)
    await db.execute(
        "INSERT INTO workers (id, host, stages, pools, path_map, version, started_at,"
        " last_seen_at, node_id, role, os, capabilities, stages_off)"
        " VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$8,$9,$10,$11,$12)"
        " ON CONFLICT (id) DO UPDATE SET host = EXCLUDED.host, stages = EXCLUDED.stages,"
        " pools = EXCLUDED.pools, path_map = EXCLUDED.path_map, version = EXCLUDED.version,"
        " started_at = EXCLUDED.started_at, last_seen_at = EXCLUDED.last_seen_at,"
        " node_id = EXCLUDED.node_id, role = EXCLUDED.role, os = EXCLUDED.os,"
        " capabilities = EXCLUDED.capabilities, stages_off = EXCLUDED.stages_off",
        worker_id,
        host,
        _arr(db, stages),
        _arr(db, pools),
        dumps(path_map or {}),
        version,
        ts,
        node_id,
        role,
        os_name if os_name is not None else platform.system().lower(),
        dumps(capabilities or {}),
        dumps(stages_off or {}),
    )


async def update_worker_stages(
    db: Any,
    worker_id: str,
    *,
    stages: Sequence[str],
    pools: Sequence[str],
    stages_off: dict[str, str],
) -> bool:
    """The stages this worker now claims and why the others are off (re-evaluated while
    it runs, Epic 31 design section 5); False when its row is gone."""
    row = await db.fetchval(
        "UPDATE workers SET stages = $2, pools = $3, stages_off = $4 WHERE id = $1 RETURNING id",
        worker_id,
        _arr(db, stages),
        _arr(db, pools),
        dumps(stages_off),
    )
    return row is not None


async def heartbeat_worker(db: Any, worker_id: str, host: str | None = None) -> bool:
    """Beat; False when this worker's row is gone (cleared as dead, or pruned) and it
    should register again."""
    beat = await db.fetchval(
        "UPDATE workers SET last_seen_at = $2 WHERE id = $1 RETURNING id",
        worker_id,
        to_ts(db, now()),
    )
    if host is not None:
        await _clear_dead(db, host, worker_id)
    return beat is not None


async def deregister_worker(db: Any, worker_id: str) -> None:
    await db.execute("DELETE FROM workers WHERE id = $1", worker_id)


def _list(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, str):
        return [str(x) for x in json.loads(value)]
    return [str(x) for x in value]


async def live_workers(db: Any, *, window_sec: float = LIVE_WINDOW_SEC) -> list[dict[str, Any]]:
    cutoff = now() - timedelta(seconds=window_sec)
    rows = await db.fetch(
        "SELECT id, host, stages, pools, version, started_at, last_seen_at FROM workers"
        " WHERE last_seen_at >= $1 ORDER BY host, id",
        to_ts(db, cutoff),
    )
    return [
        {
            "id": r["id"],
            "host": r["host"],
            "stages": _list(r["stages"]),
            "pools": _list(r["pools"]),
            "version": r["version"],
            "started_at": parse_ts(r["started_at"]),
            "last_seen_at": parse_ts(r["last_seen_at"]),
        }
        for r in rows
    ]


async def stages_without_worker(db: Any, *, window_sec: float = LIVE_WINDOW_SEC) -> list[str]:
    """Enabled stages that no live worker serves."""
    served = {s for w in await live_workers(db, window_sec=window_sec) for s in w["stages"]}
    rows = await db.fetch("SELECT stage FROM job_stages WHERE enabled ORDER BY stage")
    return [r["stage"] for r in rows if r["stage"] not in served]
