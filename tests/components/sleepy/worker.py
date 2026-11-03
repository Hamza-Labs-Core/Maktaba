"""Run one pipeline worker whose stages only sleep (the components CI jobs).

    python tests/components/sleepy/worker.py --db URL --worker-id W --stages a,b [--sleep S]

Every stage named is registered with a handler that sleeps ``--sleep`` seconds (plus a
little jitter) and returns a new output, so each job finishes ``changed`` and its edges
fire; ``component_catchup`` keeps its real handler. An extension's stage (``plex_index``)
sleeps too: its own handler is replaced after the extensions register theirs. Then the
real worker runs (``maktaba_pipeline.runtime.run``) with its component runner, until SIGTERM.
"""

from __future__ import annotations

import argparse
import asyncio
import random
import sys
from typing import Any


def _sleepy(seconds: float) -> Any:
    from maktaba_pipeline.engine.graph import StageResult
    from maktaba_pipeline.engine.stages.registry import StageImpl

    async def run(_db: Any, job: Any, _ctx: Any) -> StageResult:
        await asyncio.sleep(seconds * random.uniform(0.8, 1.2))  # noqa: S311 - test jitter
        return StageResult(output_hash=f"sleepy:{job.stage}:{job.id}", outcome="changed")

    return StageImpl(run=run)


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--db", required=True)
    p.add_argument("--worker-id", required=True)
    p.add_argument("--stages", required=True)
    p.add_argument("--sleep", type=float, default=0.3)
    a = p.parse_args(argv)

    from maktaba_pipeline import runtime
    from maktaba_pipeline.engine.stages.registry import register_impl
    from maktaba_pipeline.runtime import RuntimeConfig, run

    stages = tuple(s for s in a.stages.split(",") if s)
    sleepy = [st for st in stages if st != "component_catchup"]
    for st in sleepy:
        register_impl(st, _sleepy(a.sleep))
    # the extensions register their own handlers at start: the sleepy ones go back on top
    real = runtime.register_extension_impls

    def register_extension_impls(*args: Any, **kwargs: Any) -> Any:
        out = real(*args, **kwargs)
        for st in sleepy:
            register_impl(st, _sleepy(a.sleep))
        return out

    runtime.register_extension_impls = register_extension_impls  # type: ignore[assignment]
    cfg = RuntimeConfig(
        database_url=a.db,
        stages=stages,
        worker_id=a.worker_id,
        host=a.worker_id,
        share=False,
        claim_poll_sec=0.2,
        claim_poll_max_sec=1.0,
        shutdown_drain_sec=5.0,
    )
    return asyncio.run(run(cfg))


if __name__ == "__main__":
    sys.exit(main())
