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

The same pause holds while the home's database does not answer the node's beat
(:meth:`WorkerAgent.beat`, :data:`HOME_LOST_AFTER` beats in a row): the node claims
nothing; its jobs in flight finish, unless the outage nears the claim lease, when they are
stopped, since the home would then hand them to another machine. That is measured from
the last beat the home answered (:meth:`WorkerAgent.run_lease_watch`, every
:data:`LEASE_CHECK_SEC`; :func:`stop_jobs_after` keeps a margin under the lease): a home
that hangs rather than refuses leaves each beat waiting its timeout, and the beat loop
comes round only every ~26 s then. Should a job's write still come late, it changes
nothing once another worker holds the claim (``db/claim_owner.py``). At the first beat
the home answers again the node releases what its ended jobs left claimed and claims
again. The worker's status file (cluster agent) is rewritten at each pause and resume.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import time
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any, Protocol

from ..engine.graph import StageDef
from ..engine.paths import PathMap, set_active_path_map
from ..engine.sql import is_pg, loads
from ..engine.sync import stages_ahead
from ..engine.workers import update_worker_stages
from ..log import get_logger
from ..pipeline.shutdown import release_claims
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

__all__ = [
    "BEAT_TIMEOUT_SEC",
    "HOME_LOST_AFTER",
    "STAGE_REFRESH_SEC",
    "LEASE_CHECK_SEC",
    "STOP_JOBS_AFTER_SEC",
    "STOP_JOBS_MARGIN_SEC",
    "STORAGE_CHECK_SEC",
    "WorkerAgent",
    "home_unreachable",
    "path_map_from_json",
    "stop_jobs_after",
]

STAGE_REFRESH_SEC = 30.0
STORAGE_CHECK_SEC = 300.0
#: a node beat the home's database has not answered by then counts as unanswered (a
#: powered-off home leaves a call hanging rather than failing)
BEAT_TIMEOUT_SEC = 8.0
#: unanswered beats in a row (one each 10 s) before the node pauses: one lost beat is a blip
HOME_LOST_AFTER = 2
#: no beat answered for this long, a node stops its jobs in flight: the claim lease (90 s
#: without a job heartbeat) lapses soon after, and the home then hands those jobs to
#: another machine (the runtime sets it from the configured lease: :func:`stop_jobs_after`)
STOP_JOBS_AFTER_SEC = 70.0
#: kept under the lease: the last job heartbeat the home took may be one heartbeat older
#: than the last beat it answered, the check runs every few seconds, cancelling takes a
#: moment, and the home's clock is not this machine's
STOP_JOBS_MARGIN_SEC = 15.0
#: how often a node checks the lease while it runs jobs
LEASE_CHECK_SEC = 2.0


def stop_jobs_after(stale_claim_sec: float, heartbeat_sec: float) -> float:
    """How long after the last beat the home answered a node stops its jobs in flight,
    for a claim lease of ``stale_claim_sec`` and job heartbeats every ``heartbeat_sec``."""
    return max(stale_claim_sec - heartbeat_sec - STOP_JOBS_MARGIN_SEC, stale_claim_sec / 2)


#: asyncpg's errors for a connection that failed, closed or is refused for now (by name,
#: as :mod:`.exitcodes` matches the login refusals): class 08 and 57P03
_CONNECTION_ERRORS = (
    "PostgresConnectionError",
    "CannotConnectNowError",
    "ConnectionDoesNotExistError",
)


def home_unreachable(exc: BaseException) -> bool:
    """The home's database did not answer (as opposed to answering with an error)."""
    if isinstance(exc, (OSError, TimeoutError, asyncio.TimeoutError)):  # noqa: UP041
        return True
    names = {c.__name__ for c in type(exc).__mro__}
    if names.intersection(_CONNECTION_ERRORS):
        return True
    # asyncpg's "connection is closed" / "pool is closing"
    return "InterfaceError" in names and "clos" in str(exc)


class InFlight(Protocol):
    """The claim loop, as a node's beat sees it."""

    in_flight: set[asyncio.Task[None]]

    def stop_in_flight(self) -> int: ...


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
    #: the claim loop (the runtime sets it): its jobs in flight when the home goes away
    claims: InFlight | None = None
    beat_timeout_sec: float = BEAT_TIMEOUT_SEC
    stop_jobs_after_sec: float = STOP_JOBS_AFTER_SEC
    #: called when the pause changes (the cluster agent rewrites the status file)
    pause_listeners: list[Callable[[], None]] = field(default_factory=list)
    #: the last beat the home's database answered (monotonic; the start's counts)
    _answered_at: float = field(default_factory=time.monotonic)
    #: the current home outage: unanswered beats in a row, since when (monotonic), whether
    #: its jobs were stopped
    _beats_lost: int = 0
    _lost_at: float | None = None
    _jobs_stopped: bool = False
    _capabilities: dict[str, Any] = field(default_factory=dict)
    #: story 31.3: the home's own machine converts its stored paths by itself
    _auto_canon: bool = False
    _storage_dirty: bool = False

    @property
    def is_node(self) -> bool:
        return self.node_id is not None

    @property
    def paused(self) -> bool:
        """The home's schema does not fit this code, or a node cannot reach the home's
        database: claim nothing, write nothing."""
        return self.selector.paused is not None or self.selector.unreachable is not None

    @property
    def node8(self) -> str:
        return (self.node_id or "").replace("-", "")[:8]

    # -- start -----------------------------------------------------------------

    async def start_node(self, capabilities: dict[str, Any]) -> None:
        """A node's start: its row's state, the path map (stored, or inferred and
        reported), the storage check, the switches. Call before the first compute."""
        assert self.node_id is not None
        beat = await nodes_db.beat(self.db, self.node_id)
        self._answered_at = time.monotonic()
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
        self._pause_changed()
        return True

    def _pause_changed(self) -> None:
        for listener in list(self.pause_listeners):
            try:
                listener()
            except Exception:  # noqa: BLE001 - a status file is not worth a beat
                _log.debug("cluster_pause_listener_failed", exc_info=True)

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
        if self.selector.unreachable is not None:
            return False  # its reads would fail: the beat brings the node back
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
        """A node's heartbeat on its cluster row: revoked -> stop; drain -> claim nothing;
        the home's database not answering -> claim nothing until it does again."""
        if self.node_id is None:
            return
        try:
            beat = await asyncio.wait_for(
                nodes_db.beat(self.db, self.node_id), timeout=self.beat_timeout_sec
            )
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
            elif home_unreachable(exc):
                self._home_lost(exc)
                return
            else:
                # an error the database answered: it is there (the lease is not at risk)
                self._answered_at = time.monotonic()
            raise
        await self._home_back()
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

    def _home_lost(self, exc: BaseException) -> None:
        """A beat the home's database did not answer (see the module doc)."""
        now = time.monotonic()
        self._beats_lost += 1
        if self._lost_at is None:
            self._lost_at = now
        if self.selector.unreachable is None and self._beats_lost >= HOME_LOST_AFTER:
            self._pause_unreachable(f"{type(exc).__name__}: {exc}")
        self.check_lease(now)

    def _pause_unreachable(self, error: str) -> None:
        since = datetime.now(UTC).strftime("%Y-%m-%d %H:%M UTC")
        self.selector.unreachable = f"paused: the home's database does not answer (since {since})"
        # compute, not apply: apply writes the set to the home
        self.selector.compute()
        _log.warning("cluster_node_paused_home_unreachable", error=error, since=since)
        self._pause_changed()

    def check_lease(self, now: float | None = None) -> None:
        """No beat answered for :attr:`stop_jobs_after_sec`: stop the jobs in flight
        (once per outage) and claim nothing, before the home hands them to another
        machine."""
        if self.node_id is None or self._jobs_stopped:
            return
        now = time.monotonic() if now is None else now
        silent = now - self._answered_at
        if silent < self.stop_jobs_after_sec:
            return
        self._jobs_stopped = True
        if self.selector.unreachable is None:
            self._pause_unreachable(f"no beat answered for {round(silent)} s")
        stopped = self.claims.stop_in_flight() if self.claims is not None else 0
        if stopped:
            _log.warning(
                "cluster_node_jobs_stopped_home_unreachable", jobs=stopped, silent_sec=round(silent)
            )

    async def run_lease_watch(self, interval: float = LEASE_CHECK_SEC) -> None:
        """:meth:`check_lease` every ``interval``, whatever the beat loop waits on. It
        starts with the claim loop: no job here is older than its start."""
        self._answered_at = max(self._answered_at, time.monotonic())
        while not self.shutdown.is_set():
            with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
                await asyncio.wait_for(self.shutdown.wait(), timeout=interval)
                return
            self.check_lease()

    async def _home_back(self) -> None:
        """A beat the home's database answered: a node paused for it releases what its
        ended jobs left claimed (nothing in flight here then holds a claim) and resumes."""
        lost_at = self._lost_at
        self._beats_lost, self._lost_at = 0, None
        self._answered_at = time.monotonic()
        if self.selector.unreachable is None:
            self._jobs_stopped = False
            return
        released: list[int] = []
        if self.claims is not None and not any(not t.done() for t in self.claims.in_flight):
            try:
                released = await release_claims(self.db, self.worker_id)
            except Exception:  # noqa: BLE001 - the claim lease releases them later
                _log.warning("cluster_node_release_claims_failed", exc_info=True)
        self.selector.unreachable = None
        self._jobs_stopped = False
        _log.info(
            "cluster_node_resumed_home",
            after_sec=None if lost_at is None else round(time.monotonic() - lost_at),
            released=released,
        )
        self._pause_changed()
        await self.apply()

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
