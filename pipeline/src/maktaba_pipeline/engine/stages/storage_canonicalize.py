"""``storage_canonicalize`` (story 31.3): the home converts its stored paths to
canonical share paths by itself (:mod:`maktaba_pipeline.sharepath.auto`). The home's
agent enqueues it (catalog subject); it runs on the home's own machine only, whose
mounts decide the mapping."""

from __future__ import annotations

from typing import Any

from ...db.jobs import Job
from ..context import StageContext
from ..graph import StageResult
from ..hashing import canonical_hash
from .registry import StageImpl

__all__ = ["IMPL", "set_env"]

#: the environment the home's agent gives the stage (its mounts' callback); ``None``
#: on a worker that is not the home's own machine (it has a path map of its own)
_ENV: Any = None


def set_env(env: Any) -> None:
    global _ENV
    _ENV = env


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    from ...sharepath.auto import run_once

    if _ENV is None:
        out: dict[str, Any] = {"phase": "skipped", "why": "not the home's own machine"}
        return StageResult(output_hash=canonical_hash(["storage_canonicalize", out]), summary=out)
    state = await run_once(db, _ENV)
    out = {
        "phase": state.get("phase"),
        "problems": state.get("problems") or [],
        "plan": {k: v for k, v in (state.get("plan") or {}).items() if k != "columns"},
        "last": state.get("last"),
        "servers_added": [s.get("name") for s in state.get("servers_added") or []],
    }
    return StageResult(output_hash=canonical_hash(["storage_canonicalize", out]), summary=out)


IMPL = StageImpl(run=run)
