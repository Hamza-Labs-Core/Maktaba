"""A worker's moving parts while it runs (Epic 31 design sections 5-7).

:class:`WorkerAgent` keeps a worker's :class:`~.stages.StageSelector` current: every
30 s it re-reads the versions in the database and, on a joined machine (role ``node``),
the admin's stage switches and (every 5 min) the storage check; at each 10 s heartbeat a
node also beats its ``cluster_nodes`` row and reads ``state`` / ``drain``: ``revoked``
sets the shutdown event, ``drain`` empties the served set at once. A change is written
to ``workers.stages`` / ``stages_off``; the claim loop reads the set on every poll.
"""

from __future__ import annotations

import asyncio
import json
import time
from collections.abc import Sequence
from dataclasses import dataclass, field
from typing import Any

from ..engine.graph import StageDef
from ..engine.paths import PathMap, set_active_path_map
from ..engine.sync import stages_ahead
from ..engine.workers import update_worker_stages
from ..log import get_logger
from . import exitcodes, nodes_db
from .stages import StageSelector
from .storage import LibrarySample, candidate_roots, check_storage, infer_path_map, load_samples

__all__ = ["STAGE_REFRESH_SEC", "STORAGE_CHECK_SEC", "WorkerAgent", "path_map_from_json"]

STAGE_REFRESH_SEC = 30.0
STORAGE_CHECK_SEC = 300.0

_log = get_logger()


def path_map_from_json(data: dict[str, str] | None) -> PathMap:
    return PathMap.parse(json.dumps(data)) if data else PathMap()


@dataclass
class WorkerAgent:
    db: Any
    worker_id: str
    selector: StageSelector
    graph: Sequence[StageDef]
    shutdown: asyncio.Event
    node_id: str | None = None
    #: ``--path-map`` was given: never replaced by the stored or inferred map
    explicit_path_map: bool = False
    path_map: PathMap = field(default_factory=PathMap)
    runtime: Any = None  #: the EngineRuntime whose path map follows ours
    samples: list[LibrarySample] = field(default_factory=list)
    storage_check_sec: float = STORAGE_CHECK_SEC
    _last_storage: float = 0.0

    @property
    def is_node(self) -> bool:
        return self.node_id is not None

    @property
    def node8(self) -> str:
        return (self.node_id or "").replace("-", "")[:8]

    # -- start -----------------------------------------------------------------

    async def start_node(self, capabilities: dict[str, Any]) -> None:
        """A node's start: its row's state, the path map (stored, or inferred and
        reported), the storage check, the switches. Call before the first compute."""
        assert self.node_id is not None
        beat = await nodes_db.beat(self.db, self.node_id)
        if beat is None:
            raise RuntimeError(
                f"this machine ({self.node_id}) is not known to the home: join it again"
            )
        if beat.state != "approved":
            _log.error("cluster_node_not_approved", state=beat.state)
            if beat.state in ("revoked", "rejected"):
                exitcodes.mark_refused(f"this machine is {beat.state} on the home")
            self.shutdown.set()
        self.selector.drain = beat.drain
        self.samples = await load_samples(self.db)
        inferred: dict[str, str] | None = None
        if not self.explicit_path_map:
            if beat.path_map_source == "admin" and beat.path_map is not None:
                self._set_path_map(path_map_from_json(beat.path_map))
            else:
                inferred = await asyncio.to_thread(self._infer)
                if inferred:
                    self._set_path_map(path_map_from_json(inferred))
                elif beat.path_map:
                    self._set_path_map(path_map_from_json(beat.path_map))
                _log.info("cluster_path_map_inferred", path_map=inferred or {})
        storage = await self._check_storage()
        await nodes_db.report(
            self.db,
            self.node_id,
            capabilities=capabilities,
            storage=storage,
            inferred_path_map=inferred or None,
        )
        self.selector.switches = await nodes_db.stage_switches(self.db, self.node_id)

    def _infer(self) -> dict[str, str]:
        paths = [p for lib in self.samples for p, _ in lib.videos]
        return infer_path_map(self.samples, candidate_roots(db_paths=paths))

    def _set_path_map(self, pm: PathMap) -> None:
        self.path_map = pm
        set_active_path_map(pm)
        if self.runtime is not None:
            self.runtime.path_map = pm

    async def _check_storage(self) -> dict[str, Any]:
        result = await asyncio.to_thread(check_storage, self.samples, self.path_map, self.node8)
        self.selector.storage = result
        self._last_storage = time.monotonic()
        if result["read"] == "error" or result["write"] == "error":
            _log.warning("cluster_storage_check_failed", **result)
        return result

    # -- while running ---------------------------------------------------------

    async def apply(self) -> bool:
        """Recompute; when the set changed write it to the registry. True on a change."""
        before = self.selector.served
        if not self.selector.compute():
            return False
        await update_worker_stages(
            self.db,
            self.worker_id,
            stages=self.selector.served,
            pools=self.selector.pools,
            stages_off=self.selector.stages_off,
        )
        added = sorted(set(self.selector.served) - set(before))
        removed = sorted(set(before) - set(self.selector.served))
        if added or removed:
            _log.info("worker_stages_changed", added=added, removed=removed)
        return True

    async def refresh(self) -> bool:
        """The 30 s re-evaluation."""
        self.selector.ahead = await stages_ahead(self.db, self.graph)
        if self.node_id is not None:
            self.selector.switches = await nodes_db.stage_switches(self.db, self.node_id)
            if time.monotonic() - self._last_storage >= self.storage_check_sec:
                storage = await self._check_storage()
                await nodes_db.report(self.db, self.node_id, storage=storage)
        return await self.apply()

    async def beat(self) -> None:
        """A node's heartbeat on its cluster row: revoked -> stop; drain -> claim nothing."""
        if self.node_id is None:
            return
        try:
            beat = await nodes_db.beat(self.db, self.node_id)
        except Exception as exc:
            if exitcodes.login_revoked(exc):
                _log.error("cluster_node_login_refused_stopping", error=str(exc))
                exitcodes.mark_refused("the home's database refused this machine's login")
                self.shutdown.set()
                return
            if exitcodes.is_auth_error(exc):
                # refused for another reason (pg_hba, ...): fixable on the home, so the
                # worker keeps going and the next heartbeat tries again
                _log.error("cluster_node_login_refused", error=str(exc))
            raise
        if beat is None or beat.state in ("revoked", "rejected"):
            _log.warning("cluster_node_revoked_stopping", state=None if beat else "removed")
            exitcodes.mark_refused(f"this machine is {beat.state if beat else 'removed'}")
            self.shutdown.set()
            return
        if beat.drain != self.selector.drain:
            _log.info("cluster_node_drain", drain=beat.drain)
            self.selector.drain = beat.drain
            await self.apply()
        if (
            not self.explicit_path_map
            and beat.path_map_source == "admin"
            and beat.path_map is not None
            and beat.path_map != self.path_map.as_json()
        ):
            _log.info("cluster_path_map_set_by_admin", path_map=beat.path_map)
            self._set_path_map(path_map_from_json(beat.path_map))

    async def run_refresher(self, interval: float = STAGE_REFRESH_SEC) -> None:
        while not self.shutdown.is_set():
            try:
                await asyncio.wait_for(self.shutdown.wait(), timeout=interval)
                return
            except (TimeoutError, asyncio.TimeoutError):  # noqa: UP041
                pass
            try:
                await self.refresh()
            except Exception:  # noqa: BLE001 - a failed refresh keeps the current set
                _log.exception("worker_stage_refresh_failed")
