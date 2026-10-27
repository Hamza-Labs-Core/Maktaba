"""``storage_servers`` rows and this machine's resolver built from them (story 31.3)."""

from __future__ import annotations

import json
import platform
import uuid
from collections.abc import Mapping, Sequence
from typing import Any

from ..db import schema_compat
from ..engine.paths import PathMap
from ..engine.sql import is_pg, loads
from ..log import get_logger
from .canonical import parse_char_map
from .mounts import Mount, discover
from .resolver import Resolver, StorageServer, _resolve_ips, build_resolver

__all__ = [
    "build_for_machine",
    "load_servers",
    "save_server",
]

_log = get_logger()


def _addresses(raw: Any) -> tuple[str, ...]:
    v = loads(raw) if isinstance(raw, str) else raw
    return tuple(str(a) for a in (v or []) if str(a).strip())


def _exports(raw: Any) -> tuple[tuple[str, str], ...]:
    v = loads(raw) if isinstance(raw, str) else raw
    return tuple((str(k), str(s)) for k, s in (v or {}).items()) if isinstance(v, dict) else ()


async def load_servers(db: Any) -> list[StorageServer]:
    """Every server; an empty list on a database without the table (an older home)."""
    try:
        rows = await db.fetch(
            "SELECT id, name, addresses, char_map, exports FROM storage_servers ORDER BY name"
        )
    except Exception as exc:  # noqa: BLE001 - no table yet: nothing is canonical
        schema_compat.fallback(_log, "storage_servers_unavailable", exc)
        return []
    return [
        StorageServer(
            id=str(r["id"]),
            name=str(r["name"]),
            addresses=_addresses(r["addresses"]),
            char_map=str(r["char_map"] or "none"),
            exports=_exports(r["exports"]),
        )
        for r in rows
    ]


async def save_server(db: Any, server: StorageServer) -> StorageServer:
    """Insert or update by name (lower case); addresses are merged, not replaced."""
    name = server.name.strip().lower()
    if not name or "/" in name:
        raise ValueError(f"bad server name {server.name!r}")
    parse_char_map(server.char_map)  # refuses a malformed table
    existing = {s.name: s for s in await load_servers(db)}.get(name)
    addrs = list(existing.addresses) if existing else []
    for a in server.addresses:
        if a.lower() not in {x.lower() for x in addrs}:
            addrs.append(a)
    exports = dict(existing.exports) if existing else {}
    exports.update(dict(server.exports))
    char_map = (
        server.char_map if server.char_map != "none" or existing is None else existing.char_map
    )
    jaddr, jexp = json.dumps(addrs), json.dumps(exports)
    cast = "::jsonb" if is_pg(db) else ""
    if existing is None:
        sid = str(uuid.uuid4())
        await db.execute(
            "INSERT INTO storage_servers (id, name, addresses, char_map, exports)"
            f" VALUES ($1, $2, $3{cast}, $4, $5{cast})",
            sid,
            name,
            jaddr,
            char_map,
            jexp,
        )
    else:
        sid = str(existing.id)
        await db.execute(
            f"UPDATE storage_servers SET addresses = $2{cast}, char_map = $3, exports = $4{cast},"
            " updated_at = CURRENT_TIMESTAMP WHERE name = $1",
            name,
            jaddr,
            char_map,
            jexp,
        )
    return StorageServer(
        id=sid, name=name, addresses=tuple(addrs), char_map=char_map, exports=tuple(exports.items())
    )


def build_for_machine(
    servers: Sequence[StorageServer],
    *,
    explicit: PathMap | None = None,
    admin: Mapping[str, str] | None = None,
    legacy: PathMap | Mapping[str, str] | None = None,
    mounts: Sequence[Mount] | None = None,
    system: str | None = None,
) -> Resolver:
    """This machine's resolver: its live mounts (unless given), the servers, the maps."""
    system = system or platform.system()
    found = list(discover(system) if mounts is None else mounts)
    return build_resolver(
        servers=servers,
        mounts=found,
        system=system,
        explicit=explicit,
        admin=admin,
        legacy=legacy,
        resolve=_resolve_ips if servers else None,
    )
