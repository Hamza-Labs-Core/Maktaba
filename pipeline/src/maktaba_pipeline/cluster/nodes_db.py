"""A node's own ``cluster_nodes`` row (design sections 3.3 and 7).

On Postgres a node's database role has no direct access to the cluster tables; it goes
through ``SECURITY DEFINER`` functions that only allow its own row
(``cluster_node_beat``, ``cluster_node_report``, ``cluster_node_stage_switches``). On
SQLite (tests, a single machine) the same operations are plain SQL. Callers use these
three helpers and never touch the tables.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from ..engine.sql import dumps, is_pg, loads, now, to_ts

__all__ = ["NodeBeat", "beat", "report", "stage_switches"]


@dataclass(frozen=True, slots=True)
class NodeBeat:
    state: str
    drain: bool
    path_map: dict[str, str] | None
    path_map_source: str | None


def _json_arg(db: Any, value: Any) -> Any:
    return None if value is None else dumps(value)


async def beat(db: Any, node_id: str) -> NodeBeat | None:
    """Mark the node seen; its state, drain switch and stored path map. None: no row."""
    if is_pg(db):
        try:
            row = await db.fetchrow("SELECT * FROM cluster_node_beat($1::uuid)", node_id)
        except Exception as exc:
            # the guard refuses a row that is not this login's: removed on the home (or
            # never ours), the same answer as no row
            if type(exc).__name__ == "InsufficientPrivilegeError":
                return None
            raise
    else:
        row = await db.fetchrow(
            "UPDATE cluster_nodes SET last_seen_at = $2 WHERE id = $1"
            " RETURNING state, drain, path_map, path_map_source",
            node_id,
            to_ts(db, now()),
        )
    if row is None:
        return None
    pm = loads(row["path_map"])
    return NodeBeat(
        state=str(row["state"]),
        drain=bool(row["drain"]),
        path_map={str(k): str(v) for k, v in pm.items()} if isinstance(pm, dict) else None,
        path_map_source=row["path_map_source"],
    )


async def report(
    db: Any,
    node_id: str,
    *,
    capabilities: dict[str, Any] | None = None,
    storage: dict[str, Any] | None = None,
    inferred_path_map: dict[str, str] | None = None,
) -> None:
    """Store what the node found; an admin-set path map is never replaced."""
    caps, stor, pm = (
        _json_arg(db, capabilities),
        _json_arg(db, storage),
        _json_arg(db, inferred_path_map),
    )
    if is_pg(db):
        await db.execute(
            "SELECT cluster_node_report($1::uuid, $2::jsonb, $3::jsonb, $4::jsonb)",
            node_id,
            caps,
            stor,
            pm,
        )
        return
    await db.execute(
        "UPDATE cluster_nodes SET"
        " capabilities = COALESCE($2, capabilities),"
        " storage = COALESCE($3, storage),"
        " path_map = CASE WHEN $4 IS NOT NULL AND COALESCE(path_map_source, '') <> 'admin'"
        "   THEN $4 ELSE path_map END,"
        " path_map_source = CASE WHEN $4 IS NOT NULL AND COALESCE(path_map_source, '') <> 'admin'"
        "   THEN 'inferred' ELSE path_map_source END,"
        " updated_at = $5"
        " WHERE id = $1",
        node_id,
        caps,
        stor,
        pm,
        to_ts(db, now()),
    )


async def stage_switches(db: Any, node_id: str) -> dict[str, bool]:
    """The admin's per-stage switches for this node (absent: automatic)."""
    if is_pg(db):
        rows = await db.fetch("SELECT * FROM cluster_node_stage_switches($1::uuid)", node_id)
    else:
        rows = await db.fetch(
            "SELECT stage, enabled FROM cluster_node_stages WHERE node_id = $1", node_id
        )
    return {str(r["stage"]): bool(r["enabled"]) for r in rows}
