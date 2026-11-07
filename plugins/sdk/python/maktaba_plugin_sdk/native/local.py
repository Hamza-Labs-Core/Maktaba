"""A plugin API in the test's own process: the SDK harness's fake API (story 54.12, 54.27 §4).

:class:`LocalTransport` answers ``trusted/stmt`` and ``trusted/tx`` the way the Go API does,
on a database the test holds (the pipeline's ``Database``, SQLite or Postgres): only the
plugin's registered statements, by name, with the same values on the wire
(:mod:`.codec`). A plugin's contract tests run its handlers through it, so a statement
the code runs but the plugin did not register fails there, before any runner or API.

It checks no role and no scope: those are the Go API's (its own tests prove them), and the
parity proof runs the real API.
"""

from __future__ import annotations

import contextlib
import itertools
from typing import Any

from ..statements import Registry
from .codec import decode, encode
from .stmtdb import PluginRefused

__all__ = ["LocalTransport"]


def _value(v: Any) -> Any:
    """A column as the Go API answers it (asyncpg's types, encoded)."""
    if isinstance(v, list):
        return [_value(x) for x in v]
    if isinstance(v, dict):
        import json

        return json.dumps(v)  # jsonb as text, as asyncpg answers it by default
    return encode(v)


class LocalTransport:
    def __init__(self, registry: Registry, db: Any) -> None:
        self.registry = registry
        self.db = db
        self.calls: list[str] = []
        self._ids = itertools.count(1)
        self._txs: dict[str, contextlib.AbstractAsyncContextManager[Any]] = {}
        self._sps: dict[str, list[contextlib.AbstractAsyncContextManager[Any]]] = {}

    async def call(self, fn: str, body: dict[str, Any]) -> dict[str, Any]:
        if fn == "trusted/tx":
            return await self._tx(body)
        if fn != "trusted/stmt":
            raise PluginRefused(404, "not_found", fn)
        st = self.registry.by_name.get(str(body.get("name")))
        if st is None:
            raise PluginRefused(403, "plugin_capability", f"no statement {body.get('name')}")
        self.calls.append(st.name)
        op = body.get("op")
        args = [decode(a) for a in body.get("args") or []]
        if op == "fetch":
            rows = await self.db.fetch(st.text, *args)
            if body.get("limit"):
                rows = rows[: int(body["limit"])]
            cols = list(rows[0].keys()) if rows else []
            return {"columns": cols, "rows": [[_value(r[c]) for c in cols] for r in rows]}
        if op == "execute":
            status = await self.db.execute(st.text, *args)
            return {"status": status if isinstance(status, str) else ""}
        if op == "executemany":
            await self.db.executemany(
                st.text, [[decode(a) for a in r] for r in body.get("rows") or []]
            )
            return {}
        raise PluginRefused(400, "plugin_invalid", f"op {op}")

    async def _tx(self, body: dict[str, Any]) -> dict[str, Any]:
        op = body.get("op")
        if op == "begin":
            tx = f"tx_{next(self._ids)}"
            cm = self.db.transaction()
            await cm.__aenter__()
            self._txs[tx] = cm
            self._sps[tx] = []
            return {"tx": tx}
        tx = str(body.get("tx"))
        if tx not in self._txs:
            raise PluginRefused(409, "plugin_tx", f"no open transaction {tx}")
        if op == "savepoint":
            cm = self.db.transaction()
            await cm.__aenter__()
            self._sps[tx].append(cm)
        elif op == "release":
            await self._sps[tx].pop().__aexit__(None, None, None)
        elif op == "rollback_to":
            sp = self._sps[tx].pop()
            with contextlib.suppress(RuntimeError):
                await sp.__aexit__(RuntimeError, RuntimeError("rollback"), None)
        elif op in ("commit", "rollback"):
            cm = self._txs.pop(tx)
            self._sps.pop(tx, None)
            if op == "commit":
                await cm.__aexit__(None, None, None)
            else:
                with contextlib.suppress(RuntimeError):
                    await cm.__aexit__(RuntimeError, RuntimeError("rollback"), None)
        return {"tx": tx}

    async def close(self) -> None:
        return None
