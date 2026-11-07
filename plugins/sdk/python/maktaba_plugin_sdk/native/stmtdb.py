"""The database a first-party plugin's code sees in the runner (Epic 54, story 54.24).

The moved stage code keeps the pipeline's database interface (``fetch``, ``fetchrow``,
``fetchval``, ``execute``, ``executemany``, ``transaction()``, ``dialect``), so it runs
unchanged in-process and in the runner. Here every call looks its text up in the plugin's
registered statements and sends the plugin API only the statement's **name** and its
arguments (:mod:`.codec`); the API runs the text it registered from the release, under the
plugin's own Postgres role. A text the plugin did not register never leaves the process:
:class:`StatementUnregistered` is raised, and the worker runs that one job in-process
instead (the parity job fails on any such fallback).

``transaction()`` opens a transaction in the API that the following statements of the
same task join (``tx``); a nested one is a savepoint, as asyncpg's is.
"""

from __future__ import annotations

import contextlib
import contextvars
from collections.abc import AsyncIterator, Awaitable, Callable, Sequence
from typing import Any, Protocol

from ..statements import Registry, Statement, digest
from .codec import decode, encode
from .records import Record

__all__ = [
    "EngineRelay",
    "PluginRefused",
    "StatementDB",
    "StatementUnregistered",
    "Transport",
]


class PluginRefused(Exception):
    """The plugin API refused a call: its HTTP status and code (``plugin_scope``, …)."""

    def __init__(self, status: int, code: str, detail: str = "") -> None:
        super().__init__(f"{status} {code}: {detail}" if detail else f"{status} {code}")
        self.status = status
        self.code = code
        self.detail = detail


class StatementUnregistered(Exception):
    """The code ran a statement the plugin does not have in its ``queries/``."""

    def __init__(self, plugin: str, text: str) -> None:
        super().__init__(f"{plugin}: unregistered statement {digest(text)[:12]}: {text[:200]}")
        self.plugin = plugin
        self.text = text
        self.sha256 = digest(text)


class Transport(Protocol):
    async def call(self, fn: str, body: dict[str, Any]) -> dict[str, Any]: ...


class EngineRelay(Protocol):
    """What the moved code asks of the queue engine (the job's log and events), which only
    the worker writes: the runner relays it on the job's dispatch connection."""

    async def log_event(self, **kw: Any) -> None: ...

    async def publish(self, type_: str, payload: dict[str, Any]) -> None: ...

    async def job_ref(self, job_id: int) -> dict[str, Any] | None: ...


_TX: contextvars.ContextVar[tuple[str, int] | None] = contextvars.ContextVar(
    "maktaba_plugin_tx", default=None
)


class StatementDB:
    dialect = "postgres"
    #: db/jobs_state never calls into the engine's hooks from a plugin
    engine_hooks = False

    def __init__(
        self,
        registry: Registry,
        transport: Transport,
        *,
        engine_relay: EngineRelay | None = None,
        on_call: Callable[[Statement], None] | None = None,
    ) -> None:
        self.registry = registry
        self.transport = transport
        self.engine_relay = engine_relay
        self._on_call = on_call
        self.calls = 0

    def _statement(self, sql: str) -> Statement:
        st = self.registry.lookup(sql)
        if st is None:
            raise StatementUnregistered(self.registry.plugin, sql)
        if self._on_call is not None:
            self._on_call(st)
        return st

    async def _run(
        self, op: str, sql: str, args: Sequence[Any] = (), **extra: Any
    ) -> dict[str, Any]:
        st = self._statement(sql)
        tx = _TX.get()
        body: dict[str, Any] = {"name": st.name, "op": op, "args": [encode(a) for a in args]}
        if tx is not None:
            body["tx"] = tx[0]
        body.update(extra)
        self.calls += 1
        return await self.transport.call("trusted/stmt", body)

    @staticmethod
    def _rows(out: dict[str, Any]) -> list[Record]:
        cols = list(out.get("columns") or [])
        index = Record.index_of(cols)
        return [Record(cols, [decode(v) for v in r], index) for r in out.get("rows") or []]

    async def fetch(self, sql: str, *args: Any) -> list[Any]:
        return list(self._rows(await self._run("fetch", sql, args)))

    async def fetchrow(self, sql: str, *args: Any) -> Any:
        rows = self._rows(await self._run("fetch", sql, args, limit=1))
        return rows[0] if rows else None

    async def fetchval(self, sql: str, *args: Any) -> Any:
        row = await self.fetchrow(sql, *args)
        return None if row is None else row[0]

    async def execute(self, sql: str, *args: Any) -> Any:
        out = await self._run("execute", sql, args)
        return out.get("status")

    async def executemany(self, sql: str, rows: Sequence[Sequence[Any]]) -> None:
        if not rows:
            return
        await self._run("executemany", sql, (), rows=[[encode(a) for a in r] for r in rows])

    @contextlib.asynccontextmanager
    async def transaction(self) -> AsyncIterator[StatementDB]:
        outer = _TX.get()
        if outer is not None:
            tx, depth = outer
            sp = f"sp{depth + 1}"
            await self.transport.call("trusted/tx", {"op": "savepoint", "tx": tx, "sp": sp})
            token = _TX.set((tx, depth + 1))
            try:
                yield self
            except BaseException:
                with contextlib.suppress(Exception):
                    await self.transport.call(
                        "trusted/tx", {"op": "rollback_to", "tx": tx, "sp": sp}
                    )
                raise
            else:
                await self.transport.call("trusted/tx", {"op": "release", "tx": tx, "sp": sp})
            finally:
                _TX.reset(token)
            return
        out = await self.transport.call("trusted/tx", {"op": "begin"})
        tx = str(out["tx"])
        token = _TX.set((tx, 0))
        try:
            yield self
        except BaseException:
            with contextlib.suppress(Exception):
                await self.transport.call("trusted/tx", {"op": "rollback", "tx": tx})
            raise
        else:
            await self.transport.call("trusted/tx", {"op": "commit", "tx": tx})
        finally:
            _TX.reset(token)


async def relay_or(db: Any, name: str, fallback: Callable[[], Awaitable[Any]], **kw: Any) -> Any:
    """``db``'s engine relay for ``name`` when it is a plugin's database, else ``fallback``."""
    relay = getattr(db, "engine_relay", None)
    if relay is None:
        return await fallback()
    return await getattr(relay, name)(**kw)
