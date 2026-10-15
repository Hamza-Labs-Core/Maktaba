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
from collections.abc import AsyncIterator, Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any

from .db.jobs import DBConn, Job, Stage
from .db.jobs_state import StageError, mark_done, mark_failed_or_retry
from .log import get_logger
from .pipeline.heartbeat import DEFAULT_HEARTBEAT_SEC, heartbeat_for
from .pipeline.reaper import Reaper
from .pipeline.runner import ClaimLoop, WorkerConfig, install_signal_handlers

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
    stages: tuple[Stage, ...]
    worker_id: str | None = None
    heartbeat_sec: float = DEFAULT_HEARTBEAT_SEC
    stale_claim_sec: float = 90.0
    reaper_interval_sec: float = 30.0
    claim_poll_sec: float = 1.0
    claim_poll_max_sec: float = 30.0
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
    overrides: dict[Stage, StageHandler] | None = None,
) -> StageHandler:
    """Return a dispatch callable that routes by ``job.stage``.

    Each built-in handler is a placeholder that logs the dispatch and
    marks the job ``done``. Real handlers plug in via ``overrides``;
    the daemon entry point passes the registered handlers in from the
    runtime config, and tests can install fakes for behavioural
    assertions.
    """

    table: dict[Stage, StageHandler] = {
        Stage.SCAN: _placeholder_handler("scan"),
        Stage.PROBE: _placeholder_handler("probe"),
        Stage.EXTRACT: _placeholder_handler("extract"),
        Stage.TRANSCRIBE: _placeholder_handler("transcribe"),
        Stage.SUBTITLE_GEN: _placeholder_handler("subtitle_gen"),
        Stage.INDEX: _placeholder_handler("index"),
        Stage.THUMBNAIL: _placeholder_handler("thumbnail"),
    }
    if overrides:
        table.update(overrides)

    async def dispatch(db: DBConn, job: Job) -> None:
        handler = table.get(job.stage)
        if handler is None:
            err = StageError(
                kind="dispatch_unknown_stage",
                message=f"no handler registered for stage {job.stage.value}",
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
    dispatch_overrides: dict[Stage, StageHandler] | None = None,
    db: Database | None = None,
) -> int:
    """Run the worker daemon until SIGTERM/SIGINT.

    Returns the process exit code. Tests can pre-build a ``db`` and
    pass an :func:`build_default_dispatch`-compatible override map; the
    CLI builds both from environment + flags before calling this.
    """

    owned_db = db is None
    database = db or await Database.connect(cfg.database_url)
    _log.info(
        "pipeline_runtime_starting",
        dialect=database.dialect,
        worker_id=cfg.worker_id,
        stages=[s.value for s in cfg.stages],
    )

    shutdown = asyncio.Event()
    install_signal_handlers(shutdown)

    semaphores = {
        stage: asyncio.Semaphore(cfg.stage_concurrency.get(stage, 1)) for stage in cfg.stages
    }

    worker_cfg = WorkerConfig(
        supported_stages=cfg.stages,
        claim_poll_sec=cfg.claim_poll_sec,
        claim_poll_max_sec=cfg.claim_poll_max_sec,
        **({"worker_id": cfg.worker_id} if cfg.worker_id else {}),
    )

    dispatch_for_db = build_default_dispatch(dispatch_overrides)

    async def _loop_dispatch(job: Job) -> None:
        await dispatch_for_db(database, job)

    claim_loop = ClaimLoop(
        database,
        worker_cfg,
        _loop_dispatch,
        semaphores=semaphores,
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
        await claim_loop.run()
    finally:
        await reaper.stop()
        if owned_db:
            await database.close()
        _log.info("pipeline_runtime_stopped")
    return 0


# --- helpers -----------------------------------------------------------------


_PG_PLACEHOLDER_RE = re.compile(r"\$(\d+)")


def _rewrite_for_sqlite(sql: str, args: tuple[Any, ...]) -> tuple[str, tuple[Any, ...]]:
    """Convert ``$N`` placeholders to ``?`` and re-order args.

    Postgres allows the same placeholder to repeat (``$1`` used twice)
    whereas SQLite needs one ``?`` per slot. The rewrite duplicates
    args as needed so callers can stay dialect-agnostic.
    """

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
