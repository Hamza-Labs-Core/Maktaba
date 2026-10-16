"""A toy stage graph to exercise the engine independently of the real stages."""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.context import StageContext
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.graph import Edge, StageDef, StageResult
from maktaba_pipeline.engine.stages.registry import StageImpl, register_impl, reset_registry
from maktaba_pipeline.engine.sync import sync_graph

Handler = Callable[[Any, Job, StageContext], Awaitable[StageResult]]


@dataclass
class Recorder:
    calls: list[tuple[str, str]] = field(default_factory=list)
    outputs: dict[str, str] = field(default_factory=dict)
    inputs: dict[str, str | None] = field(default_factory=dict)
    hooks: dict[str, Handler] = field(default_factory=dict)

    def count(self, stage: str) -> int:
        return sum(1 for s, _ in self.calls if s == stage)


def toy_graph() -> tuple[StageDef, ...]:
    return (
        StageDef(
            "t_a",
            "A",
            ("video",),
            "cpu",
            "episodes",
            1,
            1,
            edges=(Edge("t_b", "library_of"),),
        ),  # fmt: skip
        StageDef(
            "t_b",
            "B",
            ("library",),
            "cpu",
            "seasons",
            1,
            1,
            edges=(Edge("t_c", "catalog"),),
            may_request=("t_a",),
        ),  # fmt: skip
        StageDef("t_c", "C", ("catalog",), "cpu", "channels", 1, 1),
    )


def install(rec: Recorder, graph: tuple[StageDef, ...]) -> None:
    """Register recording handlers for every toy stage."""
    reset_registry()
    for s in graph:
        name = s.name

        async def run(db: Any, job: Job, ctx: StageContext, name: str = name) -> StageResult:
            rec.calls.append((name, str(job.subject.id)))
            hook = rec.hooks.get(name)
            if hook is not None:
                return await hook(db, job, ctx)
            return StageResult(output_hash=rec.outputs.get(name, "out-1"))

        async def input_fp(db: Any, subject: Any, name: str = name) -> str | None:
            return rec.inputs.get(name)

        register_impl(name, StageImpl(run=run, input_fingerprint=input_fp))


async def setup(db: Any, rec: Recorder, graph: tuple[StageDef, ...] | None = None) -> EngineRuntime:
    graph = graph or toy_graph()
    install(rec, graph)
    await sync_graph(db, graph)
    return EngineRuntime(graph={s.name: s for s in graph})


async def drain(
    db: Any, rt: EngineRuntime, *, worker: str = "w1", limit: int = 200, max_jobs: int | None = None
) -> int:
    """Claim and run until nothing is eligible; returns the number of jobs run."""
    n = 0
    stages = list(rt.graph)
    while n < limit:
        if max_jobs is not None and n >= max_jobs:
            return n
        job = await claim_for_pool(
            db, worker_id=worker, pool="cpu", stages=stages, scope="per_worker"
        )
        if job is None:
            return n
        await dispatch_job(db, job, runtime=rt, legacy_handlers={})
        n += 1
    raise AssertionError("drain did not quiesce")


async def rows(db: Any, stage: str | None = None) -> list[Any]:
    if stage is None:
        return list(await db.fetch("SELECT * FROM processing_jobs ORDER BY id"))
    return list(await db.fetch("SELECT * FROM processing_jobs WHERE stage = $1 ORDER BY id", stage))
