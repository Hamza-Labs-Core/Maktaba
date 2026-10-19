"""A worker's moving parts while it runs (Epic 31 design sections 5-7).

:class:`WorkerAgent` keeps a worker's :class:`~.stages.StageSelector` current: every
30 s it re-reads the versions in the database and, on a joined machine (role ``node``),
the admin's stage switches and (every 5 min) the storage check; at each 10 s heartbeat a
node also beats its ``cluster_nodes`` row and reads ``state`` / ``drain``: ``revoked``
sets the shutdown event, ``drain`` empties the served set at once. A change is written
to ``workers.stages`` / ``stages_off``; the claim loop reads the set on every poll.

A node also checks the home's schema (:mod:`.schema`) at start and with every storage
check, at every refresh while paused: a home whose database lacks this code's migrations
pauses the machine (every stage off, ``paused: ...``; no storage probe; the runtime's
writers wait on :attr:`WorkerAgent.paused`) and it resumes by itself once the home caught up.
"""

from __future__ import annotations

import asyncio
import json
import time
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass, field
from typing import Any

from ..engine.graph import StageDef
from ..engine.paths import PathMap, set_active_path_map
from ..engine.sql import is_pg, loads
from ..engine.sync import stages_ahead
from ..engine.workers import update_worker_stages
from ..log import get_logger
from ..sharepath.servers import build_for_machine, load_servers
from . import exitcodes, nodes_db
from .schema import SCHEMA_REV, HomeSchema, pause_reason, read_home_schema
from .stages import StageSelector, lifted_placements
from .storage import (
    LibrarySample,
    candidate_roots,
    check_shares,
    check_storage,
    infer_path_map,
    load_samples,
)

__all__ = ["STAGE_REFRESH_SEC", "STORAGE_CHECK_SEC", "WorkerAgent", "path_map_from_json"]

STAGE_REFRESH_SEC = 30.0
STORAGE_CHECK_SEC = 300.0

_log = get_logger()


async def _home_admin_map(db: Any) -> dict[str, str] | None:
    """The home's "share -> local mount" override (``instance_identity.path_map``)."""
    try:
        row = await db.fetchrow("SELECT path_map FROM instance_identity WHERE id = 1")
    except Exception:  # noqa: BLE001 - an older schema
        return None
    if row is None or row["path_map"] is None:
        return None
    v = loads(row["path_map"]) if isinstance(row["path_map"], str) else row["path_map"]
    return {str(k): str(x) for k, x in v.items()} if isinstance(v, dict) and v else None


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
    #: story 31.3, the resolver's sources: the storage servers, the admin's "share ->
    #: local mount" override, the legacy (inferred / stored) prefixes, and this
    #: machine's mounts (``mounts`` for tests; ``None`` discovers them live)
    servers: list[Any] = field(default_factory=list)
    explicit: PathMap = field(default_factory=PathMap)
    admin_map: dict[str, str] | None = None
    legacy_map: dict[str, str] | None = None
    mounts: list[Any] | None = None
    #: the schema guard's inputs (tests replace them): this code's highest migration slot
    #: and how the home's schema is read
    required_schema: int = SCHEMA_REV
    schema_reader: Callable[[Any], Awaitable[HomeSchema]] | None = None
    _last_schema: float = 0.0
    _schema_unknown_logged: bool = False
    #: a node's start finished (it waits while the machine starts paused)
    _node_ready: bool = False
    _capabilities: dict[str, Any] = field(default_factory=dict)
    #: story 31.3: the home's own machine converts its stored paths by itself
    _auto_canon: bool = False
    _storage_dirty: bool = False

    @property
    def is_node(self) -> bool:
        return self.node_id is not None

    @property
    def paused(self) -> bool:
        """The home's schema does not fit this code: claim nothing, write nothing."""
        return self.selector.paused is not None

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
        self._capabilities = capabilities
        await self.check_schema()
        if self.paused:
            # the path map is built now (reads only): the cluster agent places its files
            # on the share with it, and nothing resolves a stored path through an empty
            # map (the home's /Volumes/... taken as a local path). The rest (storage
            # probe, report, switches) waits for the home: :meth:`refresh` finishes it
            try:
                await self._resolve_paths(beat)
            except Exception as exc:  # noqa: BLE001 - an older home schema: retried on resume
                _log.warning("cluster_path_map_deferred", error=str(exc))
            return
        await self._finish_node_start(beat)

    async def _finish_node_start(self, beat: nodes_db.NodeBeat | None = None) -> None:
        assert self.node_id is not None
        if beat is None:
            # resumed after a pause: the row as it is now (removed: the heartbeat stops us)
            beat = await nodes_db.beat(self.db, self.node_id)
            if beat is None:
                return
        await self._resolve_paths(beat)
        storage = await self._check_storage()
        await nodes_db.report(
            self.db,
            self.node_id,
            capabilities=self._capabilities,
            storage=storage,
            # the whole map this machine resolves with (its shares included), so the
            # Machines page and a streaming node on this machine see it
            inferred_path_map=None
            if self.explicit_path_map or self.admin_map
            else (self.path_map.as_json() or None),
        )
        self.selector.switches = await nodes_db.stage_switches(self.db, self.node_id)
        self.selector.lifted = await lifted_placements(self.db, self.graph)
        self._node_ready = True

    async def _resolve_paths(self, beat: nodes_db.NodeBeat) -> None:
        """The resolver: the admin's map, else the inferred one (else the stored one),
        with this machine's mounts and the storage servers. Reads only."""
        self.samples = await load_samples(self.db)
        self.servers = await load_servers(self.db)
        if not self.explicit_path_map:
            if beat.path_map_source == "admin" and beat.path_map is not None:
                self.admin_map = dict(beat.path_map)
            else:
                inferred = await asyncio.to_thread(self._infer)
                if inferred:
                    self.legacy_map = inferred
                elif beat.path_map:
                    self.legacy_map = dict(beat.path_map)
                _log.info("cluster_path_map_inferred", path_map=inferred or {})
        await asyncio.to_thread(self._rebuild)

    async def check_schema(self) -> bool:
        """Compare the home's schema with this code's; True when the pause changed.

        The pause and the resume are logged once each (not at every check)."""
        home = await (self.schema_reader or read_home_schema)(self.db)
        self._last_schema = time.monotonic()
        if home.applied is None and not self._schema_unknown_logged:
            self._schema_unknown_logged = True
            _log.warning("cluster_home_schema_unknown", required=self.required_schema)
        reason = pause_reason(home, self.required_schema)
        if reason == self.selector.paused:
            return False
        if reason is not None:
            _log.warning(
                "cluster_node_paused_schema",
                reason=reason,
                home_schema=home.applied,
                home_min_node_schema=home.min_node,
                required=self.required_schema,
            )
        else:
            _log.info(
                "cluster_node_resumed_schema",
                home_schema=home.applied,
                required=self.required_schema,
            )
        self.selector.paused = reason
        return True

    def _infer(self) -> dict[str, str]:
        paths = [p for lib in self.samples for p, _ in lib.videos]
        return infer_path_map(self.samples, candidate_roots(db_paths=paths))

    def _rebuild(self) -> None:
        """The resolver from the current sources and this machine's mounts now."""
        self._set_path_map(
            build_for_machine(
                self.servers,
                explicit=self.explicit if self.explicit_path_map else None,
                admin=None if self.explicit_path_map else self.admin_map,
                legacy=None if self.explicit_path_map else self.legacy_map,
                mounts=self.mounts,
            )
        )

    async def start_home(self) -> None:
        """The home's start (story 31.3): its servers, its admin override, its mounts,
        and the per-share storage check, kept on ``instance_identity``. On the home's
        own machine also the automatic path conversion (:mod:`..sharepath.auto`)."""
        self.samples = await load_samples(self.db)
        self.servers = await load_servers(self.db)
        self.admin_map = await _home_admin_map(self.db)
        await asyncio.to_thread(self._rebuild)
        await self._report_home()
        if not self.explicit_path_map:
            from ..engine.stages.storage_canonicalize import set_env
            from ..sharepath.auto import AutoEnv

            set_env(AutoEnv(mounts=self.mounts, on_change=self.storage_changed))
            self._auto_canon = True
            await self._check_canonicalize(full=True)

    def storage_changed(self) -> None:
        """The servers or the stored paths changed: rebuild and report at the next
        refresh (30 s), not in 5 minutes."""
        self._storage_dirty = True

    async def _check_canonicalize(self, *, full: bool) -> None:
        """Enqueue ``storage_canonicalize`` when it has something to do: an admin's
        request or a run cut short (looked at every refresh), else (``full``, with the
        storage check) a first look, a retry or a change in paths, mounts or servers."""
        if not self._auto_canon:
            return
        from ..sharepath import auto

        try:
            if full:
                why = await auto.due(self.db, auto.AutoEnv(mounts=self.mounts))
            else:
                state, req = await auto.load_state(self.db)
                why = (
                    f"request:{req}"
                    if req
                    else "resume"
                    if state.get("phase") in auto.ACTIVE
                    else None
                )
            if why is not None and await auto.enqueue(self.db, why.replace(":", "_")):
                _log.info("storage_canonicalize_enqueued", why=why)
        except Exception:  # noqa: BLE001 - an older schema, a busy database: next time
            _log.debug("storage_canonicalize_check_failed", exc_info=True)

    async def _report_home(self) -> None:
        storage = await self._check_storage()
        cast = "::jsonb" if is_pg(self.db) else ""
        try:
            await self.db.execute(
                f"UPDATE instance_identity SET storage = $1{cast} WHERE id = 1",
                json.dumps(storage),
            )
        except Exception as exc:  # noqa: BLE001 - an older schema: the check still ran
            _log.debug("home_storage_report_skipped", error=str(exc))

    async def _refresh_resolver(self) -> None:
        """Every storage check: servers may have been added, shares (re)mounted."""
        self.samples = await load_samples(self.db)
        self.servers = await load_servers(self.db)
        if not self.is_node:
            self.admin_map = await _home_admin_map(self.db)
        await asyncio.to_thread(self._rebuild)

    def _set_path_map(self, pm: PathMap) -> None:
        self.path_map = pm
        set_active_path_map(pm)
        if self.runtime is not None:
            self.runtime.path_map = pm

    async def _check_storage(self) -> dict[str, Any]:
        tag = self.node8 or "home"
        result = await asyncio.to_thread(check_storage, self.samples, self.path_map, tag)
        result["shares"] = await asyncio.to_thread(check_shares, self.samples, self.path_map, tag)
        share_list = getattr(self.path_map, "share_list", None)
        result["mounts"] = share_list() if callable(share_list) else []
        result["path_map"] = self.path_map.as_json()
        if self.is_node:
            # the home only reports its check: its stages never stop on it
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
            if self.paused or time.monotonic() - self._last_schema >= self.storage_check_sec:
                await self.check_schema()
            if self.paused:
                return await self.apply()
            if not self._node_ready:
                await self._finish_node_start()
            self.selector.switches = await nodes_db.stage_switches(self.db, self.node_id)
            self.selector.lifted = await lifted_placements(self.db, self.graph)
            if time.monotonic() - self._last_storage >= self.storage_check_sec:
                await self._refresh_resolver()
                storage = await self._check_storage()
                await nodes_db.report(self.db, self.node_id, storage=storage)
        elif self._last_storage and (
            self._storage_dirty or time.monotonic() - self._last_storage >= self.storage_check_sec
        ):
            full = not self._storage_dirty
            self._storage_dirty = False
            await self._refresh_resolver()
            await self._report_home()
            await self._check_canonicalize(full=full)
        elif self._last_storage:
            await self._check_canonicalize(full=False)
        if self.node_id is None:
            await self._sweep_enrich()
        return await self.apply()

    async def _sweep_enrich(self) -> None:
        """The home turns queued enrich_jobs (Find metadata, Re-enrich, automatic) into
        engine ``enrich`` jobs (:mod:`..enrich.sweep`)."""
        from ..enrich.sweep import sweep

        try:
            await sweep(self.db)
        except Exception:  # noqa: BLE001 - an older schema, a busy database: next time
            _log.debug("enrich_sweep_failed", exc_info=True)

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
            and beat.path_map != self.admin_map
        ):
            _log.info("cluster_path_map_set_by_admin", path_map=beat.path_map)
            self.admin_map = dict(beat.path_map)
            await asyncio.to_thread(self._rebuild)

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
