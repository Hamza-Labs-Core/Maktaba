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
from collections.abc import AsyncIterator, Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any

from . import __version__
from .channels.pass_ import run_topup_loop
from .db.jobs import DBConn, Job, Stage
from .db.jobs_state import StageError, mark_done, mark_failed_or_retry
from .engine.dispatch import EngineRuntime, dispatch_job
from .engine.graph import StageDef
from .engine.health import run_sampler
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

    if cfg.engine:
        engine_rt, stage_names, stage_pools, background = await _start_engine(
            database, cfg, worker_id, host, shutdown, dispatch_overrides
        )

    _log.info(
        "pipeline_runtime_starting",
        dialect=database.dialect,
        worker_id=worker_id,
        host=host,
        stages=list(stage_names),
        engine=cfg.engine,
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

    reaper = Reaper(
        database,
        interval_sec=cfg.reaper_interval_sec,
        stale_claim_sec=cfg.stale_claim_sec,
        heartbeat_sec=cfg.heartbeat_sec,
    )
    reaper.start()

    try:
        if stage_names:
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
    return 0


async def _start_engine(
    database: Database,
    cfg: RuntimeConfig,
    worker_id: str,
    host: str,
    shutdown: asyncio.Event,
    overrides: Mapping[Stage | str, StageHandler] | None,
) -> tuple[EngineRuntime, tuple[str, ...], dict[str, str], list[asyncio.Task[None]]]:
    """Extension discovery, graph sync, worker registration and background tasks."""
    extensions = discover()
    await ensure_rows(database, extensions)
    states = {e.manifest.name: await load_state(database, e) for e in extensions}
    graph = build_full_graph(extensions)
    register_extension_impls(extensions, states)
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
    handlers = {str(k) for k in (overrides or {})}
    served = _served_stages(cfg, graph, handlers, ahead)

    collector = runtime.nas.collector
    for ext in extensions:
        probes = getattr(ext, "health_probes", None)
        state = states.get(ext.manifest.name)
        if callable(probes) and state is not None and state.enabled and state.configured:
            collector.probes.update(probes(state.settings))

    pools = sorted({runtime.graph[s].pool for s in served})

    async def register() -> None:
        await register_worker(
            database,
            worker_id=worker_id,
            host=host,
            stages=served,
            pools=pools,
            path_map=path_map.as_json(),
            version=__version__,
        )

    await register()
    tasks = [
        asyncio.create_task(
            _worker_heartbeat(database, worker_id, shutdown, host, register), name="worker-hb"
        ),
        asyncio.create_task(run_sampler(database, collector, shutdown, host=host), name="health"),
        asyncio.create_task(run_reconciler(database, runtime.graph, shutdown), name="reconciler"),
        asyncio.create_task(run_pruner(database, shutdown), name="pruner"),
        asyncio.create_task(run_topup_loop(database, shutdown), name="channel-topup"),
    ]
    return runtime, tuple(served), {s: runtime.graph[s].pool for s in served}, tasks


def _served_stages(
    cfg: RuntimeConfig,
    graph: Sequence[StageDef],
    handlers: set[str],
    ahead: Mapping[str, int],
) -> list[str]:
    """Stages this worker claims.

    Default: every graph stage that has a handler here (legacy stages need an
    entry in the dispatch override map; engine stages need an implementation).
    ``cfg.stages`` narrows it; stages whose database version is ahead of this
    worker's graph are never claimed.
    """
    from .engine.stages.registry import get_impl

    requested = {str(s) for s in cfg.stages} if cfg.stages else None
    excluded = set(cfg.exclude_stages)
    out: list[str] = []
    for st in graph:
        if requested is not None and st.name not in requested:
            continue
        if st.name in ahead or st.name in excluded:
            continue
        if st.legacy:
            if st.name not in handlers:
                continue
        else:
            impl = get_impl(st.name)
            if impl is None or impl.run is None:
                continue
            why = impl.unavailable() if impl.unavailable is not None else None
            if why is not None:
                if requested is not None:
                    _log.warning("stage_unavailable_here", stage=st.name, reason=why)
                continue
        out.append(st.name)
    return out


async def _worker_heartbeat(
    database: Database,
    worker_id: str,
    stop: asyncio.Event,
    host: str | None = None,
    register: Callable[[], Awaitable[None]] | None = None,
) -> None:
    while not stop.is_set():
        try:
            if not await heartbeat_worker(database, worker_id, host) and register is not None:
                _log.warning("worker_registered_again", worker_id=worker_id)
                await register()
        except Exception:  # noqa: BLE001 - the registry heartbeat must not kill the worker
            _log.exception("worker_heartbeat_failed")
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
