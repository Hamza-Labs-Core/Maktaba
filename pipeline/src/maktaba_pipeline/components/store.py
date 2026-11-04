"""What should run and what does (Epic 51, slot 0178): the pipeline's side of the
component tables, the twin of ``shared/components/store`` in Go.

The API alone writes desired state (``components``, ``component_overrides``). The
pipeline reads it and reports its own status rows (``component_status``, part
``pipeline``). On Postgres a joined machine's login has no direct writes there: it
reads through ``component_desired_for`` and reports through ``component_status_report``,
both guarded to its own node. The home and SQLite (one machine) use plain SQL.

Changes are signalled on :data:`CHANNEL` with ``{"revision", "id", "node"}``; a runner
also polls :func:`revision` every :data:`POLL_S` seconds and reloads when it moved.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any

from ..engine.sql import dumps, is_pg, loads, parse_ts
from .catalog import Catalog, default

__all__ = [
    "CHANNEL",
    "HOME",
    "MAX_DETAIL_BYTES",
    "POLL_S",
    "Condition",
    "Desired",
    "Event",
    "Hold",
    "Status",
    "StatusError",
    "desired",
    "parse_notify",
    "report_status",
    "revision",
    "rollup",
    "statuses",
]

HOME = "home"
CHANNEL = "components_changed"
MAX_DETAIL_BYTES = 4096
POLL_S = 30.0

# worst first: the phase a component shows for a machine is the worst of its parts'
# (the same order as store/rollup.go)
_ROLLUP_ORDER = (
    "failed",
    "waiting",
    "removing",
    "installing",
    "draining",
    "starting",
    "degraded",
    "stopped",
    "running",
    "off",
    "not_installed",
)
_RANK = {p: i for i, p in enumerate(_ROLLUP_ORDER)}


class StatusError(ValueError):
    """A status report the catalog does not allow (unknown phase or reason, bad shape)."""


@dataclass(frozen=True, slots=True)
class Hold:
    by: str
    at: datetime


@dataclass(frozen=True, slots=True)
class Desired:
    id: str
    want: str
    generation: int
    hold: Hold | None = None


@dataclass(frozen=True, slots=True)
class Condition:
    type: str
    status: str  # true, false or unknown
    reason: str = ""
    params: dict[str, Any] = field(default_factory=dict)

    def wire(self) -> dict[str, Any]:
        out: dict[str, Any] = {"type": self.type, "status": self.status}
        if self.reason:
            out["reason"] = self.reason
        if self.params:
            out["params"] = self.params
        return out


@dataclass(frozen=True, slots=True)
class Status:
    id: str
    phase: str
    conditions: tuple[Condition, ...] = ()
    observed_generation: int = 0
    version: str = ""
    detail: dict[str, Any] = field(default_factory=dict)
    machine: str = ""
    part: str = ""
    since: datetime | None = None
    updated_at: datetime | None = None

    @property
    def reason(self) -> str:
        """The first reason of a condition that is not true ("" when all are)."""
        for c in self.conditions:
            if c.status != "true" and c.reason:
                return c.reason
        return ""


@dataclass(frozen=True, slots=True)
class Event:
    revision: int = 0
    id: str = ""
    node: str = ""
    reload: bool = False


def parse_notify(payload: str) -> Event:
    """A components_changed payload; one that cannot be read is a reload."""
    try:
        m = json.loads(payload)
        return Event(
            revision=int(m["revision"]), id=str(m.get("id") or ""), node=str(m.get("node") or "")
        )
    except (ValueError, TypeError, KeyError):
        return Event(reload=True)


async def revision(db: Any) -> int:
    """component_meta.revision: moves on every change of desired state (one cheap query)."""
    return int(await db.fetchval("SELECT revision FROM component_meta"))


_MERGE_SQLITE = """
SELECT COALESCE(c.id, o.id) AS id, COALESCE(o.want, c.want) AS want,
       CASE WHEN o.hold_at IS NOT NULL THEN o.hold_by ELSE c.hold_by END AS hold_by,
       CASE WHEN o.hold_at IS NOT NULL THEN o.hold_at ELSE c.hold_at END AS hold_at,
       COALESCE(c.generation, 0) + COALESCE(o.generation, 0) AS generation
  FROM components c
  LEFT JOIN component_overrides o ON o.id = c.id AND o.node_id = $1
 WHERE COALESCE(o.want, c.want) IS NOT NULL
UNION ALL
SELECT o.id, o.want, o.hold_by, o.hold_at, o.generation
  FROM component_overrides o
 WHERE o.node_id = $1 AND o.want IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM components c WHERE c.id = o.id)
"""


async def desired(db: Any, machine: str = HOME) -> tuple[dict[str, Desired], int]:
    """The desired state ``machine`` (HOME or its cluster_nodes.id) sees, by id, and the
    revision it was read at (read first, so a change meanwhile reloads next time)."""
    rev = await revision(db)
    if machine in (HOME, ""):
        rows = await db.fetch("SELECT id, want, hold_by, hold_at, generation FROM components")
    elif is_pg(db):
        rows = await db.fetch(
            "SELECT id, want, hold_by, hold_at, generation FROM component_desired_for($1::uuid)",
            machine,
        )
    else:
        rows = await db.fetch(_MERGE_SQLITE, machine)
    out: dict[str, Desired] = {}
    for r in rows:
        at = parse_ts(r["hold_at"])
        hold = Hold(by=str(r["hold_by"]), at=at) if at is not None else None
        out[str(r["id"])] = Desired(
            id=str(r["id"]), want=str(r["want"]), generation=int(r["generation"]), hold=hold
        )
    return out, rev


def _wire(rows: list[Status], cat: Catalog, part: str, version: str) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    seen: set[str] = set()
    for r in rows:
        if not r.id:
            raise StatusError("a status row needs an id")
        if r.id in seen:
            raise StatusError(f"{r.id} reported twice by {part}")
        seen.add(r.id)
        if not cat.is_phase(r.phase):
            raise StatusError(f"{r.id}: unknown phase {r.phase!r}")
        for c in r.conditions:
            if c.reason and not cat.is_reason(c.reason):
                raise StatusError(
                    f"{r.id}: unknown reason {c.reason!r}"
                    " (add it to the catalog's reasons, with its words)"
                )
            if c.status not in ("true", "false", "unknown"):
                raise StatusError(
                    f"{r.id}: condition {c.type} has status {c.status!r} (true, false or unknown)"
                )
            if not c.type:
                raise StatusError(f"{r.id}: a condition needs a type")
        detail: Any = r.detail or {}
        if len(dumps(detail).encode()) > MAX_DETAIL_BYTES:
            detail = {"truncated": True}
        out.append(
            {
                "id": r.id,
                "phase": r.phase,
                "conditions": [c.wire() for c in r.conditions],
                "observed_generation": r.observed_generation,
                "version": r.version or version or None,
                "detail": detail,
            }
        )
    return out


async def report_status(
    db: Any,
    machine: str,
    part: str,
    rows: list[Status],
    *,
    version: str = "",
    catalog: Catalog | None = None,
) -> None:
    """Replace ``part``'s status rows on ``machine``: upsert each row (``since`` moves
    only when the phase does), drop that part's rows not in ``rows``. A phase or reason
    the catalog does not know raises :class:`StatusError` before anything is written.
    A detail over :data:`MAX_DETAIL_BYTES` is stored as ``{"truncated": true}``."""
    if not machine or not part:
        raise StatusError("a status report needs a machine and a part")
    wire = _wire(rows, catalog or default(), part, version)
    if machine != HOME and is_pg(db):
        await db.execute(
            "SELECT component_status_report($1::uuid, $2, $3::jsonb)", machine, part, dumps(wire)
        )
        return
    if is_pg(db):
        upsert = """INSERT INTO component_status AS s
            (machine, id, part, phase, conditions, observed_generation, version, detail)
            VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8::jsonb)
            ON CONFLICT (machine, id, part) DO UPDATE SET phase = excluded.phase,
                conditions = excluded.conditions,
                observed_generation = excluded.observed_generation,
                version = excluded.version, detail = excluded.detail,
                since = CASE WHEN s.phase IS DISTINCT FROM excluded.phase
                             THEN now() ELSE s.since END,
                updated_at = now()"""
        prune = """DELETE FROM component_status WHERE machine = $1 AND part = $2
            AND id NOT IN (SELECT jsonb_array_elements_text($3::jsonb))"""
    else:
        upsert = """INSERT INTO component_status AS s
            (machine, id, part, phase, conditions, observed_generation, version, detail)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
            ON CONFLICT (machine, id, part) DO UPDATE SET phase = excluded.phase,
                conditions = excluded.conditions,
                observed_generation = excluded.observed_generation,
                version = excluded.version, detail = excluded.detail,
                since = CASE WHEN s.phase IS NOT excluded.phase
                             THEN strftime('%Y-%m-%dT%H:%M:%fZ', 'now') ELSE s.since END,
                updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')"""
        prune = """DELETE FROM component_status WHERE machine = $1 AND part = $2
            AND id NOT IN (SELECT value FROM json_each($3))"""
    async with db.transaction():
        for w in wire:
            await db.execute(
                upsert,
                machine,
                w["id"],
                part,
                w["phase"],
                dumps(w["conditions"]),
                w["observed_generation"],
                w["version"],
                dumps(w["detail"]),
            )
        await db.execute(prune, machine, part, dumps([w["id"] for w in wire]))


def _status(r: Any) -> Status:
    conds = loads(r["conditions"]) or []
    return Status(
        machine=str(r["machine"]),
        id=str(r["id"]),
        part=str(r["part"]),
        phase=str(r["phase"]),
        conditions=tuple(
            Condition(
                type=str(c.get("type", "")),
                status=str(c.get("status", "")),
                reason=str(c.get("reason") or ""),
                params=dict(c.get("params") or {}),
            )
            for c in conds
        ),
        observed_generation=int(r["observed_generation"]),
        version=str(r["version"] or ""),
        detail=dict(loads(r["detail"]) or {}),
        since=parse_ts(r["since"]),
        updated_at=parse_ts(r["updated_at"]),
    )


async def statuses(db: Any, component: str | None = None) -> list[Status]:
    """Every status row (or ``component``'s), by machine, id and part."""
    q = (
        "SELECT machine, id, part, phase, conditions, observed_generation, version, detail,"
        " since, updated_at"
        " FROM component_status"
    )
    if component is None:
        rows = await db.fetch(q + " ORDER BY machine, id, part")
    else:
        rows = await db.fetch(q + " WHERE id = $1 ORDER BY machine, part", component)
    return [_status(r) for r in rows]


def rollup(rows: list[Status]) -> Status | None:
    """One component's status on one machine from its parts' rows: the worst phase wins,
    with that part's conditions (the first of equally bad rows). A phase this release
    does not know ranks worst. None for no rows."""
    if not rows:
        return None
    best = rows[0]
    for r in rows[1:]:
        if _RANK.get(r.phase, -1) < _RANK.get(best.phase, -1):
            best = r
    return Status(
        id=best.id,
        phase=best.phase,
        conditions=best.conditions,
        observed_generation=best.observed_generation,
        version=best.version,
        detail=best.detail,
        machine=best.machine,
        since=best.since,
        updated_at=best.updated_at,
    )
