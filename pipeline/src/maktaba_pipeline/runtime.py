"""Pipeline daemon runtime.

Glues together the connection wrapper, claim loop, heartbeat, reaper,
and per-stage dispatch. ``python -m maktaba_pipeline run`` lands here
via :mod:`maktaba_pipeline.__main__`.

The connection wrapper is a thin facade satisfying
:class:`maktaba_pipeline.db.jobs.DBConn` for both Postgres
(``asyncpg``) and SQLite (``aiosqlite``). The full Story 1.5 wrapper
adds connection pooling, automatic reconnect, and a typed
transaction context manager; this module ships the minimum the
worker loop actually needs.

The stage dispatch table maps a :class:`~maktaba_pipeline.db.jobs.Stage`
to an awaitable handler ``(db, job) -> None``. Each handler is
responsible for: (a) doing the real work via the underlying Epic 1–6
modules, (b) committing its DB writes inside a transaction, and (c)
flipping the job to ``done`` / ``failed`` via the
:mod:`maktaba_pipeline.db.jobs_state` helpers. The built-in handlers
ship as no-op shims that log the dispatch and mark the job ``done`` —
the real per-stage business logic plugs in by importing the relevant
module (Epic 2's ``audio.probe.commit_probe``, Epic 3's STT registry,
Epic 4's ``subtitle.generator``, etc.) and registering a richer
handler before :func:`run` is called.
"""

from __future__ import annotations

import asyncio
import contextlib
import contextvars
import os
import re
from collections.abc import AsyncIterator, Awaitable, Callable, Coroutine, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from . import __version__
from .channels.pass_ import run_topup_loop
from .cluster import exitcodes
from .cluster.agent import enabled as cluster_agent_enabled
from .cluster.capabilities import capabilities
from .cluster.node import WorkerAgent
from .cluster.stages import StageSelector, select_stages
from .db.jobs import DBConn, Job, Stage
from .db.jobs_state import StageError, mark_done, mark_failed_or_retry
from .engine.dispatch import EngineRuntime, dispatch_job
from .engine.graph import StageDef
from .engine.health import run_network_probes
from .engine.loader import build_full_graph, register_extension_impls
from .engine.paths import PathMap, set_active_path_map
from .engine.prune import run_pruner
from .engine.reconcile import run_reconciler
from .engine.sync import stages_ahead, sync_graph
from .engine.workers import (
    deregister_worker,
    heartbeat_worker,
    register_worker,
    stable_hostname,
)
from .extensions import discover
from .extensions.store import ensure_rows, load_state
from .log import get_logger
from .pipeline.heartbeat import DEFAULT_HEARTBEAT_SEC, heartbeat_for
from .pipeline.reaper import Reaper
from .pipeline.runner import ClaimLoop, WorkerConfig, default_worker_id, install_signal_handlers

__all__ = [
    "Database",
    "RuntimeConfig",
    "StageHandler",
    "build_default_dispatch",
    "connect",
    "run",
]


_log = get_logger()


StageHandler = Callable[[DBConn, Job], Awaitable[None]]
#: a background writer's loop: runs until the event it is given is set
WriterLoop = Callable[[asyncio.Event], Coroutine[Any, Any, None]]


@dataclass(slots=True)
class RuntimeConfig:
    """Worker daemon configuration.

    Everything is sourced from environment variables or CLI flags by
    the caller; this dataclass is the single in-process source of
    truth once the args are parsed.
    """

    database_url: str
    stages: tuple[Stage | str, ...]
    worker_id: str | None = None
    #: this host's name in the worker registry (default: the machine's hostname)
    host: str | None = None
    #: this host's own path map, ``db_prefix=local_prefix;...`` (see engine/paths.py)
    path_map: str = ""
    #: stages this worker must not serve (for example the audio chain on a host without a GPU)
    exclude_stages: tuple[str, ...] = ()
    #: run through the queue-graph engine (pools, change-driven propagation, extensions)
    engine: bool = True
    heartbeat_sec: float = DEFAULT_HEARTBEAT_SEC
    stale_claim_sec: float = 90.0
    reaper_interval_sec: float = 30.0
    claim_poll_sec: float = 1.0
    claim_poll_max_sec: float = 30.0
    #: Deprecated: per-stage caps were never wired (F10). Concurrency now comes from
    #: ``worker_pools``; kept so existing callers still construct the config.
    stage_concurrency: dict[Stage, int] = field(default_factory=dict)
    #: ``home`` (the home's own workers) or ``node`` (a machine that joined it, Epic 31):
    #: a node never syncs the stage registry, claims no home-only stage and follows its
    #: ``cluster_nodes`` row (revoked, drain, stage switches, path map)
    role: str = "home"
    #: the joined machine's id (role ``node``)
    node_id: str | None = None
    #: how often the served stages are re-evaluated (design section 5)
    stage_refresh_sec: float = 30.0


_TX_CONN: contextvars.ContextVar[Any] = contextvars.ContextVar("maktaba_tx_conn", default=None)


class Database:
    """Minimal :class:`DBConn`-compatible facade over asyncpg / aiosqlite.

    Postgres uses an :class:`asyncpg.Pool` so the reaper, claim loop,
    and dispatch can run concurrently without contending on a single
    connection's `_stmt_exclusive_section`. SQLite uses a single
    aiosqlite connection (the driver serializes its own work through
    an executor thread, and aiosqlite doesn't expose a pool primitive).

    Transactions are task-scoped: inside ``async with db.transaction()``
    every ``fetchrow`` / ``fetch`` / ``execute`` of the same task runs on
    the transaction's connection (Postgres) or under the transaction's lock
    (SQLite), and a nested ``transaction()`` joins the outer one (a
    savepoint on Postgres). Other tasks wait for the SQLite connection, so
    concurrent tasks cannot interleave statements into someone else's
    transaction. SQLite runs in autocommit mode (``isolation_level=None``):
    a statement outside a transaction commits on its own.

    The dialect is inferred from the URL scheme:

    - ``postgres://`` / ``postgresql://`` → asyncpg pool
    - ``sqlite://`` / ``file:`` / ``:memory:`` → aiosqlite (single conn)
    """

    dialect: str
    #: lets ``db/jobs_state`` call into ``engine/hooks`` (error_cause, job_events, follow-ups)
    engine_hooks = True

    def __init__(self, *, dialect: str, conn: Any = None, pool: Any = None, dsn: str = "") -> None:
        self.dialect = dialect
        self._conn = conn  # sqlite path only
        self._pool = pool  # postgres path only
        self._dsn = dsn  # only used by acquire_listener (postgres only)
        self._lock: asyncio.Lock | None = None  # sqlite only, created lazily on the loop

    @classmethod
    async def connect(cls, url: str) -> Database:
        if url.startswith(("postgres://", "postgresql://")):
            asyncpg = _import_asyncpg()
            # min_size=1 keeps a warm connection so the first reaper
            # tick doesn't pay the TCP+TLS+auth round-trip; max_size=10
            # is generous for a single pipeline daemon (reaper + claim
            # loop + per-job dispatch peaks at single-digit concurrency).
            pool = await asyncpg.create_pool(url, min_size=1, max_size=10)
            return cls(dialect="postgres", pool=pool, dsn=url)

        aiosqlite = _import_aiosqlite()
        path = _sqlite_path_from_url(url)
        conn = await aiosqlite.connect(path, isolation_level=None)
        conn.row_factory = aiosqlite.Row
        await conn.execute("PRAGMA foreign_keys = ON")
        # other worker processes hold the write lock briefly (a claim is BEGIN IMMEDIATE):
        # wait for it rather than fail with "database is locked" after the 5 s default
        await conn.execute("PRAGMA busy_timeout = 30000")
        return cls(dialect="sqlite", conn=conn)

    def _sqlite_lock(self) -> asyncio.Lock:
        if self._lock is None:
            self._lock = asyncio.Lock()
        return self._lock

    @contextlib.asynccontextmanager
    async def transaction(self) -> AsyncIterator[Any]:
        outer = _TX_CONN.get()
        if self.dialect == "postgres":
            if outer is not None:
                async with outer.transaction():  # savepoint
                    yield outer
                return
            async with self._pool.acquire() as conn, conn.transaction():
                token = _TX_CONN.set(conn)
                try:
                    yield conn
                finally:
                    _TX_CONN.reset(token)
            return
        if outer is not None:
            yield self._conn  # join the outer transaction
            return
        async with self._sqlite_lock():
            await self._conn.execute("BEGIN IMMEDIATE")
            token = _TX_CONN.set(self._conn)
            try:
                yield self._conn
            except BaseException:
                await self._conn.execute("ROLLBACK")
                raise
            else:
                await self._conn.execute("COMMIT")
            finally:
                _TX_CONN.reset(token)

    @contextlib.asynccontextmanager
    async def _pg_conn(self) -> AsyncIterator[Any]:
        tx = _TX_CONN.get()
        if tx is not None:
            yield tx
            return
        async with self._pool.acquire() as conn:
            yield conn

    @contextlib.asynccontextmanager
    async def _sqlite_guard(self) -> AsyncIterator[None]:
        if _TX_CONN.get() is not None:
            yield
            return
        async with self._sqlite_lock():
            yield

    async def fetchrow(self, sql: str, *args: Any) -> Any:
        if self.dialect == "postgres":
            async with self._pg_conn() as conn:
                return await conn.fetchrow(sql, *args)
        sql_q, params = _rewrite_for_sqlite(sql, args)
        async with self._sqlite_guard(), self._conn.execute(sql_q, params) as cursor:
            return await cursor.fetchone()

    async def fetch(self, sql: str, *args: Any) -> list[Any]:
        if self.dialect == "postgres":
            async with self._pg_conn() as conn:
                return list(await conn.fetch(sql, *args))
        sql_q, params = _rewrite_for_sqlite(sql, args)
        async with self._sqlite_guard(), self._conn.execute(sql_q, params) as cursor:
            return list(await cursor.fetchall())

    async def fetchval(self, sql: str, *args: Any) -> Any:
        row = await self.fetchrow(sql, *args)
        return None if row is None else row[0]

    async def execute(self, sql: str, *args: Any) -> Any:
        if self.dialect == "postgres":
            async with self._pg_conn() as conn:
                return await conn.execute(sql, *args)
        sql_q, params = _rewrite_for_sqlite(sql, args)
        async with self._sqlite_guard():
            await self._conn.execute(sql_q, params)
        return None

    async def executemany(self, sql: str, rows: Sequence[Sequence[Any]]) -> None:
        """One statement for many parameter rows, in one round trip per batch (bulk
        imports: a row-by-row ``execute`` pays a round trip per row)."""
        if not rows:
            return
        if self.dialect == "postgres":
            async with self._pg_conn() as conn:
                await conn.executemany(sql, rows)
            return
        sql_q, _ = _rewrite_for_sqlite(sql, tuple(rows[0]))
        params = [_rewrite_for_sqlite(sql, tuple(r))[1] for r in rows]
        async with self._sqlite_guard():
            await self._conn.executemany(sql_q, params)

    async def acquire_listener(self) -> Any:
        # LISTEN/NOTIFY needs a dedicated, long-lived connection.
        # `pool.acquire()` returns a context manager whose lifetime is
        # tied to a `with` block; the listener wants the connection
        # past that scope. Open a fresh connection outside the pool
        # so the listener's LISTEN state never blocks pool acquires.
        if self.dialect != "postgres":
            raise RuntimeError("acquire_listener is Postgres-only")
        asyncpg = _import_asyncpg()
        return await asyncpg.connect(self._dsn)

    async def close(self) -> None:
        with contextlib.suppress(Exception):
            if self.dialect == "postgres" and self._pool is not None:
                await self._pool.close()
            elif self._conn is not None:
                await self._conn.close()


def connect(url: str) -> Awaitable[Database]:
    """Module-level shortcut used by the entry point and tests."""

    return Database.connect(url)


def build_default_dispatch(
    overrides: Mapping[Stage | str, StageHandler] | None = None,
    *,
    engine: EngineRuntime | None = None,
) -> StageHandler:
    """Return a dispatch callable that routes by ``job.stage``.

    Each built-in handler is a placeholder that logs the dispatch and
    marks the job ``done``. Real handlers plug in via ``overrides``;
    the daemon entry point passes the registered handlers in from the
    runtime config, and tests can install fakes for behavioural
    assertions.

    With ``engine`` the routing goes through :func:`engine.dispatch.dispatch_job`:
    legacy stages run their handler and then the engine bookkeeping
    (output hash, propagation); every other stage runs through the engine
    (skip check, handler, change propagation).
    """

    table: dict[str, StageHandler] = {
        Stage.SCAN.value: _placeholder_handler("scan"),
        Stage.PROBE.value: _placeholder_handler("probe"),
        Stage.EXTRACT.value: _placeholder_handler("extract"),
        Stage.TRANSCRIBE.value: _placeholder_handler("transcribe"),
        Stage.SUBTITLE_GEN.value: _placeholder_handler("subtitle_gen"),
        Stage.INDEX.value: _placeholder_handler("index"),
        Stage.THUMBNAIL.value: _placeholder_handler("thumbnail"),
    }
    if overrides:
        table.update({str(k): v for k, v in overrides.items()})

    async def dispatch(db: DBConn, job: Job) -> None:
        if engine is not None:
            await dispatch_job(db, job, runtime=engine, legacy_handlers=table)
            return
        handler = table.get(str(job.stage))
        if handler is None:
            err = StageError(
                kind="dispatch_unknown_stage",
                message=f"no handler registered for stage {job.stage}",
                retryable=False,
            )
            await mark_failed_or_retry(db, job_id=job.id, error=err)
            return
        async with heartbeat_for(db, job_id=job.id):
            await handler(db, job)

    # The ClaimLoop's dispatch protocol is ``Callable[[Job], Awaitable[None]]``;
    # bind ``db`` here so we can hand it the closure-bound callable.
    return dispatch


def _placeholder_handler(stage_name: str) -> StageHandler:
    """Default stage handler — log + mark done.

    Real per-stage implementations live in Epic 1-6 modules; until
    those are wired in via the dispatch override map, the worker
    still drains the queue rather than parking jobs forever.
    """

    async def _run(db: DBConn, job: Job) -> None:
        _log.info(
            "stage_handler_placeholder",
            stage=stage_name,
            job_id=job.id,
            video_id=str(job.video_id) if job.video_id is not None else None,
        )
        await mark_done(db, job_id=job.id)

    return _run


async def run(
    cfg: RuntimeConfig,
    *,
    dispatch_overrides: Mapping[Stage | str, StageHandler] | None = None,
    db: Database | None = None,
) -> int:
    """Run the worker daemon until SIGTERM/SIGINT.

    Returns the process exit code. Tests can pre-build a ``db`` and
    pass an :func:`build_default_dispatch`-compatible override map; the
    CLI builds both from environment + flags before calling this.

    With ``cfg.engine`` the daemon also: discovers the extensions and builds
    the full stage graph, syncs it into ``job_stages``, registers this worker
    (host, served stages, path map) and heartbeats, claims per resource pool,
    samples health, and runs the reconciler and the retention job.
    """

    owned_db = db is None
    database = db or await Database.connect(cfg.database_url)
    worker_id = cfg.worker_id or default_worker_id()
    shutdown = asyncio.Event()
    install_signal_handlers(shutdown)
    background: list[asyncio.Task[None]] = []
    engine_rt: EngineRuntime | None = None
    stage_names: tuple[str, ...] = tuple(str(s) for s in cfg.stages)
    stage_pools: dict[str, str] = {}
    host = cfg.host or stable_hostname()
    selector: StageSelector | None = None
    agent: WorkerAgent | None = None

    if cfg.engine:
        started = await _start_engine(database, cfg, worker_id, host, shutdown, dispatch_overrides)
        engine_rt, background = started.runtime, started.tasks
        selector = started.selector
        agent = started.agent
        stage_names = started.served
        # every graph stage: the served set changes while the worker runs
        stage_pools = {name: st.pool for name, st in engine_rt.graph.items()}

    _log.info(
        "pipeline_runtime_starting",
        dialect=database.dialect,
        worker_id=worker_id,
        host=host,
        stages=list(stage_names),
        engine=cfg.engine,
        role=cfg.role,
        node_id=cfg.node_id,
    )
    if not stage_names:
        _log.warning("pipeline_no_stages_to_serve")

    worker_cfg = WorkerConfig(
        supported_stages=stage_names,
        claim_poll_sec=cfg.claim_poll_sec,
        claim_poll_max_sec=cfg.claim_poll_max_sec,
        worker_id=worker_id,
        pool_mode=cfg.engine,
        stage_pools=stage_pools,
        dynamic_stages=selector.current if selector is not None else None,
    )

    dispatch_for_db = build_default_dispatch(dispatch_overrides, engine=engine_rt)

    async def _loop_dispatch(job: Job) -> None:
        await dispatch_for_db(database, job)

    claim_loop = ClaimLoop(
        database,
        worker_cfg,
        _loop_dispatch,
        semaphores=(None if cfg.engine else {s: asyncio.Semaphore(1) for s in cfg.stages}),
        shutdown_event=shutdown,
    )

    def node_paused() -> bool:
        return agent is not None and agent.paused

    reaper = Reaper(
        database,
        interval_sec=cfg.reaper_interval_sec,
        stale_claim_sec=cfg.stale_claim_sec,
        heartbeat_sec=cfg.heartbeat_sec,
        # a paused node (its code needs migrations the home lacks) writes nothing
        paused=node_paused if agent is not None and agent.is_node else None,
    )
    reaper.start()

    try:
        if stage_names or selector is not None:
            await claim_loop.run()
        else:
            await shutdown.wait()
    finally:
        shutdown.set()
        await reaper.stop()
        for task in background:
            task.cancel()
        for task in background:
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await task
        if cfg.engine:
            with contextlib.suppress(Exception):
                await deregister_worker(database, worker_id)
        if owned_db:
            await database.close()
        _log.info("pipeline_runtime_stopped")
    # 77 when the home refused this machine for good (revoked): services stop restarting
    return exitcodes.exit_code(0)


@dataclass(slots=True)
class EngineStart:
    """What :func:`_start_engine` hands to :func:`run`."""

    runtime: EngineRuntime
    selector: StageSelector
    agent: WorkerAgent
    tasks: list[asyncio.Task[None]]

    @property
    def served(self) -> tuple[str, ...]:
        return self.selector.served

    @property
    def stage_pools(self) -> dict[str, str]:
        return {s: self.runtime.graph[s].pool for s in self.selector.served}


async def _start_engine(
    database: Database,
    cfg: RuntimeConfig,
    worker_id: str,
    host: str,
    shutdown: asyncio.Event,
    overrides: Mapping[Stage | str, StageHandler] | None,
) -> EngineStart:
    """Extension discovery, graph sync, worker registration and background tasks.

    A node (``cfg.role == "node"``) never writes the stage registry or the extension
    rows: the home's workers own them, and a node running older code would roll them
    back. It still refuses stages whose database version is ahead of its code.
    """
    is_node = cfg.role == "node"
    if is_node and not cfg.node_id:
        raise ValueError("a node worker needs its node id")
    extensions = discover()
    if not is_node:
        await ensure_rows(database, extensions)
    states = {e.manifest.name: await load_state(database, e) for e in extensions}
    graph = build_full_graph(extensions)
    register_extension_impls(extensions, states)
    if is_node:
        _log.info("node_leaves_the_stage_registry_to_the_home", node_id=cfg.node_id)
    else:
        report = await sync_graph(database, graph, states)
        if report.version_changes:
            _log.info("graph_versions_changed", changes=dict(report.version_changes))
    ahead = await stages_ahead(database, graph)
    for name, db_version in ahead.items():
        _log.warning("graph_version_ahead", stage=name, db_version=db_version)

    path_map = PathMap.parse(cfg.path_map)
    set_active_path_map(path_map)
    runtime = EngineRuntime(
        graph={s.name: s for s in graph},
        extension_states=states,
        path_map=path_map,
    )
    handlers = frozenset(str(k) for k in (overrides or {}))
    selector = StageSelector(
        graph=graph,
        requested=frozenset(str(s) for s in cfg.stages) if cfg.stages else None,
        excluded=frozenset(cfg.exclude_stages),
        handlers=handlers,
        role=cfg.role,
        ahead=dict(ahead),
    )
    agent = WorkerAgent(
        db=database,
        worker_id=worker_id,
        selector=selector,
        graph=graph,
        shutdown=shutdown,
        node_id=cfg.node_id if is_node else None,
        explicit_path_map=bool(cfg.path_map.strip()),
        path_map=path_map,
        explicit=path_map,
        runtime=runtime,
    )
    caps = await asyncio.to_thread(capabilities)
    if is_node:
        await agent.start_node(caps)
    else:
        try:
            # story 31.3: canonical share paths resolve through this machine's mounts
            await agent.start_home()
        except Exception:  # noqa: BLE001 - the legacy map keeps working
            _log.exception("home_storage_start_failed")
    selector.compute()

    collector = runtime.nas.collector
    for ext in extensions:
        probes = getattr(ext, "health_probes", None)
        state = states.get(ext.manifest.name)
        if callable(probes) and state is not None and state.enabled and state.configured:
            collector.probes.update(probes(state.settings))

    async def register() -> None:
        await register_worker(
            database,
            worker_id=worker_id,
            host=host,
            stages=selector.served,
            pools=selector.pools,
            path_map=agent.path_map.as_json(),
            version=__version__,
            node_id=agent.node_id,
            role=cfg.role,
            capabilities=caps,
            stages_off=selector.stages_off,
        )

    await register()

    def writer(name: str, loop: WriterLoop) -> asyncio.Task[None]:
        # on a node the writers run only while the home's schema fits this code
        body = _while_unpaused(agent, shutdown, loop, name) if is_node else loop(shutdown)
        return asyncio.create_task(body, name=name)

    after_topup = tuple(s.name for s in graph if s.after_topup)
    tasks = [
        asyncio.create_task(
            _worker_heartbeat(database, worker_id, shutdown, host, register, agent.beat),
            name="worker-hb",
        ),
        asyncio.create_task(agent.run_refresher(cfg.stage_refresh_sec), name="stage-selection"),
        writer("reconciler", lambda stop: run_reconciler(database, runtime.graph, stop)),
        writer("pruner", lambda stop: run_pruner(database, stop)),
        writer(
            "channel-topup",
            lambda stop: run_topup_loop(database, stop, listeners=after_topup),
        ),
    ]
    if cluster_agent_enabled():
        # the live channel to the home (design-agent.md): metrics (the health numbers
        # among them: workers never write health_samples), logs, job events, LLM
        from .cluster.agent.service import run_for_worker

        tasks.append(asyncio.create_task(run_network_probes(collector, shutdown), name="health"))

        tasks.append(
            asyncio.create_task(
                run_for_worker(
                    database,
                    worker_id=worker_id,
                    host=host,
                    shutdown=shutdown,
                    worker=agent,
                    health=collector,
                ),
                name="cluster-agent",
            )
        )
    return EngineStart(runtime=runtime, selector=selector, agent=agent, tasks=tasks)


def _served_stages(
    cfg: RuntimeConfig,
    graph: Sequence[StageDef],
    handlers: set[str],
    ahead: Mapping[str, int],
) -> list[str]:
    """Stages this worker claims (the static part of :func:`cluster.stages.select_stages`).

    Default: every graph stage that has a handler here (legacy stages need an
    entry in the dispatch override map; engine stages need an implementation).
    ``cfg.stages`` narrows it; stages whose database version is ahead of this
    worker's graph are never claimed; a node claims no home-only stage.
    """
    served, _ = select_stages(
        graph,
        requested={str(s) for s in cfg.stages} if cfg.stages else None,
        excluded=set(cfg.exclude_stages),
        handlers=handlers,
        ahead=ahead,
        role=cfg.role,
    )
    return served


async def _while_unpaused(
    agent: WorkerAgent,
    shutdown: asyncio.Event,
    loop: WriterLoop,
    name: str,
    *,
    poll_sec: float = 5.0,
) -> None:
    """Run ``loop(stop)`` while ``agent`` is not paused (cluster/schema.py): a pause sets
    the loop's own stop event, a resume starts it again; ``shutdown`` ends both."""
    while not shutdown.is_set():
        if agent.paused:
            with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
                await asyncio.wait_for(shutdown.wait(), timeout=poll_sec)
            continue
        stop = asyncio.Event()
        task = asyncio.ensure_future(loop(stop))
        while not shutdown.is_set() and not agent.paused and not task.done():
            with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
                await asyncio.wait_for(shutdown.wait(), timeout=poll_sec)
        stop.set()
        if agent.paused and not shutdown.is_set():
            _log.info("node_writer_paused", writer=name)
        try:
            await asyncio.wait_for(task, timeout=30.0)
        except (TimeoutError, asyncio.TimeoutError):  # noqa: UP041
            task.cancel()
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await task
        except Exception:  # noqa: BLE001 - the loops log their own failures
            _log.exception("node_writer_failed", writer=name)
        if task.done() and not shutdown.is_set() and not agent.paused:
            # the loop ended on its own: do not spin
            return


async def _worker_heartbeat(
    database: Database,
    worker_id: str,
    stop: asyncio.Event,
    host: str | None = None,
    register: Callable[[], Awaitable[None]] | None = None,
    node_beat: Callable[[], Awaitable[None]] | None = None,
) -> None:
    while not stop.is_set():
        try:
            if not await heartbeat_worker(database, worker_id, host) and register is not None:
                _log.warning("worker_registered_again", worker_id=worker_id)
                await register()
        except Exception:  # noqa: BLE001 - the registry heartbeat must not kill the worker
            _log.exception("worker_heartbeat_failed")
        if node_beat is not None:
            try:
                await node_beat()
            except Exception:  # noqa: BLE001
                _log.exception("cluster_node_beat_failed")
        with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
            await asyncio.wait_for(stop.wait(), timeout=10.0)


# --- helpers -----------------------------------------------------------------


_PG_PLACEHOLDER_RE = re.compile(r"\$(\d+)")


def _rewrite_for_sqlite(sql: str, args: tuple[Any, ...]) -> tuple[str, tuple[Any, ...]]:
    """Convert ``$N`` placeholders to ``?`` and re-order args.

    Postgres allows the same placeholder to repeat (``$1`` used twice)
    whereas SQLite needs one ``?`` per slot. The rewrite duplicates
    args as needed so callers can stay dialect-agnostic.
    """

    if not _PG_PLACEHOLDER_RE.search(sql):
        return sql, args  # already uses ``?`` placeholders (the SQLite twins of jobs_state)

    new_args: list[Any] = []

    def _sub(match: re.Match[str]) -> str:
        idx = int(match.group(1)) - 1
        new_args.append(args[idx])
        return "?"

    new_sql = _PG_PLACEHOLDER_RE.sub(_sub, sql)
    return new_sql, tuple(new_args)


def _sqlite_path_from_url(url: str) -> str:
    if url.startswith("sqlite:///"):
        return url[len("sqlite:///") :] or ":memory:"
    if url.startswith("sqlite://"):
        return url[len("sqlite://") :] or ":memory:"
    if url.startswith("file:"):
        return url
    if url == ":memory:":
        return url
    return url


def _import_asyncpg() -> Any:
    try:
        import asyncpg  # type: ignore[import-untyped]  # noqa: PLC0415

        return asyncpg
    except ImportError as exc:  # pragma: no cover — covered by pyproject
        raise RuntimeError(
            "asyncpg is required to connect to Postgres; install the "
            "pipeline package or `pip install asyncpg`"
        ) from exc


def _import_aiosqlite() -> Any:
    try:
        import aiosqlite  # noqa: PLC0415

        return aiosqlite
    except ImportError as exc:  # pragma: no cover — covered by pyproject
        raise RuntimeError(
            "aiosqlite is required to connect to SQLite; install the "
            "pipeline package or `pip install aiosqlite`"
        ) from exc


def env_or_default(name: str, default: str) -> str:
    """Read ``name`` from the environment or fall back to ``default``."""

    return os.environ.get(name, default)
