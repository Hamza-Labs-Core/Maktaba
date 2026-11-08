"""Test harness for the extension and stage tests: runtime setup, a one-job runner, fakes."""

from __future__ import annotations

import json
import uuid
from collections.abc import Mapping, Sequence
from dataclasses import dataclass, field
from typing import Any
from uuid import UUID

from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, StageDef
from maktaba_pipeline.engine.loader import build_full_graph, register_extension_impls
from maktaba_pipeline.engine.sql import dumps, loads
from maktaba_pipeline.engine.stages.registry import reset_registry
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.extensions.base import Extension
from maktaba_pipeline.extensions.store import ExtensionState
from maktaba_pipeline.integrations.plex import TransportError


async def setup_runtime(
    db: Any,
    extensions: Sequence[Extension],
    settings: Mapping[str, Mapping[str, str]] | None = None,
    *,
    enabled: bool = True,
) -> EngineRuntime:
    """Build the full graph, register implementations, sync ``job_stages``."""
    settings = settings or {}
    reset_registry()
    graph: tuple[StageDef, ...] = build_full_graph(extensions) if extensions else GRAPH
    register_extension_impls(extensions)
    states = {
        e.manifest.name: ExtensionState(
            e.manifest.name,
            enabled,
            dict(settings.get(e.manifest.name, {})),
            e.is_configured(settings.get(e.manifest.name, {})),
        )
        for e in extensions
    }
    await sync_graph(db, graph, states)
    return EngineRuntime(graph={s.name: s for s in graph}, extension_states=states)


async def run_stage(
    db: Any,
    rt: EngineRuntime,
    stage: str,
    kind: str,
    subject_id: UUID,
    *,
    force: bool = False,
    payload: dict[str, Any] | None = None,
) -> Any:
    """Enqueue one job, claim it and dispatch it; returns the finished ``processing_jobs`` row."""
    res = await enqueue_job(
        db, subject_kind=kind, subject_id=subject_id, stage=stage, force=force, payload=payload
    )
    assert res.job_id is not None, res.outcome
    await db.execute("UPDATE processing_jobs SET not_before = NULL WHERE id = $1", res.job_id)
    job = await claim_for_pool(db, worker_id="w1", pool=rt.graph[stage].pool, stages=[stage])
    assert job is not None and job.id == res.job_id
    await dispatch_job(db, job, runtime=rt, legacy_handlers={})
    return await db.fetchrow("SELECT * FROM processing_jobs WHERE id = $1", res.job_id)


def error_of(row: Any) -> dict[str, Any]:
    return dict(loads(row["error"]) or {})


def summary_of(row: Any) -> dict[str, Any]:
    return dict(loads(row["output_summary"]) or {})


async def events(db: Any, job_id: int, kind: str) -> list[dict[str, Any]]:
    rows = await db.fetch(
        "SELECT data FROM job_events WHERE job_id = $1 AND kind = $2 ORDER BY id", job_id, kind
    )
    return [dict(loads(r["data"]) or {}) for r in rows]


async def all_event_text(db: Any) -> str:
    rows = await db.fetch("SELECT message, data FROM job_events")
    summaries = await db.fetch("SELECT output_summary, error FROM processing_jobs")
    return json.dumps(
        [[str(r["message"]), str(r["data"])] for r in rows]
        + [[str(r["output_summary"]), str(r["error"])] for r in summaries]
    )


async def add_episode(
    db: Any, series_id: UUID, video_id: UUID, *, season: int = 1, episode: int | None = None
) -> None:
    await db.execute(
        "INSERT INTO series_episodes (series_id, video_id, season, episode) VALUES ($1,$2,$3,$4)",
        str(series_id),
        str(video_id),
        season,
        episode,
    )


def new_id() -> UUID:
    return uuid.uuid4()


# ------------------------------------------------------------------ fake Plex


@dataclass
class FakePlex:
    """A Plex Media Server's identity endpoint; implements the client's ``Transport`` (the
    health probe and the relay tests ping it; Live TV has its own fake)."""

    base: str = "http://plex.test"
    calls: list[tuple[str, str]] = field(default_factory=list)
    token_seen: set[str] = field(default_factory=set)
    down: bool = False

    def request(
        self,
        method: str,
        url: str,
        headers: Mapping[str, str],
        body: bytes | None,
        timeout: float,
    ) -> tuple[int, bytes]:
        if self.down:
            raise TransportError("connection refused")
        if token := headers.get("X-Plex-Token"):
            self.token_seen.add(token)
        assert url.startswith(self.base), url
        path = url[len(self.base) :].partition("?")[0]
        self.calls.append((method, path))
        if path == "/identity":
            out = {"MediaContainer": {"machineIdentifier": "machine-1"}}
            return 200, json.dumps(out).encode()
        return 404, b""


__all__ = [
    "FakePlex",
    "add_episode",
    "all_event_text",
    "dumps",
    "error_of",
    "events",
    "new_id",
    "run_stage",
    "setup_runtime",
    "summary_of",
]
