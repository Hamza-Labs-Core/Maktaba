"""Run one pipeline worker whose stages only sleep (the components CI jobs).

    python tests/components/sleepy/worker.py --db URL --worker-id W --stages a,b [--sleep S]

Every stage named is registered with a handler that sleeps ``--sleep`` seconds (plus a
little jitter) and returns a new output, so each job finishes ``changed`` and its edges
fire; ``component_catchup`` keeps its real handler. Then the real worker runs
(``maktaba_pipeline.runtime.run``) with its component runner, until SIGTERM.
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

    from maktaba_pipeline.engine.stages.registry import register_impl
    from maktaba_pipeline.runtime import RuntimeConfig, run

    stages = tuple(s for s in a.stages.split(",") if s)
    for st in stages:
        if st != "component_catchup":
            register_impl(st, _sleepy(a.sleep))
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
