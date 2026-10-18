"""Sync the in-code stage graph into ``job_stages`` (plan 06-11 section 4.3).

Run by every worker at start. It owns the *structural* columns (label, pool,
edges, version, ...) and never touches the operator columns (``paused``,
``write_mode``, ``paused_by``, ...). ``enabled`` / ``disabled_reason`` are
shared: the worker may flip them only while ``disabled_reason`` is NULL,
``not_configured`` or ``extension_disabled``; an ``operator`` disable always wins.
"""

from __future__ import annotations

import json
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from typing import Any

from ..extensions.store import ExtensionState
from .graph import POOLS, PoolDef, StageDef
from .sql import dumps, is_pg, now, to_ts

__all__ = ["SyncReport", "stage_enabled_state", "stages_ahead", "sync_graph"]

#: reasons the worker itself may set or clear
WORKER_REASONS = ("not_configured", "extension_disabled")


@dataclass(frozen=True, slots=True)
class SyncReport:
    #: stages whose ``version`` changed: ``name -> (old, new)``
    version_changes: Mapping[str, tuple[int, int]]
    #: stages newly inserted
    added: tuple[str, ...]


def stage_enabled_state(
    stage: StageDef, extensions: Mapping[str, ExtensionState]
) -> tuple[bool, str | None]:
    """``(enabled, disabled_reason)`` the graph wants for ``stage``."""
    if stage.extension is None:
        return True, None
    state = extensions.get(stage.extension)
    if state is None or not state.enabled:
        return False, "extension_disabled"
    if not state.configured:
        return False, "not_configured"
    return True, None


def _arr(db: Any, items: Sequence[str]) -> Any:
    return list(items) if is_pg(db) else json.dumps(list(items))


async def _ensure_pools(db: Any, pools: Sequence[PoolDef]) -> None:
    for p in pools:
        await db.execute(
            "INSERT INTO worker_pools (pool, label, scope, size, default_size)"
            " VALUES ($1,$2,$3,$4,$5) ON CONFLICT (pool) DO NOTHING",
            p.pool,
            p.label,
            p.scope,
            p.size,
            p.default_size,
        )


_UPSERT = """
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, extension, enabled, disabled_reason, graph_synced_at, updated_at,
     placement)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$20,$21)
ON CONFLICT (stage) DO UPDATE SET
    label = EXCLUDED.label, subject_kinds = EXCLUDED.subject_kinds, pool = EXCLUDED.pool,
    lane = EXCLUDED.lane, lane_order = EXCLUDED.lane_order, version = EXCLUDED.version,
    debounce_sec = EXCLUDED.debounce_sec, debounce_max_sec = EXCLUDED.debounce_max_sec,
    max_attempts = EXCLUDED.max_attempts, edges = EXCLUDED.edges,
    may_request = EXCLUDED.may_request, entry_for = EXCLUDED.entry_for,
    legacy = EXCLUDED.legacy, hidden = EXCLUDED.hidden,
    external_write = EXCLUDED.external_write, extension = EXCLUDED.extension,
    placement = EXCLUDED.placement,
    enabled = CASE WHEN job_stages.disabled_reason IS NULL
                     OR job_stages.disabled_reason IN
                        ('not_configured', 'extension_disabled')
                   THEN EXCLUDED.enabled ELSE job_stages.enabled END,
    disabled_reason = CASE WHEN job_stages.disabled_reason IS NULL
                             OR job_stages.disabled_reason IN
                                ('not_configured', 'extension_disabled')
                           THEN EXCLUDED.disabled_reason ELSE job_stages.disabled_reason END,
    graph_synced_at = EXCLUDED.graph_synced_at,
    updated_at = EXCLUDED.updated_at
"""


async def sync_graph(
    db: Any,
    graph: Sequence[StageDef],
    extensions: Mapping[str, ExtensionState] | None = None,
) -> SyncReport:
    """Upsert every stage of ``graph``; return what changed."""
    extensions = extensions or {}
    await _ensure_pools(db, POOLS)
    old = {
        r["stage"]: int(r["version"])
        for r in await db.fetch("SELECT stage, version FROM job_stages")
    }
    ts = to_ts(db, now())
    versions: dict[str, tuple[int, int]] = {}
    added: list[str] = []
    for s in graph:
        enabled, reason = stage_enabled_state(s, extensions)
        await db.execute(
            _UPSERT,
            s.name,
            s.label,
            _arr(db, s.subject_kinds),
            s.pool,
            s.lane,
            s.lane_order,
            s.version,
            s.debounce_sec,
            s.debounce_max_sec,
            s.max_attempts,
            dumps(s.edges_json()),
            _arr(db, s.may_request),
            _arr(db, s.entry_for),
            s.legacy,
            s.hidden,
            s.external_write,
            s.extension,
            enabled,
            reason,
            ts,
            s.placement,
        )
        if s.name not in old:
            added.append(s.name)
        elif old[s.name] != s.version:
            versions[s.name] = (old[s.name], s.version)
    return SyncReport(versions, tuple(added))


async def stages_ahead(db: Any, graph: Sequence[StageDef]) -> dict[str, int]:
    """Stages whose database ``version`` is newer than this worker's (``graph_version_ahead``).

    A worker must not claim these: a newer worker has already moved the stage on.
    """
    mine = {s.name: s.version for s in graph}
    rows = await db.fetch("SELECT stage, version FROM job_stages")
    return {
        r["stage"]: int(r["version"])
        for r in rows
        if r["stage"] in mine and int(r["version"]) > mine[r["stage"]]
    }
